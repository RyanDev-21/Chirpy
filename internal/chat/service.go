package chat

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"RyanDev-21.com/Chirpy/internal/groups"
	"RyanDev-21.com/Chirpy/internal/platform/queue"
	"RyanDev-21.com/Chirpy/internal/ws"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	streamCap = 200
	pageSize  = 30 // default history page: fits Render free's CPU slice
	maxPage   = 100
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
	PrivateMessages(ctx context.Context, me, other uuid.UUID, q HistoryQuery) ([]MessageMetaDataRes, error)
	GroupMessages(ctx context.Context, me, groupID uuid.UUID, q HistoryQuery) ([]MessageMetaDataRes, error)
	MarkGroupRead(ctx context.Context, me, groupID uuid.UUID, msgID string) error
	Conversations(ctx context.Context, me uuid.UUID) ([]Conversation, error)
	// React sets the caller's single reaction on a message (replacing any
	// earlier one); Unreact removes it.
	React(ctx context.Context, me, msgID uuid.UUID, emoji string) error
	Unreact(ctx context.Context, me, msgID uuid.UUID) error
	Reactors(ctx context.Context, me, msgID uuid.UUID) ([]Reactor, error)
	// EditMessage changes the text of the caller's own message, within
	// EditWindow of sending; DeleteMessage deletes it for everyone.
	EditMessage(ctx context.Context, me, msgID uuid.UUID, content string) (EditedMessage, error)
	DeleteMessage(ctx context.Context, me, msgID uuid.UUID) error
	// ForgetGroup is retained for the group lifecycle hook; group messages are
	// no longer cached.
	ForgetGroup(ctx context.Context, groupID uuid.UUID)
	MarkSeen(ctx context.Context, me, other uuid.UUID, msgID string) error

	HandleSeen(ctx context.Context, j JobSeen) error

	// ws.EventPolicy: who may receive client-sent typing and seen events.
	TypingTargets(ctx context.Context, from, to uuid.UUID, group bool) ([]uuid.UUID, error)
	SeenAllowed(ctx context.Context, from, to uuid.UUID) (bool, error)
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
	var parentID uuid.UUID
	if in.ParendID != "" {
		if parentID, err = uuid.Parse(in.ParendID); err != nil {
			return uuid.Nil, ErrNotValidParentID
		}
	}

	var targets []uuid.UUID
	switch in.Type {
	case TypePrivate:
		ok, err := s.friends.AreFriends(ctx, from, to)
		if err != nil {
			return uuid.Nil, err
		}
		if !ok {
			return uuid.Nil, ErrNotFriends
		}
		targets = []uuid.UUID{to}
	case TypePublic:
		members, err := s.groups.Members(ctx, to)
		if err != nil {
			return uuid.Nil, err
		}
		if !slices.Contains(members, from) {
			return uuid.Nil, ErrNotAuthorized
		}
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

	if parentID != uuid.Nil {
		ok, err := s.parentInChat(ctx, in.Type, parentID, from, to)
		if err != nil {
			return uuid.Nil, err
		}
		if !ok {
			return uuid.Nil, ErrNotValidParentID
		}
	}

	if in.Type == TypePrivate {
		err = s.repo.AddPrivate(ctx, msg)
	} else {
		err = s.repo.AddPublic(ctx, msg)
	}
	if err != nil {
		return uuid.Nil, err
	}
	out := ws.OutGoingMessage{
		ID:        msgID.String(),
		Content:   in.Content,
		ParentID:  in.ParendID,
		FromID:    from.String(),
		Type:      in.Type,
		CreatedAt: now,
	}
	if in.Type == TypePublic {
		out.GroupID = to.String()
	}
	if err := s.push.WriteToUsers(targets, ws.Event{Event: ws.EventMessage, Payload: out}); err != nil {
		s.logger.WarnContext(ctx, "failed to push message", "error", err)
	}
	return msgID, nil
}

