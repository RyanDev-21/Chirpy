// Package stories: a photo shown to the poster's friends for 24 hours.
// Friends see who posted (rings on avatars), the poster sees who viewed,
// and replies are private messages (see chat, story_id). The photo is an
// ordinary media upload that expires with the story.
package stories

import (
	"RyanDev-21.com/Chirpy/internal/characters"
	"RyanDev-21.com/Chirpy/internal/chat"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5/pgconn"
	"net/http"
	"slices"
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
	ErrBadStickers = errors.New(`stickers: at most 3 {"type":"pigeon","anim":<a one-pigeon catalog animation>} and 10 {"type":"mention","user_id":<a friend>}, each with "x":0-1,"y":0-1,"size":0.1-0.8,"rotation":-180-180`)
	ErrTooMany     = errors.New("at most 30 stories at a time")
	ErrNotFound    = errors.New("story not found")
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
	FriendsAmong(ctx context.Context, arg database.FriendsAmongParams) ([]uuid.UUID, error)
	AddStoryMention(ctx context.Context, arg database.AddStoryMentionParams) error
	UserNames(ctx context.Context, ids []uuid.UUID) ([]database.UserNamesRow, error)
	RepostSource(ctx context.Context, arg database.RepostSourceParams) (database.RepostSourceRow, error)
}

// Media is implemented by *media.Service.
type Media interface {
	AttachStory(ctx context.Context, owner, id, storyID uuid.UUID, expires time.Time) (media.Attachment, error)
	ForMessages(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID][]media.Attachment, error)
	ExpireMessage(ctx context.Context, id uuid.UUID) error
	Duplicate(ctx context.Context, owner, srcID uuid.UUID) (uuid.UUID, error)
}

type Handler struct {
	db    Store
	media Media
	now   func() time.Time
	// told about mentions (to the people mentioned) and reposts (to the
	// original poster): push and live events, wired by the app
	mentioned func(ctx context.Context, story, from uuid.UUID, to []uuid.UUID)
	reposted  func(ctx context.Context, story, from, to uuid.UUID)
}

// OnMention and OnRepost register who tells people about mentions and
// reposts.
func (h *Handler) OnMention(fn func(ctx context.Context, story, from uuid.UUID, to []uuid.UUID)) {
	h.mentioned = fn
}
func (h *Handler) OnRepost(fn func(ctx context.Context, story, from, to uuid.UUID)) { h.reposted = fn }

func NewHandler(db Store, m Media) *Handler {
	return &Handler{db: db, media: m, now: func() time.Time { return time.Now().UTC() }}
}

func ts(t time.Time) pgtype.Timestamp { return pgtype.Timestamp{Time: t.UTC(), Valid: true} }

