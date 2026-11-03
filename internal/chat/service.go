package chat

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"RyanDev-21.com/Chirpy/internal/platform/queue"
	"RyanDev-21.com/Chirpy/internal/ws"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// GroupMembership is implemented by groups.GroupService.
type GroupMembership interface {
	IsMember(ctx context.Context, groupID, userID uuid.UUID) (bool, error)
	Members(ctx context.Context, groupID uuid.UUID) ([]uuid.UUID, error)
}

// FriendChecker is implemented by friends.Service.
type FriendChecker interface {
	AreFriends(ctx context.Context, a, b uuid.UUID) (bool, error)
}

// Pusher delivers realtime events (implemented by *ws.Hub).
type Pusher interface {
	WriteToUsers(targets []uuid.UUID, ev ws.Event) error
}

type ChatService interface {
	SendMessage(ctx context.Context, from uuid.UUID, in InCommingMessage) (uuid.UUID, error)
	PrivateMessages(ctx context.Context, me, other uuid.UUID, since string) ([]MessageMetaDataRes, error)
	GroupMessages(ctx context.Context, me, groupID uuid.UUID) ([]MessageMetaDataRes, error)
	MarkSeen(ctx context.Context, me, other uuid.UUID, msgID string) error

	HandlePrivateMessage(ctx context.Context, j JobMessage) error
	HandlePublicMessage(ctx context.Context, j JobMessage) error
	HandleSeen(ctx context.Context, j JobSeen) error
}

type chatService struct {
	repo    ChatRepo
	cache   Cache
	groups  GroupMembership
	friends FriendChecker
	push    Pusher
	queue   queue.Publisher
	logger  *slog.Logger
}

func NewChatService(repo ChatRepo, cache Cache, groups GroupMembership, friends FriendChecker, push Pusher, q queue.Publisher, logger *slog.Logger) ChatService {
	return &chatService{repo: repo, cache: cache, groups: groups, friends: friends, push: push, queue: q, logger: logger}
}

func (s *chatService) SendMessage(ctx context.Context, from uuid.UUID, in InCommingMessage) (uuid.UUID, error) {
	in.Content = strings.TrimSpace(in.Content)
	if n := utf8.RuneCountInString(in.Content); n == 0 || n > maxMessageLen {
		return uuid.Nil, ErrEmptyMessage
	}
	to, err := uuid.Parse(in.ToID)
	if err != nil || to == from {
		return uuid.Nil, ErrNotValidToID
	}
	if in.ParendID != "" {
		if _, err := uuid.Parse(in.ParendID); err != nil {
			return uuid.Nil, ErrNotValidParentID
		}
	}

	var (
		key     chatKey
		targets []uuid.UUID
		topic   string
	)
	switch in.Type {
	case TypePrivate:
		ok, err := s.friends.AreFriends(ctx, from, to)
		if err != nil {
			return uuid.Nil, err
		}
		if !ok {
			return uuid.Nil, ErrNotFriends
		}
		key, targets, topic = privateKey(from, to), []uuid.UUID{to}, TopicPrivateMessage
	case TypePublic:
		members, err := s.groups.Members(ctx, to)
		if err != nil {
			return uuid.Nil, err
		}
		if !slices.Contains(members, from) {
			return uuid.Nil, ErrNotAuthorized
		}
		key, topic = groupKey(to), TopicPublicMessage
		targets = slices.DeleteFunc(members, func(id uuid.UUID) bool { return id == from })
	default:
		return uuid.Nil, ErrNotSupportedTypeMsg
	}

	msgID, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, err
	}
	now := time.Now().UTC()
	msg := MessageMetaDataRes{
		ID:        msgID,
		CreatedAt: now,
		MsgInfo:   MessageCache{Msg: in, FromID: from, CreatedAt: now},
	}

	// seed the stream first so history is not hidden behind the new message
	if err := s.ensureSeeded(ctx, key, from, to, in.Type); err != nil {
		return uuid.Nil, err
	}
	if err := s.cache.Append(ctx, key, msg); err != nil {
		return uuid.Nil, err
	}
	pubCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := s.queue.Publish(pubCtx, topic, JobMessage{Message: msg}); err != nil {
		return uuid.Nil, err
	}
	if err := s.push.WriteToUsers(targets, ws.Event{Event: ws.EventMessage, Payload: ws.OutGoingMessage{
		ID:        msgID.String(),
		Content:   in.Content,
		ParentID:  in.ParendID,
		FromID:    from.String(),
		Type:      in.Type,
		CreatedAt: now,
	}}); err != nil {
		s.logger.Warn("failed to push message", "error", err)
	}
	return msgID, nil
}

