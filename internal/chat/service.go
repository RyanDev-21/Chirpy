package chat

import (
	"RyanDev-21.com/Chirpy/internal/platform/telemetry"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"RyanDev-21.com/Chirpy/internal/groups"
	"RyanDev-21.com/Chirpy/internal/media"
	"RyanDev-21.com/Chirpy/internal/platform/queue"
	"RyanDev-21.com/Chirpy/internal/push"
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
	CanDeleteMessage(ctx context.Context, groupID, actor, sender uuid.UUID) (bool, error)
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
	// ReadBy lists who has read the caller's own group message (groups of
	// up to ReadByMaxMembers).
	ReadBy(ctx context.Context, me, msgID uuid.UUID) ([]Reader, error)
	// EditMessage changes the text of the caller's own message, within
	// EditWindow of sending; DeleteMessage deletes it for everyone.
	EditMessage(ctx context.Context, me, msgID uuid.UUID, content string) (EditedMessage, error)
	DeleteMessage(ctx context.Context, me, msgID uuid.UUID) error
	// SetNotifier enables push notifications for messages and reactions.
	SetNotifier(n push.Notifier)
	// SetMedia enables photos and videos in messages.
	SetMedia(m MediaStore)
	// BansChanged refreshes which senders are suspended (after a ban/unban).
	BansChanged(ctx context.Context, userID uuid.UUID)
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
	notify  push.Notifier // nil: no push notifications
	media   MediaStore    // nil: no photos/videos
	logger  *slog.Logger
}

func NewChatService(repo ChatRepo, cache Cache, groups GroupMembership, friends FriendChecker, push Pusher, q queue.Publisher, logger *slog.Logger) ChatService {
	return &chatService{repo: repo, cache: cache, groups: groups, friends: friends, push: push, queue: q, logger: logger}
}