// Story is one story as listed.
type Story struct {
	ID        uuid.UUID         `json:"id"`
	CreatedAt time.Time         `json:"created_at"`
	ExpiresAt time.Time         `json:"expires_at"`
	Media     *media.Attachment `json:"media"`          // null if the photo is gone already
	Seen      *bool             `json:"seen,omitempty"` // friends' stories: have I seen it
	Stickers  json.RawMessage   `json:"stickers,omitempty"`
	// RepostOf: this story is someone's story shared by a friend they
	// mentioned ("from @maya").
	RepostOf *Origin `json:"repost_of,omitempty"`
	Views    *int    `json:"views,omitempty"` // my stories: how many saw it
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
		MediaID  uuid.UUID `json:"media_id"`
		Stickers []Sticker `json:"stickers"`
	}
	if err := httpx.Decode(w, r, &b); err != nil || b.MediaID == uuid.Nil {
		httpx.Error(w, http.StatusBadRequest, "media_id is required")
		return
	}
	stickers, mentions, err := stickersJSON(b.Stickers)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(mentions) > 0 { // friends only (not yourself)
		friends, err := h.db.FriendsAmong(r.Context(), database.FriendsAmongParams{Me: uid, Ids: mentions})
		if err != nil {
			httpx.ServerError(w, r, err)
			return
		}
		for _, m := range mentions {
			if !slices.Contains(friends, m) {
				httpx.Error(w, http.StatusBadRequest, ErrBadStickers.Error())
				return
			}
		}
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
	if err := h.db.CreateStory(r.Context(), database.CreateStoryParams{ID: id, UserID: uid, Now: ts(now), ExpiresAt: ts(expires), Stickers: stickers}); err != nil {
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
	for _, m := range mentions {
		if err := h.db.AddStoryMention(r.Context(), database.AddStoryMentionParams{StoryID: id, UserID: m}); err != nil {
			httpx.ServerError(w, r, err)
			return
		}
	}
	if len(mentions) > 0 && h.mentioned != nil {
		h.mentioned(r.Context(), id, uid, mentions)
	}
	zero := 0
	httpx.JSON(w, http.StatusCreated, Story{ID: id, CreatedAt: now, ExpiresAt: expires, Media: &a, Views: &zero, Stickers: stickers})
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
		my = append(my, Story{ID: s.ID, CreatedAt: s.CreatedAt.Time.UTC(), ExpiresAt: s.ExpiresAt.Time.UTC(), Media: photos[s.ID], Views: &views, Stickers: s.Stickers,
			RepostOf: origin(s.RepostOf, s.OrigUserID, s.OrigName, s.OrigDisplay)})
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
		p.Stories = append(p.Stories, Story{ID: s.ID, CreatedAt: s.CreatedAt.Time.UTC(), ExpiresAt: s.ExpiresAt.Time.UTC(), Media: photos[s.ID], Seen: &seen, Stickers: s.Stickers,
			RepostOf: origin(s.RepostOf, s.OrigUserID, s.OrigName, s.OrigDisplay)})
		p.AllSeen = p.AllSeen && seen
		latest[s.UserID] = s.CreatedAt.Time
	}
	sortPeople(people, latest)
	if err := h.nameMentions(r.Context(), my, people); err != nil {
		httpx.ServerError(w, r, err)
		return
	}
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

// MaxStickers is how many pigeon stickers one story may carry, and
// MaxMentions how many friends it may mention.
const (
	MaxStickers = 3
	MaxMentions = 10
)

// Sticker is something drawn over a story's photo, kept as data (not
// flattened into the JPEG). Positions are fractions of the 9:16 photo.
//   - "pigeon": the poster's pigeon playing a one-pigeon catalog animation;
//   - "mention": a friend (user_id); they're told, and may repost it.
type Sticker struct {
	Type     string     `json:"type"`
	Anim     string     `json:"anim,omitempty"`
	UserID   *uuid.UUID `json:"user_id,omitempty"`
	X        float64    `json:"x"`
	Y        float64    `json:"y"`
	Size     float64    `json:"size"`
	Rotation float64    `json:"rotation"`
	// Name and DisplayName are filled in on read, for mentions.
	Name        string `json:"name,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
}

// stickersJSON checks the stickers and returns them as stored (nil: none)
// and the friends mentioned (each once).
func stickersJSON(list []Sticker) ([]byte, []uuid.UUID, error) {
	if len(list) == 0 {
		return nil, nil, nil
	}
	pigeons := 0
	var mentions []uuid.UUID
	for i := range list {
		s := &list[i]
		s.Name, s.DisplayName = "", ""
		if s.X < 0 || s.X > 1 || s.Y < 0 || s.Y > 1 || s.Size < 0.1 || s.Size > 0.8 || s.Rotation < -180 || s.Rotation > 180 {
			return nil, nil, ErrBadStickers
		}
		switch s.Type {
		case "pigeon":
			if !characters.Solo(s.Anim) || s.UserID != nil {
				return nil, nil, ErrBadStickers
			}
			pigeons++
		case "mention":
			if s.UserID == nil || *s.UserID == uuid.Nil || s.Anim != "" {
				return nil, nil, ErrBadStickers
			}
			if !slices.Contains(mentions, *s.UserID) {
				mentions = append(mentions, *s.UserID)
			}
		default:
			return nil, nil, ErrBadStickers
		}
	}
	if pigeons > MaxStickers || len(mentions) > MaxMentions {
		return nil, nil, ErrBadStickers
	}
	b, err := json.Marshal(list)
	return b, mentions, err
}

// Origin is the story a repost shares, and who posted it.
type Origin struct {
	StoryID     uuid.UUID `json:"story_id"`
	UserID      uuid.UUID `json:"user_id"`
	Name        string    `json:"name"`
	DisplayName string    `json:"display_name"`
}

func origin(story, user pgtype.UUID, name, display string) *Origin {
	if !story.Valid || !user.Valid {
		return nil
	}
	return &Origin{StoryID: story.Bytes, UserID: user.Bytes, Name: name, DisplayName: users.ShownName(name, display)}
}

// nameMentions fills in the names of the people mention stickers point at
// (they may not be the viewer's friends).
func (h *Handler) nameMentions(ctx context.Context, mine []Story, people []Person) error {
	var all []*Story
	for i := range mine {
		all = append(all, &mine[i])
	}
	for p := range people {
		for i := range people[p].Stories {
			all = append(all, &people[p].Stories[i])
		}
	}
	var ids []uuid.UUID
	parsed := map[*Story][]Sticker{}
	for _, st := range all {
		if len(st.Stickers) == 0 || !bytes.Contains(st.Stickers, []byte(`"mention"`)) {
			continue
		}
		var list []Sticker
		if json.Unmarshal(st.Stickers, &list) != nil {
			continue
		}
		parsed[st] = list
		for _, s := range list {
			if s.UserID != nil && !slices.Contains(ids, *s.UserID) {
				ids = append(ids, *s.UserID)
			}
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := h.db.UserNames(ctx, ids)
	if err != nil {
		return err
	}
	names := map[uuid.UUID]database.UserNamesRow{}
	for _, r := range rows {
		names[r.ID] = r
	}
	for st, list := range parsed {
		for i := range list {
			if list[i].UserID == nil {
				continue
			}
			if n, ok := names[*list[i].UserID]; ok {
				list[i].Name, list[i].DisplayName = n.Name, users.ShownName(n.Name, n.DisplayName)
			}
		}
		if b, err := json.Marshal(list); err == nil {
			st.Stickers = b
		}
	}
	return nil
}

// Repost handles POST /api/stories/{story_id}/repost: someone mentioned in
// a live story shares it to their own (once). It gets its own copy of the
// photo, shows where it's from, and ends with the original.
func (h *Handler) Repost(w http.ResponseWriter, r *http.Request) {
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
	src, err := h.db.RepostSource(r.Context(), database.RepostSourceParams{ID: id, Me: uid, Now: ts(now)})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "only a live story that mentions you can be reposted")
		return
	}
	if err != nil {
		httpx.ServerError(w, r, err)
		return
	}
	n, err := h.db.CountActiveStories(r.Context(), database.CountActiveStoriesParams{UserID: uid, Now: ts(now)})
	if err != nil {
		httpx.ServerError(w, r, err)
		return
	}
	if n >= MaxActive {
		httpx.Error(w, http.StatusTooManyRequests, ErrTooMany.Error())
		return
	}
	copyID, err := h.media.Duplicate(r.Context(), uid, src.MediaID)
	if err != nil {
		switch {
		case errors.Is(err, media.ErrDailyQuota):
			httpx.Error(w, http.StatusTooManyRequests, err.Error())
		case errors.Is(err, media.ErrStorageFull):
			httpx.Error(w, http.StatusInsufficientStorage, err.Error())
		case errors.Is(err, media.ErrForwardExpired), errors.Is(err, media.ErrNotFound):
			httpx.Error(w, http.StatusNotFound, "the photo is gone")
		case errors.Is(err, media.ErrDisabled):
			httpx.Error(w, http.StatusServiceUnavailable, "photos are not available")
		default:
			httpx.ServerError(w, r, err)
		}
		return
	}
	newID, _ := uuid.NewV7()
	expires := src.ExpiresAt.Time.UTC() // ends with the original
	err = h.db.CreateStory(r.Context(), database.CreateStoryParams{ID: newID, UserID: uid, Now: ts(now), ExpiresAt: ts(expires),
		RepostOf: pgtype.UUID{Bytes: id, Valid: true}})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		httpx.Error(w, http.StatusConflict, "you already reposted this story")
		return
	}
	if err != nil {
		httpx.ServerError(w, r, err)
		return
	}
	a, err := h.media.AttachStory(r.Context(), uid, copyID, newID, expires)
	if err != nil {
		_, _ = h.db.DeleteStory(r.Context(), database.DeleteStoryParams{ID: newID, Owner: uid})
		httpx.ServerError(w, r, err)
		return
	}
	if h.reposted != nil {
		h.reposted(r.Context(), id, uid, src.UserID)
	}
	zero := 0
	names, _ := h.db.UserNames(r.Context(), []uuid.UUID{src.UserID})
	o := &Origin{StoryID: id, UserID: src.UserID}
	if len(names) == 1 {
		o.Name, o.DisplayName = names[0].Name, users.ShownName(names[0].Name, names[0].DisplayName)
	}
	httpx.JSON(w, http.StatusCreated, Story{ID: newID, CreatedAt: now, ExpiresAt: expires, Media: &a, Views: &zero, RepostOf: o})
}
