// Package stories: a photo shown to the poster's friends for 24 hours.
// Friends see who posted (rings on avatars), the poster sees who viewed,
// and replies are private messages (see chat, story_id). The photo is an
// ordinary media upload that expires with the story.
package stories

import (
	"RyanDev-21.com/Chirpy/internal/characters"
	"RyanDev-21.com/Chirpy/internal/chat"
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"RyanDev-21.com/Chirpy/internal/database"
	"RyanDev-21.com/Chirpy/internal/media"
	"RyanDev-21.com/Chirpy/internal/platform/httpx"
	"RyanDev-21.com/Chirpy/internal/platform/reqctx"
	"RyanDev-21.com/Chirpy/internal/users"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	Lifetime  = 24 * time.Hour
	MaxActive = 30 // live stories per person
)

var (
	ErrTooMany  = errors.New("at most 30 stories at a time")
	ErrNotFound = errors.New("story not found")
)

// Store is implemented by *database.Queries.
type Store interface {
	CreateStory(ctx context.Context, arg database.CreateStoryParams) error
	CountActiveStories(ctx context.Context, arg database.CountActiveStoriesParams) (int32, error)
	MyStories(ctx context.Context, arg database.MyStoriesParams) ([]database.MyStoriesRow, error)
	FriendStories(ctx context.Context, arg database.FriendStoriesParams) ([]database.FriendStoriesRow, error)
	VisibleStoryOwner(ctx context.Context, arg database.VisibleStoryOwnerParams) (uuid.UUID, error)
	AddStoryView(ctx context.Context, arg database.AddStoryViewParams) error
	StoryViewers(ctx context.Context, arg database.StoryViewersParams) ([]database.StoryViewersRow, error)
	StoryOwnedBy(ctx context.Context, arg database.StoryOwnedByParams) (bool, error)
	DeleteStory(ctx context.Context, arg database.DeleteStoryParams) (int64, error)
	MuteStories(ctx context.Context, arg database.MuteStoriesParams) error
	UnmuteStories(ctx context.Context, arg database.UnmuteStoriesParams) error
	StoryMutes(ctx context.Context, userID uuid.UUID) ([]database.StoryMutesRow, error)
}

// Media is implemented by *media.Service.
type Media interface {
	AttachStory(ctx context.Context, owner, id, storyID uuid.UUID, expires time.Time) (media.Attachment, error)
	ForMessages(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID][]media.Attachment, error)
	ExpireMessage(ctx context.Context, id uuid.UUID) error
}

type Handler struct {
	db    Store
	media Media
	now   func() time.Time
}

func NewHandler(db Store, m Media) *Handler {
	return &Handler{db: db, media: m, now: func() time.Time { return time.Now().UTC() }}
}

func ts(t time.Time) pgtype.Timestamp { return pgtype.Timestamp{Time: t.UTC(), Valid: true} }

// Story is one story as listed.
type Story struct {
	ID        uuid.UUID         `json:"id"`
	CreatedAt time.Time         `json:"created_at"`
	ExpiresAt time.Time         `json:"expires_at"`
	Media     *media.Attachment `json:"media"`           // null if the photo is gone already
	Seen      *bool             `json:"seen,omitempty"`  // friends' stories: have I seen it
	Views     *int              `json:"views,omitempty"` // my stories: how many saw it
}

// Person is a friend with live stories.
type Person struct {
	UserID      uuid.UUID `json:"user_id"`
	Name        string    `json:"name"`
	DisplayName string    `json:"display_name"`
	AvatarURL   *string   `json:"avatar_url"`
	AllSeen     bool      `json:"all_seen"`
	Stories     []Story   `json:"stories"`
	users.CharacterInfo
}

// me is the signed-in caller.
func me(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return uuid.Nil, false
	}
	return id, true
}

