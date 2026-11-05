// Package search finds messages on the server, for what the phone no
// longer has locally: the app searches its own store first and calls the
// server only for older messages (before = its oldest local message).
//
// Only private chats and groups of up to 50 members are indexed (see
// migration 045). Results are limited to what the caller can read:
// their private chats, and their groups' messages sent since they joined.
// Deleted messages and messages of suspended senders never match.
package search

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"RyanDev-21.com/Chirpy/internal/database"
	"RyanDev-21.com/Chirpy/internal/platform/httpx"
	"RyanDev-21.com/Chirpy/internal/platform/reqctx"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	defaultLimit = 20
	maxLimit     = 50
	maxQuery     = 100
	// with fewer than 3 characters the trigram index can't help, so short
	// queries need a chat to search in
	minGlobalQuery = 3
	minChatQuery   = 2
)

var (
	ErrBadQuery  = errors.New("q must be 3-100 characters (2 when searching one chat)")
	ErrBadParams = errors.New("type must be private or public with chat_id; after and before must be RFC3339 (after earlier than before); limit 1-50")
)

// Store is implemented by *database.Queries.
type Store interface {
	SearchPrivateMessages(ctx context.Context, arg database.SearchPrivateMessagesParams) ([]database.SearchPrivateMessagesRow, error)
	SearchGroupMessages(ctx context.Context, arg database.SearchGroupMessagesParams) ([]database.SearchGroupMessagesRow, error)
}

// Suspensions is implemented by the chat repo (cached banned users).
type Suspensions interface {
	Suspended(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]bool, error)
}

// Hit is one matching message.
type Hit struct {
	MessageID uuid.UUID `json:"message_id"`
	Type      string    `json:"type"`    // private or public
	ChatID    uuid.UUID `json:"chat_id"` // the other person, or the group
	FromID    uuid.UUID `json:"from_id"` // zero if the sender deleted their account
	Content   string    `json:"msg"`
	CreatedAt time.Time `json:"created_at"`
}

// Query is one search.
type Query struct {
	Q      string
	Type   string    // "" = everywhere
	ChatID uuid.UUID // with Type
	Before time.Time // zero = newest
	// BeforeID with Before continues exactly after a result (next_before_id)
	BeforeID uuid.UUID
	After    time.Time // zero = no lower bound: with Before, a date range
	Limit    int
}

type Service struct {
	db  Store
	ban Suspensions
}

func NewService(db Store, ban Suspensions) *Service { return &Service{db: db, ban: ban} }

// escapeLike makes q a literal LIKE pattern (with ESCAPE '\').
func escapeLike(q string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
}

var maxID = uuid.Max

func ts(t time.Time) pgtype.Timestamp { return pgtype.Timestamp{Time: t.UTC(), Valid: true} }

