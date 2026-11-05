package chat

import (
	"RyanDev-21.com/Chirpy/internal/groups"
	"RyanDev-21.com/Chirpy/internal/users"
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"RyanDev-21.com/Chirpy/internal/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type ChatRepo interface {
	AddPrivate(ctx context.Context, m MessageMetaDataRes) error
	AddPublic(ctx context.Context, m MessageMetaDataRes) error
	// Recent* return the newest n messages in chronological order.
	RecentPrivate(ctx context.Context, a, b uuid.UUID, n int) ([]MessageMetaDataRes, error)
	PrivateSince(ctx context.Context, a, b uuid.UUID, since time.Time, n int) ([]MessageMetaDataRes, error)
	// group history only includes what was sent since viewer joined
	RecentPublic(ctx context.Context, groupID, viewer uuid.UUID, n int) ([]MessageMetaDataRes, error)
	UpdateLastSeen(ctx context.Context, chatID string, userID, msgID uuid.UUID) error
	// ParentInPrivate / ParentInGroup report whether a reply target belongs
	// to the same conversation.
	ParentInPrivate(ctx context.Context, parentID, a, b uuid.UUID) (bool, error)
	ParentInGroup(ctx context.Context, parentID, groupID uuid.UUID) (bool, error)
	// *Before return up to n messages older than the cursor message, in
	// chronological order; ErrBadCursor if it isn't in that chat.
	PrivateBefore(ctx context.Context, a, b, before uuid.UUID, n int) ([]MessageMetaDataRes, error)
	GroupBefore(ctx context.Context, groupID, viewer, before uuid.UUID, n int) ([]MessageMetaDataRes, error)
	// JoinedAt is when userID joined the group (ErrNotAuthorized if not in it).
	JoinedAt(ctx context.Context, groupID, userID uuid.UUID) (time.Time, error)
	// MarkGroupRead reports whether the marker moved forward.
	MarkGroupRead(ctx context.Context, groupID, userID, msgID uuid.UUID) (bool, error)
	// GroupReadBy lists current members, other than the sender, who have
	// read up to (or past) the message.
	GroupReadBy(ctx context.Context, ref messageRef, msgID uuid.UUID) ([]Reader, error)
	// RecentGroupSenders: who sent the last 50 messages up to msgID.
	RecentGroupSenders(ctx context.Context, groupID, msgID uuid.UUID) ([]uuid.UUID, error)
	Conversations(ctx context.Context, me uuid.UUID) ([]Conversation, error)

	// LocateMessage finds which chat a message belongs to (ErrMessageNotFound).
	LocateMessage(ctx context.Context, msgID uuid.UUID) (messageRef, error)
	SetReaction(ctx context.Context, kind string, msgID, userID uuid.UUID, emoji string) error
	// DeleteReaction reports whether there was a reaction to remove.
	DeleteReaction(ctx context.Context, kind string, msgID, userID uuid.UUID) (bool, error)
	// ReactionCounts returns per-message counts for a page of messages.
	ReactionCounts(ctx context.Context, kind string, me uuid.UUID, ids []uuid.UUID) (map[uuid.UUID][]ReactionCount, error)
	Reactors(ctx context.Context, kind string, msgID uuid.UUID) ([]Reactor, error)

	// EditMessage / DeleteMessage report false if the message is already
	// deleted. Deleting also removes its reactions.
	EditMessage(ctx context.Context, kind string, msgID uuid.UUID, content string, at time.Time) (bool, error)
	DeleteMessage(ctx context.Context, kind string, msgID uuid.UUID, at time.Time) (bool, error)
	// Suspended returns which of these users are banned (cached briefly).
	Suspended(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]bool, error)
	// ForgetBans drops the cached list (after a ban or unban here).
	ForgetBans()
}

type chatRepo struct {
	q      *database.Queries
	banned *bannedCache
}

func NewChatRepo(q *database.Queries) ChatRepo { return &chatRepo{q: q, banned: &bannedCache{}} }

func pgUUID(id uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: true} }

func pgOptUUID(s string) pgtype.UUID {
	id, err := uuid.Parse(s)
	if err != nil {
		return pgtype.UUID{}
	}
	return pgUUID(id)
}

func pgText(s string) pgtype.Text         { return pgtype.Text{String: s, Valid: true} }
func pgTime(t time.Time) pgtype.Timestamp { return pgtype.Timestamp{Time: t, Valid: true} }