func (s *chatService) parentInChat(ctx context.Context, kind string, parentID, from, to uuid.UUID) (bool, error) {
	if kind == TypePrivate {
		return s.repo.ParentInPrivate(ctx, parentID, from, to)
	}
	return s.repo.ParentInGroup(ctx, parentID, to)
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

// HistoryQuery selects a page of history: the latest page by default,
// newer messages with Since, or older ones with Before (a message id).
type HistoryQuery struct {
	Since  string
	Before string
	Limit  int
}

func (q HistoryQuery) parse() (before uuid.UUID, limit int, err error) {
	if q.Since != "" && q.Before != "" {
		return uuid.Nil, 0, ErrSinceAndBefore
	}
	limit = pageSize
	if q.Limit > 0 {
		limit = min(q.Limit, maxPage)
	}
	if q.Before != "" {
		if before, err = uuid.Parse(q.Before); err != nil {
			return uuid.Nil, 0, ErrBadCursor
		}
	}
	return before, limit, nil
}

func (s *chatService) PrivateMessages(ctx context.Context, me, other uuid.UUID, q HistoryQuery) ([]MessageMetaDataRes, error) {
	msgs, err := s.privateHistory(ctx, me, other, q)
	if err != nil {
		return nil, err
	}
	return s.withState(ctx, TypePrivate, me, msgs)
}

func (s *chatService) privateHistory(ctx context.Context, me, other uuid.UUID, q HistoryQuery) ([]MessageMetaDataRes, error) {
	before, limit, err := q.parse()
	if err != nil {
		return nil, err
	}
	if before != uuid.Nil {
		return s.repo.PrivateBefore(ctx, me, other, before, limit)
	}

	sinceT, err := parseSince(q.Since)
	if err != nil {
		return nil, err
	}
	if !sinceT.IsZero() {
		return s.repo.PrivateSince(ctx, me, other, sinceT, streamCap)
	}
	return s.repo.RecentPrivate(ctx, me, other, limit)
}

func (s *chatService) GroupMessages(ctx context.Context, me, groupID uuid.UUID, q HistoryQuery) ([]MessageMetaDataRes, error) {
	msgs, err := s.groupHistory(ctx, me, groupID, q)
	if err != nil {
		return nil, err
	}
	return s.withState(ctx, TypePublic, me, msgs)
}

func (s *chatService) groupHistory(ctx context.Context, me, groupID uuid.UUID, q HistoryQuery) ([]MessageMetaDataRes, error) {
	if q.Since != "" {
		return nil, ErrSinceNotSupported
	}
	before, limit, err := q.parse()
	if err != nil {
		return nil, err
	}
	ok, err := s.groups.IsMember(ctx, groupID, me)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotAuthorized
	}
	if before != uuid.Nil {
		return s.repo.GroupBefore(ctx, groupID, before, limit)
	}
	return s.repo.RecentPublic(ctx, groupID, limit)
}

// MarkGroupRead records the last group message the caller has read.
// Markers only move forward.
func (s *chatService) MarkGroupRead(ctx context.Context, me, groupID uuid.UUID, msgIDStr string) error {
	msgID, err := uuid.Parse(msgIDStr)
	if err != nil {
		return ErrNotValidUUID
	}
	ok, err := s.groups.IsMember(ctx, groupID, me)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotAuthorized
	}
	return s.repo.MarkGroupRead(ctx, groupID, me, msgID)
}

func (s *chatService) Conversations(ctx context.Context, me uuid.UUID) ([]Conversation, error) {
	return s.repo.Conversations(ctx, me)
}

