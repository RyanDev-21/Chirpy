// Package presence tells friends when someone is online and when they were
// last seen. Only friends see it (blocking unfriends, so blocks are
// respected), and it is reciprocal: a user who hides their last seen sees
// nobody else's either.
//
// "Online" means a live websocket on this server (one server for now; with
// several, the hub's view would have to be shared first).
package presence

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"RyanDev-21.com/Chirpy/internal/database"
	"RyanDev-21.com/Chirpy/internal/friends"
	"RyanDev-21.com/Chirpy/internal/platform/httpx"
	"RyanDev-21.com/Chirpy/internal/platform/reqctx"
	"RyanDev-21.com/Chirpy/internal/ws"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// OfflineGrace: a phone that drops and reconnects within this time (a
// network switch, the app restarting) isn't shown as going offline.
const OfflineGrace = 5 * time.Second

// Friends is implemented by friends.Service.
type Friends interface {
	Relationships(ctx context.Context, me uuid.UUID) (friends.Snapshot, error)
}

// Hub is implemented by *ws.Hub.
type Hub interface {
	Online(userID uuid.UUID) bool
	WriteToUsers(targets []uuid.UUID, ev ws.Event) error
}

// Store is the database side (implemented by *database.Queries).
type Store interface {
	PresenceOf(ctx context.Context, ids []uuid.UUID) ([]database.PresenceOfRow, error)
	SetHideLastSeen(ctx context.Context, arg database.SetHideLastSeenParams) error
	TouchLastSeen(ctx context.Context, arg database.TouchLastSeenParams) error
}

type Service struct {
	db      Store
	friends Friends
	hub     Hub
	logger  *slog.Logger
	grace   time.Duration

	mu      sync.Mutex
	pending map[uuid.UUID]*time.Timer // offline announcements waiting out the grace
}

func NewService(db Store, fr Friends, hub Hub, logger *slog.Logger) *Service {
	return &Service{db: db, friends: fr, hub: hub, logger: logger, grace: OfflineGrace, pending: map[uuid.UUID]*time.Timer{}}
}

// Entry is one friend's presence.
type Entry struct {
	UserID     uuid.UUID  `json:"user_id"`
	Online     bool       `json:"online"`
	LastSeenAt *time.Time `json:"last_seen_at"`
	// Hidden: they hide their last seen, or you hide yours.
	Hidden bool `json:"hidden"`
}

// Changed is the hub's hook: userID's first device connected (online) or
// last one went (offline).
func (s *Service) Changed(userID uuid.UUID, online bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	now := time.Now().UTC()
	if err := s.db.TouchLastSeen(ctx, database.TouchLastSeenParams{ID: userID, Now: pgtype.Timestamp{Time: now, Valid: true}}); err != nil {
		s.logger.WarnContext(ctx, "failed to update last seen", "error", err)
	}

	s.mu.Lock()
	t, waiting := s.pending[userID]
	if online {
		if waiting { // back within the grace: friends never saw them leave
			t.Stop()
			delete(s.pending, userID)
		}
		s.mu.Unlock()
		if !waiting {
			s.announce(userID)
		}
		return
	}
	if !waiting {
		s.pending[userID] = time.AfterFunc(s.grace, func() {
			s.mu.Lock()
			delete(s.pending, userID)
			s.mu.Unlock()
			s.announce(userID)
		})
	}
	s.mu.Unlock()
}

// announce sends userID's current state to their online friends who may
// see it.
func (s *Service) announce(userID uuid.UUID) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.broadcast(ctx, userID); err != nil {
		s.logger.WarnContext(ctx, "failed to announce presence", "error", err)
	}
}