func (s *Service) Search(ctx context.Context, me uuid.UUID, q Query) ([]Hit, error) {
	text := strings.ToLower(strings.TrimSpace(q.Q))
	n := utf8.RuneCountInString(text)
	min := minGlobalQuery
	if q.Type != "" {
		min = minChatQuery
	}
	if n < min || n > maxQuery {
		return nil, ErrBadQuery
	}
	if q.Limit == 0 {
		q.Limit = defaultLimit
	}
	if !q.After.IsZero() && !q.Before.IsZero() && !q.After.Before(q.Before) {
		return nil, ErrBadParams
	}
	var after pgtype.Timestamp
	if !q.After.IsZero() {
		after = ts(q.After)
	}
	if q.Limit < 1 || q.Limit > maxLimit || (q.Type != "" && q.Type != "private" && q.Type != "public") || (q.Type != "") != (q.ChatID != uuid.Nil) {
		return nil, ErrBadParams
	}
	before := q.Before
	if before.IsZero() {
		before = time.Now().Add(time.Hour)
	}
	beforeID := maxID // only Before: everything sent before (or at) that time
	if q.BeforeID != uuid.Nil {
		beforeID = q.BeforeID
	}
	pattern := escapeLike(text)
	// ask for a few more, since suspended senders' messages are dropped
	lim := int32(q.Limit + 5)

	var hits []Hit
	if q.Type == "" || q.Type == "private" {
		p := database.SearchPrivateMessagesParams{Me: me, Q: pattern, BeforeAt: ts(before), BeforeID: beforeID, AfterAt: after, Lim: lim}
		if q.Type == "private" {
			p.WithUser = pgtype.UUID{Bytes: q.ChatID, Valid: true}
		}
		rows, err := s.db.SearchPrivateMessages(ctx, p)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			from, to := uuid.UUID(r.FromID.Bytes), uuid.UUID(r.ToID.Bytes)
			other := to
			if to == me {
				other = from
			}
			hits = append(hits, Hit{MessageID: r.ID, Type: "private", ChatID: other, FromID: from, Content: r.Content.String, CreatedAt: r.CreatedAt.Time.UTC()})
		}
	}
	if q.Type == "" || q.Type == "public" {
		p := database.SearchGroupMessagesParams{Me: me, Q: pattern, BeforeAt: ts(before), BeforeID: beforeID, AfterAt: after, Lim: lim}
		if q.Type == "public" {
			p.InGroup = pgtype.UUID{Bytes: q.ChatID, Valid: true}
		}
		rows, err := s.db.SearchGroupMessages(ctx, p)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			hits = append(hits, Hit{MessageID: r.ID, Type: "public", ChatID: uuid.UUID(r.GroupID.Bytes), FromID: uuid.UUID(r.FromID.Bytes),
				Content: r.Content.String, CreatedAt: r.CreatedAt.Time.UTC()})
		}
	}

	// suspended senders' messages are hidden everywhere else, so here too
	senders := make([]uuid.UUID, 0, len(hits))
	for _, h := range hits {
		if h.FromID != uuid.Nil {
			senders = append(senders, h.FromID)
		}
	}
	banned, err := s.ban.Suspended(ctx, senders)
	if err != nil {
		return nil, err
	}
	hits = slices.DeleteFunc(hits, func(h Hit) bool { return banned[h.FromID] })
	slices.SortFunc(hits, func(a, b Hit) int {
		if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(b.MessageID.String(), a.MessageID.String())
	})
	if len(hits) > q.Limit {
		hits = hits[:q.Limit]
	}
	return hits, nil
}

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Search handles GET /api/chats/search?q=&type=&chat_id=&after=&before=&before_id=&limit=.
func (h *Handler) Search(w http.ResponseWriter, r *http.Request) {
	me, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	v := r.URL.Query()
	q := Query{Q: v.Get("q"), Type: v.Get("type")}
	if s := v.Get("chat_id"); s != "" {
		if q.ChatID, err = uuid.Parse(s); err != nil {
			httpx.Error(w, http.StatusBadRequest, ErrBadParams.Error())
			return
		}
	}
	if s := v.Get("before"); s != "" {
		if q.Before, err = time.Parse(time.RFC3339Nano, s); err != nil {
			httpx.Error(w, http.StatusBadRequest, ErrBadParams.Error())
			return
		}
	}
	if s := v.Get("after"); s != "" {
		if q.After, err = time.Parse(time.RFC3339Nano, s); err != nil {
			httpx.Error(w, http.StatusBadRequest, ErrBadParams.Error())
			return
		}
	}
	if s := v.Get("before_id"); s != "" {
		if q.BeforeID, err = uuid.Parse(s); err != nil || q.Before.IsZero() {
			httpx.Error(w, http.StatusBadRequest, ErrBadParams.Error())
			return
		}
	}
	if s := v.Get("limit"); s != "" {
		if q.Limit, err = strconv.Atoi(s); err != nil || q.Limit == 0 {
			httpx.Error(w, http.StatusBadRequest, ErrBadParams.Error())
			return
		}
	}
	hits, err := h.svc.Search(r.Context(), me, q)
	switch {
	case errors.Is(err, ErrBadQuery), errors.Is(err, ErrBadParams):
		httpx.Error(w, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		httpx.ServerError(w, r, err)
		return
	}
	out := map[string]any{"results": hits}
	if len(hits) > 0 {
		last := hits[len(hits)-1]
		out["next_before"], out["next_before_id"] = last.CreatedAt.Format(time.RFC3339Nano), last.MessageID
	}
	httpx.JSON(w, http.StatusOK, out)
}