func optTime(t pgtype.Timestamp) *time.Time {
	if !t.Valid {
		return nil
	}
	u := t.Time.UTC()
	return &u
}

func optString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return uuid.UUID(id.Bytes).String()
}

func (r *chatRepo) AddPrivate(ctx context.Context, m MessageMetaDataRes) error {
	return r.q.AddMessagePrivate(ctx, database.AddMessagePrivateParams{
		ID:        m.ID,
		Content:   pgText(m.MsgInfo.Msg.Content),
		Parentid:  pgOptUUID(m.MsgInfo.Msg.ParendID),
		FromID:    pgUUID(m.MsgInfo.FromID),
		ToID:      pgOptUUID(m.MsgInfo.Msg.ToID),
		CreatedAt: pgTime(m.CreatedAt),
	})
}

func (r *chatRepo) AddPublic(ctx context.Context, m MessageMetaDataRes) error {
	return r.q.AddMessagePublic(ctx, database.AddMessagePublicParams{
		ID:        m.ID,
		Content:   pgText(m.MsgInfo.Msg.Content),
		GroupID:   pgOptUUID(m.MsgInfo.Msg.ToID),
		FromID:    pgUUID(m.MsgInfo.FromID),
		ParentID:  pgOptUUID(m.MsgInfo.Msg.ParendID),
		CreatedAt: pgTime(m.CreatedAt),
	})
}

func fromPrivate(rows []database.Message) []MessageMetaDataRes {
	out := make([]MessageMetaDataRes, 0, len(rows))
	for _, m := range rows {
		created := m.CreatedAt.Time.UTC()
		out = append(out, MessageMetaDataRes{
			ID:        m.ID,
			CreatedAt: created,
			EditedAt:  optTime(m.EditedAt),
			Deleted:   m.DeletedAt.Valid,
			MsgInfo: MessageCache{
				FromID:    uuid.UUID(m.FromID.Bytes),
				CreatedAt: created,
				Msg: InCommingMessage{
					Content:  m.Content.String,
					ParendID: optString(m.Parentid),
					ToID:     optString(m.ToID),
					Type:     TypePrivate,
				},
			},
		})
	}
	return out
}

func fromPublic(rows []database.Groupmessage) []MessageMetaDataRes {
	out := make([]MessageMetaDataRes, 0, len(rows))
	for _, m := range rows {
		created := m.CreatedAt.Time.UTC()
		out = append(out, MessageMetaDataRes{
			ID:        m.ID,
			CreatedAt: created,
			EditedAt:  optTime(m.EditedAt),
			Deleted:   m.DeletedAt.Valid,
			MsgInfo: MessageCache{
				FromID:    uuid.UUID(m.FromID.Bytes),
				CreatedAt: created,
				Msg: InCommingMessage{
					Content:  m.Content.String,
					ParendID: optString(m.ParentID),
					ToID:     optString(m.GroupID),
					Type:     TypePublic,
				},
			},
		})
	}
	return out
}

func (r *chatRepo) RecentPrivate(ctx context.Context, a, b uuid.UUID, n int) ([]MessageMetaDataRes, error) {
	rows, err := r.q.GetRecentPrivateMessages(ctx, database.GetRecentPrivateMessagesParams{UserA: pgUUID(a), UserB: pgUUID(b), Lim: int32(n)})
	if err != nil {
		return nil, err
	}
	out := fromPrivate(rows)
	slices.Reverse(out)
	return out, nil
}

func (r *chatRepo) PrivateSince(ctx context.Context, a, b uuid.UUID, since time.Time, n int) ([]MessageMetaDataRes, error) {
	rows, err := r.q.GetPrivateMessagesSince(ctx, database.GetPrivateMessagesSinceParams{UserA: pgUUID(a), UserB: pgUUID(b), Since: pgTime(since.UTC()), Lim: int32(n)})
	if err != nil {
		return nil, err
	}
	return fromPrivate(rows), nil
}

func (r *chatRepo) RecentPublic(ctx context.Context, groupID, viewer uuid.UUID, n int) ([]MessageMetaDataRes, error) {
	rows, err := r.q.GetRecentGroupMessages(ctx, database.GetRecentGroupMessagesParams{GroupID: pgUUID(groupID), Viewer: viewer, Lim: int32(n)})
	if err != nil {
		return nil, err
	}
	out := fromPublic(rows)
	slices.Reverse(out)
	return out, nil
}

