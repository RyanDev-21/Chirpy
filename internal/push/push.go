// Package push sends notifications to phones (Firebase Cloud Messaging)
// for users who aren't connected: new messages, friend requests and
// acceptances, and reactions to their messages. Users choose which kinds
// they want and can mute single chats.
package push

import (
	"RyanDev-21.com/Chirpy/internal/platform/telemetry"
	"context"
	"errors"
	"log/slog"
	"slices"
	"time"
	"unicode/utf8"

	"RyanDev-21.com/Chirpy/internal/database"
	"RyanDev-21.com/Chirpy/internal/platform/queue"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// TopicPush carries Jobs; sending happens off the request path.
const TopicPush = "push.notify"

// Kinds of notification.
const (
	KindPrivate       = "private"        // private message
	KindGroup         = "public"         // group message
	KindFriendRequest = "friend_request" // someone sent me a request
	KindFriendAccept  = "friend_accept"  // someone accepted my request
	KindReaction      = "reaction"       // someone reacted to my message
	KindGroupAdded    = "group_added"    // a group admin or moderator added me
	KindCall          = "call"           // incoming call: data-only, rings the phone
	KindMissedCall    = "missed_call"    // a call I didn't answer
)

// CallRingTTL: an incoming-call push that arrives later than this is useless.
const CallRingTTL = 45 * time.Second

// Job describes something to tell users about.
type Job struct {
	Kind string      `json:"kind"`
	To   []uuid.UUID `json:"to"`
	From uuid.UUID   `json:"from"`
	// Group is set for group messages and reactions in groups.
	Group   uuid.UUID `json:"group,omitempty"`
	MsgID   uuid.UUID `json:"msg_id,omitempty"`
	Preview string    `json:"preview,omitempty"` // message text, or the emoji for reactions
	CallID  uuid.UUID `json:"call_id,omitempty"`
}

const maxPreview = 120

// Notifier is what chat and friends call; implemented by Dispatcher.
type Notifier interface {
	Notify(ctx context.Context, j Job)
}

// Dispatcher queues jobs. Losing one (e.g. the in-memory queue on restart)
// only loses a notification, never a message.
type Dispatcher struct {
	q      queue.Publisher
	logger *slog.Logger
}

func NewDispatcher(q queue.Publisher, logger *slog.Logger) *Dispatcher {
	return &Dispatcher{q: q, logger: logger}
}

func (d *Dispatcher) Notify(ctx context.Context, j Job) {
	if len(j.To) == 0 {
		return
	}
	if utf8.RuneCountInString(j.Preview) > maxPreview {
		j.Preview = string([]rune(j.Preview)[:maxPreview-1]) + "…"
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	if err := d.q.Publish(ctx, TopicPush, j); err != nil {
		d.logger.WarnContext(ctx, "failed to queue notification", "error", err)
	}
}

// Presence reports whether a user has a live connection (*ws.Hub).
type Presence interface {
	Online(userID uuid.UUID) bool
}

// Names resolves display names.
type Names interface {
	GetName(ctx context.Context, id uuid.UUID) (string, error)
	GroupName(ctx context.Context, id uuid.UUID) (string, error)
}

// Service turns jobs into notifications and serves the settings routes.
type Service struct {
	q      *database.Queries
	send   Sender // nil: push is off (no FIREBASE_SERVICE_ACCOUNT)
	online Presence
	names  Names
	logger *slog.Logger
	now    func() time.Time
}

func NewService(q *database.Queries, send Sender, online Presence, names Names, logger *slog.Logger) *Service {
	return &Service{q: q, send: send, online: online, names: names, logger: logger, now: time.Now}
}

// prefKind is the preference that controls a kind.
func prefKind(kind string) string {
	switch kind {
	case KindFriendRequest, KindFriendAccept:
		return "friends"
	case KindReaction:
		return "reactions"
	case KindGroupAdded:
		return KindGroup
	case KindCall, KindMissedCall:
		return "calls"
	}
	return kind
}

// Handle is the TopicPush worker.
func (s *Service) Handle(ctx context.Context, j Job) error {
	if s.send == nil {
		return nil
	}
	// the app shows live events itself; only offline users get a push
	to := slices.DeleteFunc(slices.Clone(j.To), func(id uuid.UUID) bool { return id == j.From || s.online.Online(id) })
	if len(to) == 0 {
		return nil
	}
	// the chat a mute applies to, as the recipient sees it
	chatType, chatID := "private", j.From
	if j.Group != uuid.Nil {
		chatType, chatID = "public", j.Group
	}
	targets, err := s.q.PushTargets(ctx, database.PushTargetsParams{
		Users: to, Kind: prefKind(j.Kind), ChatType: chatType, ChatID: chatID,
		Now: pgtype.Timestamp{Time: s.now().UTC(), Valid: true},
	})
	if err != nil || len(targets) == 0 {
		return err
	}
	msg, err := s.render(ctx, j)
	if err != nil {
		return err
	}
	var dead []string
	for _, t := range targets {
		m := msg
		m.Token = t.Token
		switch err := s.send.Send(ctx, m); {
		case errors.Is(err, ErrDeadToken):
			dead = append(dead, t.Token)
			telemetry.Push(ctx, j.Kind, "dead_token")
		case err != nil:
			s.logger.WarnContext(ctx, "push failed", "kind", j.Kind, "error", err)
			telemetry.Push(ctx, j.Kind, "failed")
		default:
			telemetry.Push(ctx, j.Kind, "sent")
		}
	}
	if len(dead) > 0 {
		if err := s.q.DeleteDeadTokens(ctx, dead); err != nil {
			s.logger.WarnContext(ctx, "failed to forget dead device tokens", "error", err)
		}
	}
	return nil
}

func (s *Service) render(ctx context.Context, j Job) (Message, error) {
	from, err := s.names.GetName(ctx, j.From)
	if err != nil {
		return Message{}, err
	}
	m := Message{Data: map[string]string{"type": j.Kind, "from_id": j.From.String()}}
	if j.MsgID != uuid.Nil {
		m.Data["msg_id"] = j.MsgID.String()
	}
	group := ""
	if j.Group != uuid.Nil {
		m.Data["group_id"] = j.Group.String()
		if group, err = s.names.GroupName(ctx, j.Group); err != nil {
			return Message{}, err
		}
	}
	switch j.Kind {
	case KindPrivate:
		m.Title, m.Body, m.Tag = from, j.Preview, "dm:"+j.From.String()
	case KindGroup:
		m.Title, m.Body, m.Tag = group, from+": "+j.Preview, "group:"+j.Group.String()
	case KindFriendRequest:
		m.Title, m.Body, m.Tag = "Friend request", from+" sent you a friend request", "friends"
	case KindFriendAccept:
		m.Title, m.Body, m.Tag = "Friend request accepted", from+" accepted your friend request", "friends"
	case KindGroupAdded:
		m.Title, m.Body, m.Tag = group, from+" added you to the group", "group:"+j.Group.String()
	case KindCall:
		m.DataOnly, m.TTL = true, CallRingTTL
		m.Data["call_id"], m.Data["from_name"] = j.CallID.String(), from
	case KindMissedCall:
		m.Title, m.Body, m.Tag = "Missed call", from, "call:"+j.From.String()
		m.Data["call_id"] = j.CallID.String()
	case KindReaction:
		m.Title, m.Body, m.Tag = from, "reacted "+j.Preview+" to your message", "reaction:"+j.MsgID.String()
		if group != "" {
			m.Title = from + " in " + group
		}
	}
	return m, nil
}

// --- settings ---

// Prefs says which kinds of notification a user wants.
type Prefs struct {
	Private   bool `json:"private"`
	Groups    bool `json:"groups"`
	Friends   bool `json:"friends"`
	Reactions bool `json:"reactions"`
}

func (s *Service) Prefs(ctx context.Context, user uuid.UUID) (Prefs, error) {
	p, err := s.q.GetNotificationPrefs(ctx, user)
	if errors.Is(err, pgx.ErrNoRows) {
		return Prefs{true, true, true, true}, nil
	}
	return Prefs{p.Private, p.Groups, p.Friends, p.Reactions}, err
}

func (s *Service) SetPrefs(ctx context.Context, user uuid.UUID, p Prefs) error {
	return s.q.SetNotificationPrefs(ctx, database.SetNotificationPrefsParams{
		UserID: user, Private: p.Private, Groups: p.Groups, Friends: p.Friends, Reactions: p.Reactions,
	})
}

// Mute is one muted chat.
type Mute struct {
	Type  string     `json:"type"` // "private" or "public"
	ID    uuid.UUID  `json:"id"`
	Until *time.Time `json:"until"` // null: until unmuted
}

var ErrBadMute = errors.New(`type must be "private" or "public", id a uuid, until a future RFC3339 time or null`)

func (s *Service) Mute(ctx context.Context, user uuid.UUID, m Mute) error {
	if (m.Type != "private" && m.Type != "public") || m.ID == uuid.Nil || (m.Until != nil && !m.Until.After(s.now())) {
		return ErrBadMute
	}
	until := pgtype.Timestamp{}
	if m.Until != nil {
		until = pgtype.Timestamp{Time: m.Until.UTC(), Valid: true}
	}
	return s.q.SetMute(ctx, database.SetMuteParams{UserID: user, ChatType: m.Type, ChatID: m.ID, Until: until})
}

func (s *Service) Unmute(ctx context.Context, user uuid.UUID, chatType string, id uuid.UUID) error {
	return s.q.DeleteMute(ctx, database.DeleteMuteParams{UserID: user, ChatType: chatType, ChatID: id})
}

func (s *Service) Mutes(ctx context.Context, user uuid.UUID) ([]Mute, error) {
	rows, err := s.q.ListMutes(ctx, database.ListMutesParams{UserID: user, Now: pgtype.Timestamp{Time: s.now().UTC(), Valid: true}})
	if err != nil {
		return nil, err
	}
	out := make([]Mute, 0, len(rows))
	for _, r := range rows {
		m := Mute{Type: r.ChatType, ID: r.ChatID}
		if r.Until.Valid {
			t := r.Until.Time.UTC()
			m.Until = &t
		}
		out = append(out, m)
	}
	return out, nil
}

// --- devices ---

var ErrBadDevice = errors.New("token is required (at most 4096 characters); platform is \"android\" or \"ios\"")

func (s *Service) RegisterDevice(ctx context.Context, user uuid.UUID, token, platform string) error {
	if platform == "" {
		platform = "android"
	}
	if token == "" || len(token) > 4096 || (platform != "android" && platform != "ios") {
		return ErrBadDevice
	}
	return s.q.UpsertDevice(ctx, database.UpsertDeviceParams{Token: token, UserID: user, Platform: platform})
}

func (s *Service) RemoveDevice(ctx context.Context, user uuid.UUID, token string) error {
	return s.q.DeleteDevice(ctx, database.DeleteDeviceParams{Token: token, UserID: user})
}

// ForgetDevices removes all of a user's phones (when their sessions end).
func (s *Service) ForgetDevices(ctx context.Context, user uuid.UUID) error {
	return s.q.DeleteUserDevices(ctx, user)
}