func (s *Service) broadcast(ctx context.Context, userID uuid.UUID) error {
	snap, err := s.friends.Relationships(ctx, userID)
	if err != nil {
		return err
	}
	var online []uuid.UUID
	for _, f := range snap.Friends {
		if s.hub.Online(f.UserID) {
			online = append(online, f.UserID)
		}
	}
	if len(online) == 0 {
		return nil
	}
	rows, err := s.db.PresenceOf(ctx, append(online, userID))
	if err != nil {
		return err
	}
	var me *database.PresenceOfRow
	hides := map[uuid.UUID]bool{}
	for i := range rows {
		if rows[i].ID == userID {
			me = &rows[i]
		} else {
			hides[rows[i].ID] = rows[i].HideLastSeen
		}
	}
	if me == nil { // account gone
		return nil
	}
	ev := ws.OutPresenceEvent{UserID: userID.String(), Hidden: me.HideLastSeen}
	if !me.HideLastSeen {
		ev.Online = s.hub.Online(userID)
		ev.LastSeenAt = lastSeen(me.LastSeenAt, ev.Online)
	}
	var to []uuid.UUID
	for _, id := range online {
		if !hides[id] { // they hide theirs, so they don't see mine
			to = append(to, id)
		}
	}
	if len(to) == 0 {
		return nil
	}
	return s.hub.WriteToUsers(to, ws.Event{Event: ws.EventPresence, Payload: ev})
}

func lastSeen(t pgtype.Timestamp, online bool) *time.Time {
	if online || !t.Valid {
		return nil
	}
	v := t.Time.UTC()
	return &v
}

// Friends returns the presence of each of me's friends, and whether me
// hides theirs (then every entry is hidden).
func (s *Service) Friends(ctx context.Context, me uuid.UUID) (bool, []Entry, error) {
	snap, err := s.friends.Relationships(ctx, me)
	if err != nil {
		return false, nil, err
	}
	ids := []uuid.UUID{me}
	for _, f := range snap.Friends {
		ids = append(ids, f.UserID)
	}
	rows, err := s.db.PresenceOf(ctx, ids)
	if err != nil {
		return false, nil, err
	}
	byID := map[uuid.UUID]database.PresenceOfRow{}
	for _, r := range rows {
		byID[r.ID] = r
	}
	hideMine := byID[me].HideLastSeen
	out := make([]Entry, 0, len(snap.Friends))
	for _, f := range snap.Friends {
		r, ok := byID[f.UserID]
		if !ok {
			continue
		}
		e := Entry{UserID: f.UserID, Hidden: hideMine || r.HideLastSeen}
		if !e.Hidden {
			e.Online = s.hub.Online(f.UserID)
			e.LastSeenAt = lastSeen(r.LastSeenAt, e.Online)
		}
		out = append(out, e)
	}
	return hideMine, out, nil
}

// SetHidden turns "hide my last seen" on or off and tells online friends.
func (s *Service) SetHidden(ctx context.Context, me uuid.UUID, hide bool) error {
	if err := s.db.SetHideLastSeen(ctx, database.SetHideLastSeenParams{ID: me, Hide: hide}); err != nil {
		return err
	}
	if err := s.broadcast(ctx, me); err != nil {
		s.logger.WarnContext(ctx, "failed to announce presence", "error", err)
	}
	return nil
}

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Friends handles GET /api/friends/presence.
func (h *Handler) Friends(w http.ResponseWriter, r *http.Request) {
	me, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	hidden, list, err := h.svc.Friends(r.Context(), me)
	if err != nil {
		httpx.ServerError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"hide_last_seen": hidden, "friends": list})
}

// SetPrivacy handles PUT /api/users/me/privacy {"hide_last_seen": bool}.
func (h *Handler) SetPrivacy(w http.ResponseWriter, r *http.Request) {
	me, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var b struct {
		Hide *bool `json:"hide_last_seen"`
	}
	if err := httpx.Decode(w, r, &b); err != nil || b.Hide == nil {
		httpx.Error(w, http.StatusBadRequest, "hide_last_seen must be true or false")
		return
	}
	if err := h.svc.SetHidden(r.Context(), me, *b.Hide); err != nil {
		httpx.ServerError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"hide_last_seen": *b.Hide})
}