func (h *Handler) mediaOf(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]*media.Attachment, error) {
	all, err := h.media.ForMessages(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := map[uuid.UUID]*media.Attachment{}
	for id, list := range all {
		if len(list) > 0 && !list[0].Expired {
			a := list[0]
			out[id] = &a
		}
	}
	return out, nil
}

// Create handles POST /api/stories {"media_id"}: a photo uploaded with
// POST /api/media (kind image).
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	uid, ok := me(w, r)
	if !ok {
		return
	}
	var b struct {
		MediaID uuid.UUID `json:"media_id"`
	}
	if err := httpx.Decode(w, r, &b); err != nil || b.MediaID == uuid.Nil {
		httpx.Error(w, http.StatusBadRequest, "media_id is required")
		return
	}
	now := h.now()
	n, err := h.db.CountActiveStories(r.Context(), database.CountActiveStoriesParams{UserID: uid, Now: ts(now)})
	if err != nil {
		httpx.ServerError(w, r, err)
		return
	}
	if n >= MaxActive {
		httpx.Error(w, http.StatusTooManyRequests, ErrTooMany.Error())
		return
	}
	id, _ := uuid.NewV7()
	expires := now.Add(Lifetime)
	// the story row first: the photo's access check looks for it
	if err := h.db.CreateStory(r.Context(), database.CreateStoryParams{ID: id, UserID: uid, Now: ts(now), ExpiresAt: ts(expires)}); err != nil {
		httpx.ServerError(w, r, err)
		return
	}
	a, err := h.media.AttachStory(r.Context(), uid, b.MediaID, id, expires)
	if err != nil {
		_, _ = h.db.DeleteStory(r.Context(), database.DeleteStoryParams{ID: id, Owner: uid})
		switch {
		case errors.Is(err, media.ErrBadAttachment):
			httpx.Error(w, http.StatusBadRequest, "media_id must be your own uploaded photo, not used yet")
		case errors.Is(err, media.ErrDisabled):
			httpx.Error(w, http.StatusServiceUnavailable, "photos are not available")
		default:
			httpx.ServerError(w, r, err)
		}
		return
	}
	zero := 0
	httpx.JSON(w, http.StatusCreated, Story{ID: id, CreatedAt: now, ExpiresAt: expires, Media: &a, Views: &zero})
}

// List handles GET /api/stories: my live stories, and my friends' (people
// with something unseen first, then the most recent).
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	uid, ok := me(w, r)
	if !ok {
		return
	}
	now := ts(h.now())
	mine, err := h.db.MyStories(r.Context(), database.MyStoriesParams{Me: uid, Now: now})
	if err != nil {
		httpx.ServerError(w, r, err)
		return
	}
	friends, err := h.db.FriendStories(r.Context(), database.FriendStoriesParams{Me: uid, Now: now})
	if err != nil {
		httpx.ServerError(w, r, err)
		return
	}
	ids := make([]uuid.UUID, 0, len(mine)+len(friends))
	for _, s := range mine {
		ids = append(ids, s.ID)
	}
	for _, s := range friends {
		ids = append(ids, s.ID)
	}
	photos, err := h.mediaOf(r.Context(), ids)
	if err != nil {
		httpx.ServerError(w, r, err)
		return
	}
	my := make([]Story, 0, len(mine))
	for _, s := range mine {
		views := int(s.Views)
		my = append(my, Story{ID: s.ID, CreatedAt: s.CreatedAt.Time.UTC(), ExpiresAt: s.ExpiresAt.Time.UTC(), Media: photos[s.ID], Views: &views})
	}
	people := []Person{}
	latest := map[uuid.UUID]time.Time{}
	for _, s := range friends {
		if len(people) == 0 || people[len(people)-1].UserID != s.UserID {
			people = append(people, Person{UserID: s.UserID, Name: s.Name, DisplayName: users.ShownName(s.Name, s.DisplayName),
				AvatarURL: users.AvatarURLFrom(s.UserID, s.AvatarAt), AllSeen: true, Stories: []Story{}, CharacterInfo: users.CharacterFrom(s.Character, s.CharacterV)})
		}
		p := &people[len(people)-1]
		seen := s.Seen
		p.Stories = append(p.Stories, Story{ID: s.ID, CreatedAt: s.CreatedAt.Time.UTC(), ExpiresAt: s.ExpiresAt.Time.UTC(), Media: photos[s.ID], Seen: &seen})
		p.AllSeen = p.AllSeen && seen
		latest[s.UserID] = s.CreatedAt.Time
	}
	sortPeople(people, latest)
	httpx.JSON(w, http.StatusOK, map[string]any{"mine": my, "friends": people})
}