func (s *chatService) MarkSeen(ctx context.Context, me, other uuid.UUID, msgIDStr string) error {
	msgID, err := uuid.Parse(msgIDStr)
	if err != nil {
		return ErrNotValidUUID
	}
	// the message must belong to this conversation, otherwise anyone could
	// send read receipts to anyone for arbitrary ids
	inChat, err := s.repo.ParentInPrivate(ctx, msgID, me, other)
	if err != nil {
		return err
	}
	if !inChat {
		return ErrMessageNotInChat
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
	// the receipt is stored either way; it is only pushed to friends
	if ok, err := s.SeenAllowed(ctx, me, other); err == nil && ok {
		if err := s.push.WriteToUsers([]uuid.UUID{other}, ws.Event{Event: ws.EventSeen, Payload: ws.OutGoingEventForSeen{
			MsgID: msgID.String(), FromID: me.String(),
		}}); err != nil {
			s.logger.WarnContext(ctx, "failed to push seen event", "error", err)
		}
	}
	return nil
}

// --- current state of a page ---

// withState attaches reaction counts to a page of messages (one indexed
// query); edits and deletions are already in the rows read from postgres.
func (s *chatService) withState(ctx context.Context, kind string, me uuid.UUID, msgs []MessageMetaDataRes) ([]MessageMetaDataRes, error) {
	ids := make([]uuid.UUID, len(msgs))
	for i, m := range msgs {
		ids[i] = m.ID
	}
	counts, err := s.repo.ReactionCounts(ctx, kind, me, ids)
	if err != nil {
		return nil, err
	}
	for i := range msgs {
		m := &msgs[i]
		if m.Deleted {
			m.MsgInfo.Msg.Content = ""
		}
		m.Reactions = counts[m.ID]
		if m.Reactions == nil {
			m.Reactions = []ReactionCount{}
		}
	}
	return msgs, nil
}

// --- edit and delete ---

func (s *chatService) EditMessage(ctx context.Context, me, msgID uuid.UUID, content string) (EditedMessage, error) {
	content = strings.TrimSpace(content)
	if n := utf8.RuneCountInString(content); n == 0 || n > maxMessageLen {
		return EditedMessage{}, ErrEmptyMessage
	}
	// editing is sending new text, so private edits need the friendship too
	ref, others, err := s.authorizeMessage(ctx, me, msgID, true)
	if err != nil {
		return EditedMessage{}, err
	}
	if ref.From != me {
		return EditedMessage{}, ErrNotYourMessage
	}
	now := time.Now().UTC()
	if now.Sub(ref.CreatedAt) > EditWindow {
		return EditedMessage{}, ErrEditWindowClosed
	}
	ok, err := s.repo.EditMessage(ctx, ref.Kind, msgID, content, now)
	if err != nil {
		return EditedMessage{}, err
	}
	if !ok {
		return EditedMessage{}, ErrMessageNotFound // deleted meanwhile
	}
	ev := ws.OutEditedEvent{MsgID: msgID.String(), FromID: me.String(), Type: ref.Kind, Content: content, EditedAt: now}
	if ref.Kind == TypePublic {
		ev.GroupID = ref.Group.String()
	}
	if err := s.push.WriteToUsers(others, ws.Event{Event: ws.EventEdited, Payload: ev}); err != nil {
		s.logger.WarnContext(ctx, "failed to push edit", "error", err)
	}
	return EditedMessage{MsgID: msgID, Content: content, EditedAt: now}, nil
}

func (s *chatService) DeleteMessage(ctx context.Context, me, msgID uuid.UUID) error {
	// no friendship needed: you can always take back what you said
	ref, others, err := s.authorizeMessage(ctx, me, msgID, false)
	if err != nil {
		return err
	}
	if ref.From != me {
		return ErrNotYourMessage
	}
	ok, err := s.repo.DeleteMessage(ctx, ref.Kind, msgID, time.Now().UTC())
	if err != nil {
		return err
	}
	if !ok {
		return ErrMessageNotFound
	}
	ev := ws.OutDeletedEvent{MsgID: msgID.String(), FromID: me.String(), Type: ref.Kind}
	if ref.Kind == TypePublic {
		ev.GroupID = ref.Group.String()
	}
	if err := s.push.WriteToUsers(others, ws.Event{Event: ws.EventDeleted, Payload: ev}); err != nil {
		s.logger.WarnContext(ctx, "failed to push delete", "error", err)
	}
	return nil
}

func (s *chatService) ForgetGroup(ctx context.Context, groupID uuid.UUID) {
	// nothing cached per group anymore
}

// --- reactions ---

// authorizeMessage returns where msgID lives and who else should hear
// about changes to it, if me may read it. requireFriends applies the
// "friends only" rule of private messaging (needed to add, not to remove).
func (s *chatService) authorizeMessage(ctx context.Context, me, msgID uuid.UUID, requireFriends bool) (messageRef, []uuid.UUID, error) {
	ref, err := s.repo.LocateMessage(ctx, msgID)
	if err != nil {
		return ref, nil, err
	}
	if ref.Kind == TypePrivate {
		var other uuid.UUID
		switch me {
		case ref.From:
			other = ref.To
		case ref.To:
			other = ref.From
		default:
			return ref, nil, ErrMessageNotFound // not your conversation: don't reveal it
		}
		if requireFriends {
			ok, err := s.friends.AreFriends(ctx, me, other)
			if err != nil {
				return ref, nil, err
			}
			if !ok {
				return ref, nil, ErrNotFriends
			}
		}
		return ref, []uuid.UUID{other}, nil
	}
	members, err := s.groups.Members(ctx, ref.Group)
	if err != nil {
		return ref, nil, err
	}
	if !slices.Contains(members, me) {
		return ref, nil, ErrMessageNotFound
	}
	return ref, slices.DeleteFunc(members, func(id uuid.UUID) bool { return id == me }), nil
}

func (s *chatService) React(ctx context.Context, me, msgID uuid.UUID, emoji string) error {
	if err := ValidateEmoji(emoji); err != nil {
		return err
	}
	ref, others, err := s.authorizeMessage(ctx, me, msgID, true)
	if err != nil {
		return err
	}
	if err := s.repo.SetReaction(ctx, ref.Kind, msgID, me, emoji); err != nil {
		return err
	}
	s.pushReaction(ctx, ref, others, me, msgID, emoji)
	return nil
}

func (s *chatService) Unreact(ctx context.Context, me, msgID uuid.UUID) error {
	ref, others, err := s.authorizeMessage(ctx, me, msgID, false)
	if err != nil {
		return err
	}
	removed, err := s.repo.DeleteReaction(ctx, ref.Kind, msgID, me)
	if err != nil {
		return err
	}
	if removed {
		s.pushReaction(ctx, ref, others, me, msgID, "")
	}
	return nil
}

func (s *chatService) Reactors(ctx context.Context, me, msgID uuid.UUID) ([]Reactor, error) {
	ref, _, err := s.authorizeMessage(ctx, me, msgID, false)
	if err != nil {
		return nil, err
	}
	return s.repo.Reactors(ctx, ref.Kind, msgID)
}

// pushReaction tells the rest of the chat; emoji "" means removed.
func (s *chatService) pushReaction(ctx context.Context, ref messageRef, to []uuid.UUID, from, msgID uuid.UUID, emoji string) {
	ev := ws.OutReactionEvent{MsgID: msgID.String(), FromID: from.String(), Type: ref.Kind, Emoji: emoji}
	if ref.Kind == TypePublic {
		ev.GroupID = ref.Group.String()
	}
	if err := s.push.WriteToUsers(to, ws.Event{Event: ws.EventReaction, Payload: ev}); err != nil {
		s.logger.WarnContext(ctx, "failed to push reaction", "error", err)
	}
}

// --- realtime event policy (ws.EventPolicy) ---

// TypingTargets: private typing only between friends; group typing only
// from a member, delivered to the other members.
func (s *chatService) TypingTargets(ctx context.Context, from, to uuid.UUID, group bool) ([]uuid.UUID, error) {
	if !group {
		ok, err := s.friends.AreFriends(ctx, from, to)
		if err != nil || !ok {
			return nil, err
		}
		return []uuid.UUID{to}, nil
	}
	members, err := s.groups.Members(ctx, to)
	if err != nil || !slices.Contains(members, from) {
		return nil, ignoreNotFound(err)
	}
	return slices.DeleteFunc(members, func(id uuid.UUID) bool { return id == from }), nil
}

// SeenAllowed: read receipts only between friends.
func (s *chatService) SeenAllowed(ctx context.Context, from, to uuid.UUID) (bool, error) {
	if from == to {
		return false, nil
	}
	return s.friends.AreFriends(ctx, from, to)
}

// ignoreNotFound treats an unknown group as "no recipients", not an error.
func ignoreNotFound(err error) error {
	if errors.Is(err, groups.ErrNotFoundGroup) {
		return nil
	}
	return err
}

// --- workers ---

// classify retries foreign-key errors and drops data errors.
func classify(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && strings.HasPrefix(pgErr.Code, "22") {
		return queue.Permanent(err)
	}
	return err
}

func (s *chatService) HandleSeen(ctx context.Context, j JobSeen) error {
	return classify(s.repo.UpdateLastSeen(ctx, j.ChatID, j.SeenID, j.MsgID))
}
