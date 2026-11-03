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

	var (
		key     chatKey
		targets []uuid.UUID
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
		key, targets = privateKey(from, to), []uuid.UUID{to}
	case TypePublic:
		members, err := s.groups.Members(ctx, to)
		if err != nil {
			return uuid.Nil, err
		}
		if !slices.Contains(members, from) {
			return uuid.Nil, ErrNotAuthorized
		}
		key = groupKey(to)
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

	// Seed before inserting: a seed read after the insert would already
	// contain this message and the append below would duplicate it.
	if err := s.ensureSeeded(ctx, key, from, to, in.Type); err != nil {
		return uuid.Nil, err
	}
	// postgres is the source of truth: the message is durable before we answer
	if in.Type == TypePrivate {
		err = s.repo.AddPrivate(ctx, msg)
	} else {
		err = s.repo.AddPublic(ctx, msg)
	}
	if err != nil {
		return uuid.Nil, err
	}
	if err := s.cache.Append(ctx, key, msg); err != nil {
		// the message is saved; drop the stream so the next read reseeds it
		s.logger.WarnContext(ctx, "failed to append message to cache, invalidating", "error", err)
		if err := s.cache.Invalidate(ctx, key); err != nil {
			s.logger.ErrorContext(ctx, "failed to invalidate chat cache", "error", err)
		}
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
