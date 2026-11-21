// Package notes: a short text a user shows their friends for 24 hours
// ("at the gym", "🎂 today!"). One note per user; setting a new one
// replaces it.
package notes

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"RyanDev-21.com/Chirpy/internal/database"
	"RyanDev-21.com/Chirpy/internal/platform/httpx"
	"RyanDev-21.com/Chirpy/internal/platform/reqctx"
	"RyanDev-21.com/Chirpy/internal/users"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	MaxLen   = 60
	Lifetime = 24 * time.Hour
)

// ErrBadText: empty, too long, or with line breaks or invisible characters.
var ErrBadText = errors.New("a note is 1-60 characters without line breaks or invisible characters")

// Validate allows spaces and emoji (including joined ones such as the technologist emoji), but
// not line breaks, control or invisible formatting characters.
func Validate(text string) error {
	if n := utf8.RuneCountInString(text); n == 0 || n > MaxLen {
		return ErrBadText
	}
	for _, r := range text {
		if r == '\u200d' { // zero-width joiner: part of many emoji
			continue
		}
		if (unicode.IsSpace(r) && r != ' ') || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) ||
			unicode.In(r, unicode.Zl, unicode.Zp) || r == utf8.RuneError {
			return ErrBadText
		}
	}
	return nil
}

// Note is one user's note as friends see it.
type Note struct {
	UserID      uuid.UUID `json:"user_id"`
	Name        string    `json:"name"`
	DisplayName string    `json:"display_name"`
	AvatarURL   *string   `json:"avatar_url"`
	Text        string    `json:"text"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	users.CharacterInfo
}

// Store is implemented by *database.Queries.
type Store interface {
	SetNote(ctx context.Context, arg database.SetNoteParams) (database.Note, error)
	DeleteNote(ctx context.Context, userID uuid.UUID) error
	FriendNotes(ctx context.Context, arg database.FriendNotesParams) ([]database.FriendNotesRow, error)
}

type Handler struct {
	db  Store
	now func() time.Time
}

func NewHandler(db Store) *Handler { return &Handler{db: db, now: time.Now} }

func ts(t time.Time) pgtype.Timestamp { return pgtype.Timestamp{Time: t.UTC(), Valid: true} }

// Set handles PUT /api/users/me/note {"text"}.
func (h *Handler) Set(w http.ResponseWriter, r *http.Request) {
	me, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var b struct {
		Text string `json:"text"`
	}
	if err := httpx.Decode(w, r, &b); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid parameters")
		return
	}
	text := strings.TrimSpace(b.Text)
	if err := Validate(text); err != nil {
		httpx.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	now := h.now().UTC()
	n, err := h.db.SetNote(r.Context(), database.SetNoteParams{UserID: me, Text: text, Now: ts(now), ExpiresAt: ts(now.Add(Lifetime))})
	if err != nil {
		httpx.ServerError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"text": n.Text, "created_at": n.CreatedAt.Time.UTC(), "expires_at": n.ExpiresAt.Time.UTC()})
}

// Delete handles DELETE /api/users/me/note.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	me, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if err := h.db.DeleteNote(r.Context(), me); err != nil {
		httpx.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// List handles GET /api/notes: my note first, then my friends', newest first.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	me, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	rows, err := h.db.FriendNotes(r.Context(), database.FriendNotesParams{Me: me, Now: ts(h.now())})
	if err != nil {
		httpx.ServerError(w, r, err)
		return
	}
	out := make([]Note, 0, len(rows))
	for _, x := range rows {
		out = append(out, Note{UserID: x.UserID, Name: x.Name, DisplayName: users.ShownName(x.Name, x.DisplayName),
			AvatarURL: users.AvatarURLFrom(x.UserID, x.AvatarAt), Text: x.Text, CharacterInfo: users.CharacterFrom(x.Character, x.CharacterV),
			CreatedAt: x.CreatedAt.Time.UTC(), ExpiresAt: x.ExpiresAt.Time.UTC()})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"notes": out})
}