func (r *chatRepo) UpdateLastSeen(ctx context.Context, chatID string, userID, msgID uuid.UUID) error {
	return r.q.AddLastSeenMessage(ctx, database.AddLastSeenMessageParams{ChatID: chatID, MessageID: msgID, SeenID: userID})
}

func (r *chatRepo) ParentInPrivate(ctx context.Context, parentID, a, b uuid.UUID) (bool, error) {
	return r.q.PrivateMessageInChat(ctx, database.PrivateMessageInChatParams{ID: parentID, UserA: pgUUID(a), UserB: pgUUID(b)})
}

func (r *chatRepo) ParentInGroup(ctx context.Context, parentID, groupID uuid.UUID) (bool, error) {
	return r.q.GroupMessageInGroup(ctx, database.GroupMessageInGroupParams{ID: parentID, GroupID: pgUUID(groupID)})
}

func (r *chatRepo) PrivateBefore(ctx context.Context, a, b, before uuid.UUID, n int) ([]MessageMetaDataRes, error) {
	at, err := r.q.PrivateMessageCursor(ctx, database.PrivateMessageCursorParams{ID: before, UserA: pgUUID(a), UserB: pgUUID(b)})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrBadCursor
	}
	if err != nil {
		return nil, err
	}
	rows, err := r.q.GetPrivateMessagesBefore(ctx, database.GetPrivateMessagesBeforeParams{
		UserA: pgUUID(a), UserB: pgUUID(b), BeforeTime: at, BeforeID: before, Lim: int32(n),
	})
	if err != nil {
		return nil, err
	}
	out := fromPrivate(rows)
	slices.Reverse(out)
	return out, nil
}

