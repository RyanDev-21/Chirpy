// Package discover suggests people to connect with ("people you may
// know"): opt-in users ranked by mutual friends, shared interests and
// being in the same place.
package discover

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"RyanDev-21.com/Chirpy/internal/catalog"
	"RyanDev-21.com/Chirpy/internal/database"
	"RyanDev-21.com/Chirpy/internal/platform/httpx"
	"RyanDev-21.com/Chirpy/internal/platform/reqctx"
	"RyanDev-21.com/Chirpy/internal/users"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrNotDiscoverable: you only see others after making yourself visible.
var ErrNotDiscoverable = errors.New(`turn on "discoverable" in your profile to see suggestions`)

const (
	defaultLimit = 20
	maxLimit     = 50
	maxPage      = 10
)

// Suggestion is one person you may know.
type Suggestion struct {
	ID              uuid.UUID      `json:"id"`
	Name            string         `json:"name"`
	AvatarURL       *string        `json:"avatar_url"`
	Location        *catalog.Place `json:"location"`
	SharedInterests []string       `json:"shared_interests"`
	MutualFriends   int            `json:"mutual_friends"`
}

type Service struct{ q *database.Queries }

func NewService(q *database.Queries) *Service { return &Service{q: q} }

// Suggestions returns a page of people for me, best matches first.
func (s *Service) Suggestions(ctx context.Context, me uuid.UUID, limit, page int) ([]Suggestion, error) {
	u, err := s.q.GetUserInfoByID(ctx, me)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, users.ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	// reciprocal: browsing others needs being visible yourself
	if !u.Discoverable {
		return nil, ErrNotDiscoverable
	}
	rows, err := s.q.DiscoverSuggestions(ctx, database.DiscoverSuggestionsParams{Me: me, Lim: int32(limit), Off: int32(page * limit)})
	if err != nil {
		return nil, err
	}
	out := make([]Suggestion, 0, len(rows))
	for _, r := range rows {
		sg := Suggestion{ID: r.ID, Name: r.Name, Location: users.PlaceOf(r.LocationID.String), SharedInterests: r.SharedInterests, MutualFriends: int(r.MutualFriends)}
		if r.AvatarAt.Valid {
			sg.AvatarURL = users.AvatarURL(r.ID, r.AvatarAt.Time)
		}
		if sg.SharedInterests == nil {
			sg.SharedInterests = []string{}
		}
		out = append(out, sg)
	}
	return out, nil
}

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Options handles GET /api/discover/options: the place and interest lists.
func (h *Handler) Options(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, max-age=86400")
	httpx.JSON(w, http.StatusOK, catalog.AllOptions())
}

// Suggest handles GET /api/discover?limit=&page=.
func (h *Handler) Suggest(w http.ResponseWriter, r *http.Request) {
	me, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	limit, page := defaultLimit, 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if limit, err = strconv.Atoi(v); err != nil || limit < 1 || limit > maxLimit {
			httpx.Error(w, http.StatusBadRequest, "limit must be 1-50")
			return
		}
	}
	if v := r.URL.Query().Get("page"); v != "" {
		if page, err = strconv.Atoi(v); err != nil || page < 0 || page >= maxPage {
			httpx.Error(w, http.StatusBadRequest, "page must be 0-9")
			return
		}
	}
	list, err := h.svc.Suggestions(r.Context(), me, limit, page)
	switch {
	case err == nil:
		httpx.JSON(w, http.StatusOK, map[string][]Suggestion{"suggestions": list})
	case errors.Is(err, ErrNotDiscoverable):
		httpx.Error(w, http.StatusForbidden, err.Error())
	case errors.Is(err, users.ErrUserNotFound):
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
	default:
		httpx.ServerError(w, r, err)
	}
}