// ensureSeeded loads recent history from postgres into the stream once.
func (s *chatService) ensureSeeded(ctx context.Context, key chatKey, a, b uuid.UUID, kind string) error {
	ok, err := s.cache.Loaded(ctx, key)
	if err != nil || ok {
		return err
	}
	var msgs []MessageMetaDataRes
	if kind == TypePrivate {
		msgs, err = s.repo.RecentPrivate(ctx, a, b, streamCap)
	} else {
		msgs, err = s.repo.RecentPublic(ctx, b, streamCap)
	}
	if err != nil {
		return err
	}
	return s.cache.Seed(ctx, key, msgs)
}

func parseSince(since string) (time.Time, error) {
	if since == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339Nano, since)
	if err != nil {
		return time.Time{}, ErrNotValidTimeFmt
	}
	return t, nil
}

func (s *chatService) PrivateMessages(ctx context.Context, me, other uuid.UUID, since string) ([]MessageMetaDataRes, error) {
	sinceT, err := parseSince(since)
	if err != nil {
		return nil, err
	}
	key := privateKey(me, other)
	if err := s.ensureSeeded(ctx, key, me, other, TypePrivate); err != nil {
		return nil, err
	}
	if sinceT.IsZero() {
		return s.cache.Recent(ctx, key, pageSize)
	}
	cached, err := s.cache.Recent(ctx, key, streamCap)
	if err != nil {
		return nil, err
	}
	// the cache only answers if it reaches back past `since`
	if len(cached) < streamCap || (len(cached) > 0 && !cached[0].CreatedAt.After(sinceT)) {
		return slices.DeleteFunc(cached, func(m MessageMetaDataRes) bool { return !m.CreatedAt.After(sinceT) }), nil
	}
	return s.repo.PrivateSince(ctx, me, other, sinceT, streamCap)
}

func (s *chatService) GroupMessages(ctx context.Context, me, groupID uuid.UUID) ([]MessageMetaDataRes, error) {
	ok, err := s.groups.IsMember(ctx, groupID, me)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotAuthorized
	}
	key := groupKey(groupID)
	if err := s.ensureSeeded(ctx, key, me, groupID, TypePublic); err != nil {
		return nil, err
	}
	return s.cache.Recent(ctx, key, pageSize)
}

func (s *chatService) MarkSeen(ctx context.Context, me, other uuid.UUID, msgIDStr string) error {
	msgID, err := uuid.Parse(msgIDStr)
	if err != nil {
		return ErrNotValidUUID
	}
	chatID := PrivateChatID(me, other)
	if err := s.cache.SetSeen(ctx, chatID, me, msgID); err != nil {
		return err
	}
	pubCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := s.queue.Publish(pubCtx, TopicSeen, JobSeen{ChatID: chatID, MsgID: msgID, SeenID: me}); err != nil {
		return err
	}
	if err := s.push.WriteToUsers([]uuid.UUID{other}, ws.Event{Event: ws.EventSeen, Payload: ws.OutGoingEventForSeen{
		MsgID: msgID.String(), FromID: me.String(),
	}}); err != nil {
		s.logger.Warn("failed to push seen event", "error", err)
	}
	return nil
}

// --- workers ---

// classify keeps foreign-key errors retryable (the parent message or the
// group may still be queued) and drops data errors.
func classify(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && strings.HasPrefix(pgErr.Code, "22") {
		return queue.Permanent(err)
	}
	return err
}

func (s *chatService) HandlePrivateMessage(ctx context.Context, j JobMessage) error {
	return classify(s.repo.AddPrivate(ctx, j.Message))
}

func (s *chatService) HandlePublicMessage(ctx context.Context, j JobMessage) error {
	return classify(s.repo.AddPublic(ctx, j.Message))
}

func (s *chatService) HandleSeen(ctx context.Context, j JobSeen) error {
	return classify(s.repo.UpdateLastSeen(ctx, j.ChatID, j.SeenID, j.MsgID))
}