func (r *chatRepo) JoinedAt(ctx context.Context, groupID, userID uuid.UUID) (time.Time, error) {
	t, err := r.q.GroupJoinedAt(ctx, database.GroupJoinedAtParams{GroupID: groupID, MemberID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, ErrNotAuthorized
	}
	return t.Time, err
}

func (r *chatRepo) GroupBefore(ctx context.Context, groupID, viewer, before uuid.UUID, n int) ([]MessageMetaDataRes, error) {
	at, err := r.q.GroupMessageCursor(ctx, database.GroupMessageCursorParams{ID: before, GroupID: pgUUID(groupID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrBadCursor
	}
	if err != nil {
		return nil, err
	}
	rows, err := r.q.GetGroupMessagesBefore(ctx, database.GetGroupMessagesBeforeParams{
		GroupID: pgUUID(groupID), Viewer: viewer, BeforeTime: at, BeforeID: before, Lim: int32(n),
	})
	if err != nil {
		return nil, err
	}
	out := fromPublic(rows)
	slices.Reverse(out)
	return out, nil
}

func (r *chatRepo) MarkGroupRead(ctx context.Context, groupID, userID, msgID uuid.UUID) (bool, error) {
	if _, err := r.q.GroupMessageCursor(ctx, database.GroupMessageCursorParams{ID: msgID, GroupID: pgUUID(groupID)}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, ErrMessageNotInChat
		}
		return false, err
	}
	n, err := r.q.MarkGroupRead(ctx, database.MarkGroupReadParams{GroupID: groupID, UserID: userID, MessageID: msgID})
	return n > 0, err
}

func (r *chatRepo) RecentGroupSenders(ctx context.Context, groupID, msgID uuid.UUID) ([]uuid.UUID, error) {
	return r.q.RecentGroupSenders(ctx, database.RecentGroupSendersParams{GroupID: pgUUID(groupID), MsgID: msgID})
}

func (r *chatRepo) GroupReadBy(ctx context.Context, ref messageRef, msgID uuid.UUID) ([]Reader, error) {
	rows, err := r.q.GroupReadBy(ctx, database.GroupReadByParams{GroupID: ref.Group, Sender: ref.From, MsgAt: pgTime(ref.CreatedAt), MsgID: msgID})
	if err != nil {
		return nil, err
	}
	out := make([]Reader, 0, len(rows))
	for _, x := range rows {
		out = append(out, Reader{UserID: x.ID, Name: x.Name, DisplayName: users.ShownName(x.Name, x.DisplayName),
			AvatarURL: users.AvatarURLFrom(x.ID, x.AvatarAt), ReadAt: x.ReadAt.Time.UTC()})
	}
	return out, nil
}

func lastMessage(id uuid.UUID, content pgtype.Text, from pgtype.UUID, at pgtype.Timestamp, deleted bool) *LastMessage {
	if !at.Valid {
		return nil
	}
	return &LastMessage{ID: id, Msg: content.String, FromID: uuid.UUID(from.Bytes), CreatedAt: at.Time.UTC(), Deleted: deleted}
}

// Conversations lists private chats and groups, most recent activity first;
// groups without messages come last, by name.
func (r *chatRepo) Conversations(ctx context.Context, me uuid.UUID) ([]Conversation, error) {
	priv, err := r.q.ListPrivateConversations(ctx, me)
	if err != nil {
		return nil, err
	}
	grp, err := r.q.ListGroupConversations(ctx, me)
	if err != nil {
		return nil, err
	}
	out := make([]Conversation, 0, len(priv)+len(grp))
	for _, p := range priv {
		out = append(out, Conversation{Type: TypePrivate, ID: p.OtherID, Name: p.OtherName, DisplayName: users.ShownName(p.OtherName, p.OtherDisplay), AvatarURL: users.AvatarURLFrom(p.OtherID, p.OtherAvatarAt),
			LastMessage: lastMessage(p.LastID, p.LastContent, p.LastFrom, p.LastAt, p.LastDeleted), Unread: int(p.Unread)})
	}
	for _, g := range grp {
		out = append(out, Conversation{Type: TypePublic, ID: g.GroupID, Name: g.GroupName, DisplayName: g.GroupName, AvatarURL: groups.AvatarURL(g.GroupID, g.GroupAvatarAt),
			LastMessage: lastMessage(g.LastID, g.LastContent, g.LastFrom, g.LastAt, g.LastDeleted), Unread: int(g.Unread)})
	}
	slices.SortStableFunc(out, func(a, b Conversation) int {
		switch {
		case a.LastMessage == nil && b.LastMessage == nil:
			return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		case a.LastMessage == nil:
			return 1
		case b.LastMessage == nil:
			return -1
		}
		return b.LastMessage.CreatedAt.Compare(a.LastMessage.CreatedAt)
	})
	return out, nil
}

func (r *chatRepo) LocateMessage(ctx context.Context, msgID uuid.UUID) (messageRef, error) {
	p, err := r.q.PrivateMessageParticipants(ctx, msgID)
	if err == nil {
		return messageRef{Kind: TypePrivate, From: uuid.UUID(p.FromID.Bytes), To: uuid.UUID(p.ToID.Bytes), CreatedAt: p.CreatedAt.Time.UTC()}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return messageRef{}, err
	}
	g, err := r.q.GroupMessageGroup(ctx, msgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return messageRef{}, ErrMessageNotFound
	}
	if err != nil {
		return messageRef{}, err
	}
	return messageRef{Kind: TypePublic, Group: uuid.UUID(g.GroupID.Bytes), From: uuid.UUID(g.FromID.Bytes), CreatedAt: g.CreatedAt.Time.UTC()}, nil
}

func (r *chatRepo) SetReaction(ctx context.Context, kind string, msgID, userID uuid.UUID, emoji string) error {
	if kind == TypePrivate {
		return r.q.SetPrivateReaction(ctx, database.SetPrivateReactionParams{MessageID: msgID, UserID: userID, Emoji: emoji})
	}
	return r.q.SetGroupReaction(ctx, database.SetGroupReactionParams{MessageID: msgID, UserID: userID, Emoji: emoji})
}

func (r *chatRepo) DeleteReaction(ctx context.Context, kind string, msgID, userID uuid.UUID) (bool, error) {
	var n int64
	var err error
	if kind == TypePrivate {
		n, err = r.q.DeletePrivateReaction(ctx, database.DeletePrivateReactionParams{MessageID: msgID, UserID: userID})
	} else {
		n, err = r.q.DeleteGroupReaction(ctx, database.DeleteGroupReactionParams{MessageID: msgID, UserID: userID})
	}
	return n > 0, err
}

func (r *chatRepo) ReactionCounts(ctx context.Context, kind string, me uuid.UUID, ids []uuid.UUID) (map[uuid.UUID][]ReactionCount, error) {
	out := make(map[uuid.UUID][]ReactionCount)
	if len(ids) == 0 {
		return out, nil
	}
	add := func(id uuid.UUID, emoji string, n int32, mine bool) {
		out[id] = append(out[id], ReactionCount{Emoji: emoji, Count: int(n), Me: mine})
	}
	if kind == TypePrivate {
		rows, err := r.q.PrivateReactionSummary(ctx, database.PrivateReactionSummaryParams{Me: me, Ids: ids})
		if err != nil {
			return nil, err
		}
		for _, x := range rows {
			add(x.MessageID, x.Emoji, x.N, x.Mine)
		}
		return out, nil
	}
	rows, err := r.q.GroupReactionSummary(ctx, database.GroupReactionSummaryParams{Me: me, Ids: ids})
	if err != nil {
		return nil, err
	}
	for _, x := range rows {
		add(x.MessageID, x.Emoji, x.N, x.Mine)
	}
	return out, nil
}

func (r *chatRepo) Reactors(ctx context.Context, kind string, msgID uuid.UUID) ([]Reactor, error) {
	out := []Reactor{}
	if kind == TypePrivate {
		rows, err := r.q.PrivateReactors(ctx, msgID)
		if err != nil {
			return nil, err
		}
		for _, x := range rows {
			out = append(out, Reactor{UserID: x.UserID, Name: x.Name, DisplayName: users.ShownName(x.Name, x.DisplayName), Emoji: x.Emoji, CreatedAt: x.CreatedAt.Time.UTC()})
		}
		return out, nil
	}
	rows, err := r.q.GroupReactors(ctx, msgID)
	if err != nil {
		return nil, err
	}
	for _, x := range rows {
		out = append(out, Reactor{UserID: x.UserID, Name: x.Name, DisplayName: users.ShownName(x.Name, x.DisplayName), Emoji: x.Emoji, CreatedAt: x.CreatedAt.Time.UTC()})
	}
	return out, nil
}

func (r *chatRepo) EditMessage(ctx context.Context, kind string, msgID uuid.UUID, content string, at time.Time) (bool, error) {
	var n int64
	var err error
	if kind == TypePrivate {
		n, err = r.q.EditPrivateMessage(ctx, database.EditPrivateMessageParams{ID: msgID, Content: pgText(content), At: pgTime(at)})
	} else {
		n, err = r.q.EditGroupMessage(ctx, database.EditGroupMessageParams{ID: msgID, Content: pgText(content), At: pgTime(at)})
	}
	return n > 0, err
}

func (r *chatRepo) DeleteMessage(ctx context.Context, kind string, msgID uuid.UUID, at time.Time) (bool, error) {
	var n int32
	var err error
	if kind == TypePrivate {
		n, err = r.q.DeletePrivateMessage(ctx, database.DeletePrivateMessageParams{ID: msgID, At: pgTime(at)})
	} else {
		n, err = r.q.DeleteGroupMessage(ctx, database.DeleteGroupMessageParams{ID: msgID, At: pgTime(at)})
	}
	return n > 0, err
}

func (r *chatRepo) Suspended(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]bool, error) {
	out := map[uuid.UUID]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	banned, err := r.banned.get(ctx, r.q)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if banned[id] {
			out[id] = true
		}
	}
	return out, nil
}

// bannedCache holds the list of banned accounts for bannedTTL, so pages of
// history don't each ask the database. A new ban hides messages within
// bannedTTL (the ban itself, sign-out and login refusal, is immediate).
type bannedCache struct {
	mu      sync.Mutex
	ids     map[uuid.UUID]bool
	fetched time.Time
}

const bannedTTL = 30 * time.Second

func (c *bannedCache) get(ctx context.Context, q *database.Queries) (map[uuid.UUID]bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ids != nil && time.Since(c.fetched) < bannedTTL {
		return c.ids, nil
	}
	rows, err := q.AllBannedUsers(ctx)
	if err != nil {
		return nil, err
	}
	ids := make(map[uuid.UUID]bool, len(rows))
	for _, id := range rows {
		ids[id] = true
	}
	c.ids, c.fetched = ids, time.Now()
	return ids, nil
}

func (r *chatRepo) ForgetBans() {
	r.banned.mu.Lock()
	r.banned.ids = nil
	r.banned.mu.Unlock()
}