func (s *chatService) SendMessage(ctx context.Context, from uuid.UUID, in InCommingMessage) (uuid.UUID, error) {
	in.Content = strings.TrimSpace(in.Content)
	if n := utf8.RuneCountInString(in.Content); (n == 0 && len(in.Media) == 0) || n > maxMessageLen {
		return uuid.Nil, ErrEmptyMessage
	}
	if len(in.Media) > 0 && s.media == nil {
		return uuid.Nil, media.ErrDisabled
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

	// media first: if the message then fails to save, the uploads are
	// simply never shown and expire like any other
	var attached []media.Attachment
	if len(in.Media) > 0 {
		chatType := TypePrivate
		if in.Type == TypePublic {
			chatType = TypePublic
		}
		if attached, err = s.media.Attach(ctx, from, in.Media, chatType, to, msgID); err != nil {
			return uuid.Nil, err
		}
	}
	msg.MsgInfo.Msg.Media = nil
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
	if len(attached) > 0 {
		out.Media = attached
	}
	if in.Type == TypePublic {
		out.GroupID = to.String()
	} else {
		out.ToID = to.String()
	}
	// the sender's other devices (tablet, second phone) get it too
	if err := s.push.WriteToUsers(withMe(targets, from), ws.Event{Event: ws.EventMessage, Payload: out}); err != nil {
		s.logger.WarnContext(ctx, "failed to push message", "error", err)
	}
	telemetry.Message(ctx, in.Type, len(attached) > 0)
	if s.notify != nil {
		j := push.Job{Kind: push.KindPrivate, To: targets, From: from, MsgID: msgID, Preview: mediaPreview(in.Content, attached)}
		if in.Type == TypePublic {
			j.Kind, j.Group = push.KindGroup, to
		}
		s.notify.Notify(ctx, j)
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
		return s.repo.GroupBefore(ctx, groupID, me, before, limit)
	}
	return s.repo.RecentPublic(ctx, groupID, me, limit)
}

// MarkGroupRead records the last group message the caller has read.
// Markers only move forward.
func (s *chatService) MarkGroupRead(ctx context.Context, me, groupID uuid.UUID, msgIDStr string) error {
	msgID, err := uuid.Parse(msgIDStr)
	if err != nil {
		return ErrNotValidUUID
	}
	members, err := s.groups.Members(ctx, groupID)
	if err != nil {
		return err
	}
	if !slices.Contains(members, me) {
		return ErrNotAuthorized
	}
	moved, err := s.repo.MarkGroupRead(ctx, groupID, me, msgID)
	if err != nil || !moved || len(members) > ReadByMaxMembers {
		return err
	}
	// small groups: tell the senders of recent messages (read-by is only
	// shown to a message's sender) and my other devices, so ticks update live
	senders, err := s.repo.RecentGroupSenders(ctx, groupID, msgID)
	if err != nil {
		s.logger.WarnContext(ctx, "failed to find group senders", "error", err)
		return nil
	}
	to := []uuid.UUID{me}
	for _, id := range senders {
		if id != me && slices.Contains(members, id) {
			to = append(to, id)
		}
	}
	ev := ws.OutGroupReadEvent{GroupID: groupID.String(), UserID: me.String(), MsgID: msgID.String()}
	if err := s.push.WriteToUsers(to, ws.Event{Event: ws.EventGroupRead, Payload: ev}); err != nil {
		s.logger.WarnContext(ctx, "failed to push group read", "error", err)
	}
	return nil
}

func (s *chatService) Conversations(ctx context.Context, me uuid.UUID) ([]Conversation, error) {
	list, err := s.repo.Conversations(ctx, me)
	if err != nil {
		return nil, err
	}
	var senders []uuid.UUID
	for _, c := range list {
		if c.LastMessage != nil {
			senders = append(senders, c.LastMessage.FromID)
		}
	}
	banned, err := s.repo.Suspended(ctx, senders)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if lm := list[i].LastMessage; lm != nil && banned[lm.FromID] {
			lm.SenderSuspended, lm.Msg = true, ""
		}
	}
	return list, nil
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
	att := map[uuid.UUID][]media.Attachment{}
	if s.media != nil {
		if att, err = s.media.ForMessages(ctx, ids); err != nil {
			return nil, err
		}
	}
	senders := make([]uuid.UUID, 0, len(msgs))
	for _, m := range msgs {
		if m.MsgInfo.FromID != uuid.Nil && !slices.Contains(senders, m.MsgInfo.FromID) {
			senders = append(senders, m.MsgInfo.FromID)
		}
	}
	banned, err := s.repo.Suspended(ctx, senders)
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
		m.Media = att[m.ID]
		if m.Media == nil || m.Deleted {
			m.Media = []media.Attachment{}
		}
		if banned[m.MsgInfo.FromID] {
			m.SenderSuspended = true
			m.MsgInfo.Msg.Content = ""
			m.Media = []media.Attachment{}
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
	telemetry.ChatAction(ctx, "edit")
	ev := ws.OutEditedEvent{MsgID: msgID.String(), FromID: me.String(), Type: ref.Kind, Content: content, EditedAt: now}
	if ref.Kind == TypePublic {
		ev.GroupID = ref.Group.String()
	}
	if err := s.push.WriteToUsers(withMe(others, me), ws.Event{Event: ws.EventEdited, Payload: ev}); err != nil {
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
		// in groups the admin, and moderators allowed to, delete others' messages
		if ref.Kind != TypePublic {
			return ErrNotYourMessage
		}
		ok, err := s.groups.CanDeleteMessage(ctx, ref.Group, me, ref.From)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotYourMessage
		}
	}
	ok, err := s.repo.DeleteMessage(ctx, ref.Kind, msgID, time.Now().UTC())
	if err != nil {
		return err
	}
	if !ok {
		return ErrMessageNotFound
	}
	if s.media != nil {
		if err := s.media.ExpireMessage(ctx, msgID); err != nil {
			s.logger.WarnContext(ctx, "failed to expire media of deleted message", "error", err)
		}
	}
	telemetry.ChatAction(ctx, "delete")
	ev := ws.OutDeletedEvent{MsgID: msgID.String(), FromID: ref.From.String(), Type: ref.Kind}
	if ref.Kind == TypePublic {
		ev.GroupID = ref.Group.String()
	}
	if ref.From != me {
		ev.ByID = me.String() // removed by a moderator or the admin
	}
	if err := s.push.WriteToUsers(withMe(others, me), ws.Event{Event: ws.EventDeleted, Payload: ev}); err != nil {
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
	// messages from before I joined aren't mine to see
	joined, err := s.repo.JoinedAt(ctx, ref.Group, me)
	if errors.Is(err, ErrNotAuthorized) || (err == nil && ref.CreatedAt.Before(joined)) {
		return ref, nil, ErrMessageNotFound
	}
	if err != nil {
		return ref, nil, err
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
	s.pushReaction(ctx, ref, withMe(others, me), me, msgID, emoji)
	telemetry.ChatAction(ctx, "react")
	// the author hears about reactions to their message (not their own)
	if s.notify != nil && ref.From != me && ref.From != uuid.Nil {
		j := push.Job{Kind: push.KindReaction, To: []uuid.UUID{ref.From}, From: me, MsgID: msgID, Preview: emoji}
		if ref.Kind == TypePublic {
			j.Group = ref.Group
		}
		s.notify.Notify(ctx, j)
	}
	return nil
}

func (s *chatService) SetNotifier(n push.Notifier) { s.notify = n }

func (s *chatService) SetMedia(m MediaStore) { s.media = m }

// MediaStore is implemented by *media.Service.
type MediaStore interface {
	Attach(ctx context.Context, owner uuid.UUID, ids []uuid.UUID, chatType string, chatID, msgID uuid.UUID) ([]media.Attachment, error)
	ForMessages(ctx context.Context, msgIDs []uuid.UUID) (map[uuid.UUID][]media.Attachment, error)
	ExpireMessage(ctx context.Context, msgID uuid.UUID) error
}

// mediaPreview is the push text for a message: its text, or what it carries.
func mediaPreview(text string, att []media.Attachment) string {
	if text != "" || len(att) == 0 {
		return text
	}
	photos, videos := 0, 0
	for _, a := range att {
		if a.Kind == "video" {
			videos++
		} else {
			photos++
		}
	}
	switch {
	case videos == 0 && photos == 1:
		return "📷 Photo"
	case photos == 0 && videos == 1:
		return "🎥 Video"
	case videos == 0:
		return fmt.Sprintf("📷 %d photos", photos)
	case photos == 0:
		return fmt.Sprintf("🎥 %d videos", videos)
	}
	return fmt.Sprintf("📎 %d files", photos+videos)
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
		s.pushReaction(ctx, ref, withMe(others, me), me, msgID, "")
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

func (s *chatService) ReadBy(ctx context.Context, me, msgID uuid.UUID) ([]Reader, error) {
	ref, others, err := s.authorizeMessage(ctx, me, msgID, false)
	if err != nil {
		return nil, err
	}
	if ref.Kind != TypePublic {
		return nil, ErrNotGroupMessage
	}
	if ref.From != me {
		return nil, ErrNotYourMessage
	}
	if len(others)+1 > ReadByMaxMembers {
		return nil, ErrGroupTooBig
	}
	return s.repo.GroupReadBy(ctx, ref, msgID)
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

// withMe adds the acting user, so their other devices stay in sync. A new
// slice: callers keep using targets for push notifications.
func withMe(targets []uuid.UUID, me uuid.UUID) []uuid.UUID {
	return append(slices.Clone(targets), me)
}

func (s *chatService) BansChanged(context.Context, uuid.UUID) { s.repo.ForgetBans() }
