package chat

import (
	"context"
	"errors"
	"slices"
	"strings"
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
	RecentPublic(ctx context.Context, groupID uuid.UUID, n int) ([]MessageMetaDataRes, error)
	UpdateLastSeen(ctx context.Context, chatID string, userID, msgID uuid.UUID) error
	// ParentInPrivate / ParentInGroup report whether a reply target belongs
	// to the same conversation.
	ParentInPrivate(ctx context.Context, parentID, a, b uuid.UUID) (bool, error)
	ParentInGroup(ctx context.Context, parentID, groupID uuid.UUID) (bool, error)
	// *Before return up to n messages older than the cursor message, in
	// chronological order; ErrBadCursor if it isn't in that chat.
	PrivateBefore(ctx context.Context, a, b, before uuid.UUID, n int) ([]MessageMetaDataRes, error)
	GroupBefore(ctx context.Context, groupID, before uuid.UUID, n int) ([]MessageMetaDataRes, error)
	MarkGroupRead(ctx context.Context, groupID, userID, msgID uuid.UUID) error
	Conversations(ctx context.Context, me uuid.UUID) ([]Conversation, error)
}

type chatRepo struct {
	q *database.Queries
}

func NewChatRepo(q *database.Queries) ChatRepo { return &chatRepo{q: q} }

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

func (r *chatRepo) RecentPublic(ctx context.Context, groupID uuid.UUID, n int) ([]MessageMetaDataRes, error) {
	rows, err := r.q.GetRecentGroupMessages(ctx, database.GetRecentGroupMessagesParams{GroupID: pgUUID(groupID), Lim: int32(n)})
	if err != nil {
		return nil, err
	}
	out := fromPublic(rows)
	slices.Reverse(out)
	return out, nil
}

func (r *chatRepo) UpdateLastSeen(ctx context.Context, chatID string, userID, msgID uuid.UUID) error {
	return r.q.AddLastSeenMessage(ctx, database.AddLastSeenMessageParams{ChatID: chatID, MessageID: msgID, SeenID: pgUUID(userID)})
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

func (r *chatRepo) GroupBefore(ctx context.Context, groupID, before uuid.UUID, n int) ([]MessageMetaDataRes, error) {
	at, err := r.q.GroupMessageCursor(ctx, database.GroupMessageCursorParams{ID: before, GroupID: pgUUID(groupID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrBadCursor
	}
	if err != nil {
		return nil, err
	}
	rows, err := r.q.GetGroupMessagesBefore(ctx, database.GetGroupMessagesBeforeParams{
		GroupID: pgUUID(groupID), BeforeTime: at, BeforeID: before, Lim: int32(n),
	})
	if err != nil {
		return nil, err
	}
	out := fromPublic(rows)
	slices.Reverse(out)
	return out, nil
}

func (r *chatRepo) MarkGroupRead(ctx context.Context, groupID, userID, msgID uuid.UUID) error {
	if _, err := r.q.GroupMessageCursor(ctx, database.GroupMessageCursorParams{ID: msgID, GroupID: pgUUID(groupID)}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrMessageNotInChat
		}
		return err
	}
	return r.q.MarkGroupRead(ctx, database.MarkGroupReadParams{GroupID: groupID, UserID: userID, MessageID: msgID})
}

func lastMessage(id uuid.UUID, content pgtype.Text, from pgtype.UUID, at pgtype.Timestamp) *LastMessage {
	if !at.Valid {
		return nil
	}
	return &LastMessage{ID: id, Msg: content.String, FromID: uuid.UUID(from.Bytes), CreatedAt: at.Time.UTC()}
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
		out = append(out, Conversation{Type: TypePrivate, ID: p.OtherID, Name: p.OtherName,
			LastMessage: lastMessage(p.LastID, p.LastContent, p.LastFrom, p.LastAt), Unread: int(p.Unread)})
	}
	for _, g := range grp {
		out = append(out, Conversation{Type: TypePublic, ID: g.GroupID, Name: g.GroupName,
			LastMessage: lastMessage(g.LastID, g.LastContent, g.LastFrom, g.LastAt), Unread: int(g.Unread)})
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