// View handles POST /api/stories/{story_id}/view.
func (h *Handler) View(w http.ResponseWriter, r *http.Request) {
	uid, ok := me(w, r)
	if !ok {
		return
	}
	id, err := httpx.PathUUID(r, "story_id")
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid story id")
		return
	}
	now := h.now()
	owner, err := h.db.VisibleStoryOwner(r.Context(), database.VisibleStoryOwnerParams{ID: id, Viewer: uid, Now: ts(now)})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, ErrNotFound.Error())
		return
	}
	if err != nil {
		httpx.ServerError(w, r, err)
		return
	}
	if owner != uid { // your own views aren't counted
		if err := h.db.AddStoryView(r.Context(), database.AddStoryViewParams{StoryID: id, ViewerID: uid, Now: ts(now)}); err != nil {
			httpx.ServerError(w, r, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// Viewers handles GET /api/stories/{story_id}/viewers (the poster only).
func (h *Handler) Viewers(w http.ResponseWriter, r *http.Request) {
	uid, ok := me(w, r)
	if !ok {
		return
	}
	id, err := httpx.PathUUID(r, "story_id")
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid story id")
		return
	}
	mine, err := h.db.StoryOwnedBy(r.Context(), database.StoryOwnedByParams{ID: id, Owner: uid})
	if err != nil {
		httpx.ServerError(w, r, err)
		return
	}
	if !mine {
		httpx.Error(w, http.StatusNotFound, ErrNotFound.Error())
		return
	}
	rows, err := h.db.StoryViewers(r.Context(), database.StoryViewersParams{StoryID: id, Owner: uid})
	if err != nil {
		httpx.ServerError(w, r, err)
		return
	}
	type viewer struct {
		UserID      uuid.UUID `json:"user_id"`
		Name        string    `json:"name"`
		DisplayName string    `json:"display_name"`
		AvatarURL   *string   `json:"avatar_url"`
		ViewedAt    time.Time `json:"viewed_at"`
		Reaction    *Reaction `json:"reaction,omitempty"`
	}
	out := make([]viewer, 0, len(rows))
	for _, v := range rows {
		out = append(out, viewer{UserID: v.ID, Name: v.Name, DisplayName: users.ShownName(v.Name, v.DisplayName),
			AvatarURL: users.AvatarURLFrom(v.ID, v.AvatarAt), ViewedAt: v.ViewedAt.Time.UTC(), Reaction: reactionOf(v.ReplyText, v.ReplyAnim)})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"viewers": out})
}

// Reaction is a viewer's reaction to a story: their latest reply to it,
// when that reply was one emoji or a pigeon animation.
type Reaction struct {
	Emoji string `json:"emoji"`
	Anim  string `json:"anim,omitempty"`
}

func reactionOf(text, anim string) *Reaction {
	if fallback, ok := characters.Fallback(anim); ok && (text == "" || text == fallback) {
		emoji, _, _ := strings.Cut(fallback, " ") // "🤨 side-eyed" -> 🤨
		return &Reaction{Emoji: emoji, Anim: anim}
	}
	if text != "" && chat.ValidateEmoji(text) == nil {
		return &Reaction{Emoji: text}
	}
	return nil // no reply, or a written one
}

// Delete handles DELETE /api/stories/{story_id} (the poster only).
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	uid, ok := me(w, r)
	if !ok {
		return
	}
	id, err := httpx.PathUUID(r, "story_id")
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid story id")
		return
	}
	n, err := h.db.DeleteStory(r.Context(), database.DeleteStoryParams{ID: id, Owner: uid})
	if err != nil {
		httpx.ServerError(w, r, err)
		return
	}
	if n == 0 {
		httpx.Error(w, http.StatusNotFound, ErrNotFound.Error())
		return
	}
	if err := h.media.ExpireMessage(r.Context(), id); err != nil { // the photo goes at the next cleanup
		httpx.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Mute handles PUT /api/stories/mutes/{user_id}; Unmute DELETE; Mutes GET.
func (h *Handler) Mute(w http.ResponseWriter, r *http.Request) {
	uid, ok := me(w, r)
	if !ok {
		return
	}
	other, err := httpx.PathUUID(r, "user_id")
	if err != nil || other == uid {
		httpx.Error(w, http.StatusBadRequest, "invalid user id")
		return
	}
	if err := h.db.MuteStories(r.Context(), database.MuteStoriesParams{UserID: uid, MutedID: other, Now: ts(h.now())}); err != nil {
		httpx.Error(w, http.StatusNotFound, "no user found") // unknown user (foreign key)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) Unmute(w http.ResponseWriter, r *http.Request) {
	uid, ok := me(w, r)
	if !ok {
		return
	}
	other, err := httpx.PathUUID(r, "user_id")
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid user id")
		return
	}
	if err := h.db.UnmuteStories(r.Context(), database.UnmuteStoriesParams{UserID: uid, MutedID: other}); err != nil {
		httpx.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) Mutes(w http.ResponseWriter, r *http.Request) {
	uid, ok := me(w, r)
	if !ok {
		return
	}
	rows, err := h.db.StoryMutes(r.Context(), uid)
	if err != nil {
		httpx.ServerError(w, r, err)
		return
	}
	type muted struct {
		UserID      uuid.UUID `json:"user_id"`
		Name        string    `json:"name"`
		DisplayName string    `json:"display_name"`
		AvatarURL   *string   `json:"avatar_url"`
	}
	out := make([]muted, 0, len(rows))
	for _, m := range rows {
		out = append(out, muted{UserID: m.ID, Name: m.Name, DisplayName: users.ShownName(m.Name, m.DisplayName), AvatarURL: users.AvatarURLFrom(m.ID, m.AvatarAt)})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"muted": out})
}
