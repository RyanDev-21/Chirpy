package calls

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"RyanDev-21.com/Chirpy/internal/database"
	"RyanDev-21.com/Chirpy/internal/platform/httpx"
	"RyanDev-21.com/Chirpy/internal/platform/reqctx"
	"RyanDev-21.com/Chirpy/internal/platform/telemetry"
	"RyanDev-21.com/Chirpy/internal/users"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ICEServer is one entry of RTCConfiguration.iceServers.
type ICEServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

// stunOnly is used without TURN (or when Cloudflare can't be reached):
// most calls still connect, those behind strict NATs don't.
var stunOnly = []ICEServer{{URLs: []string{"stun:stun.cloudflare.com:3478", "stun:stun.l.google.com:19302"}}}

// TURNCredentialTTL: a call longer than this keeps working (the relay
// allocation lives on), but a fresh connection needs new credentials.
const TURNCredentialTTL = 4 * time.Hour

// TURN mints short-lived Cloudflare TURN credentials.
type TURN struct {
	keyID, token string
	url          string
	client       *http.Client
}

// NewTURN returns nil without a key: calls then use STUN only.
func NewTURN(keyID, token string) *TURN {
	if keyID == "" || token == "" {
		return nil
	}
	return &TURN{keyID: keyID, token: token, client: &http.Client{Timeout: 5 * time.Second},
		url: "https://rtc.live.cloudflare.com/v1/turn/keys/" + keyID + "/credentials/generate-ice-servers"}
}

// WithURL points the client at a test server.
func (t *TURN) WithURL(u string) *TURN { t.url = u; return t }

func (t *TURN) ICEServers(ctx context.Context) ([]ICEServer, error) {
	body, _ := json.Marshal(map[string]int{"ttl": int(TURNCredentialTTL.Seconds())})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+t.token)
	req.Header.Set("Content-Type", "application/json")
	res, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("cloudflare turn: %s", res.Status) // the body may echo request data: not logged
	}
	var out struct {
		ICEServers []ICEServer `json:"iceServers"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out.ICEServers) == 0 {
		return nil, errors.New("cloudflare turn: unexpected response")
	}
	// port 53 is blocked in many networks and stalls ICE gathering
	for i := range out.ICEServers {
		urls := out.ICEServers[i].URLs[:0]
		for _, u := range out.ICEServers[i].URLs {
			if !strings.Contains(u, ":53?") && !strings.HasSuffix(u, ":53") {
				urls = append(urls, u)
			}
		}
		out.ICEServers[i].URLs = urls
	}
	return out.ICEServers, nil
}

// LogStore is the call log query side (implemented by *database.Queries).
type LogStore interface {
	ListCalls(ctx context.Context, arg database.ListCallsParams) ([]database.ListCallsRow, error)
	CallStartedAt(ctx context.Context, arg database.CallStartedAtParams) (pgtype.Timestamp, error)
}

type Handler struct {
	turn   *TURN
	log    LogStore
	logger *slog.Logger
}

func NewHandler(turn *TURN, log LogStore, logger *slog.Logger) *Handler {
	return &Handler{turn: turn, log: log, logger: logger}
}

// ICE handles GET /api/calls/ice: the ICE servers for a call.
func (h *Handler) ICE(w http.ResponseWriter, r *http.Request) {
	if _, err := reqctx.User(r.Context()); err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	servers, relay := stunOnly, false
	if h.turn != nil {
		if s, err := h.turn.ICEServers(r.Context()); err != nil {
			h.logger.WarnContext(r.Context(), "TURN credentials unavailable; STUN only", "error", err)
			telemetry.Call(r.Context(), "turn_failed")
		} else {
			servers, relay = s, true
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, http.StatusOK, map[string]any{"ice_servers": servers, "relay": relay,
		"expires_in": int(TURNCredentialTTL.Seconds())})
}

// Peer is the other person in a logged call (null fields if they deleted
// their account).
type Peer struct {
	UserID      *uuid.UUID `json:"user_id"`
	Name        string     `json:"name"`
	DisplayName string     `json:"display_name"`
	AvatarURL   *string    `json:"avatar_url"`
}

// Entry is one call in the log.
type Entry struct {
	ID         uuid.UUID  `json:"id"`
	Direction  string     `json:"direction"` // outgoing or incoming
	State      string     `json:"state"`     // ended, missed, declined, cancelled, busy, failed, ringing, active
	Reason     string     `json:"reason,omitempty"`
	Peer       Peer       `json:"peer"`
	StartedAt  time.Time  `json:"started_at"`
	AnsweredAt *time.Time `json:"answered_at"`
	EndedAt    *time.Time `json:"ended_at"`
	DurationMs int64      `json:"duration_ms"`
}

func optTime(t pgtype.Timestamp) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time.UTC()
	return &v
}

// List handles GET /api/calls?limit=&before={call_id}: my call log.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	me, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	limit := 30
	if v := r.URL.Query().Get("limit"); v != "" {
		if limit, err = strconv.Atoi(v); err != nil || limit < 1 || limit > 100 {
			httpx.Error(w, http.StatusBadRequest, "limit must be 1-100")
			return
		}
	}
	p := database.ListCallsParams{Me: me, Lim: int32(limit)}
	if v := r.URL.Query().Get("before"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			httpx.Error(w, http.StatusBadRequest, "before must be a call id from your log")
			return
		}
		at, err := h.log.CallStartedAt(r.Context(), database.CallStartedAtParams{ID: id, Me: me})
		if errors.Is(err, pgx.ErrNoRows) {
			httpx.Error(w, http.StatusBadRequest, "before must be a call id from your log")
			return
		}
		if err != nil {
			httpx.ServerError(w, r, err)
			return
		}
		p.BeforeAt, p.BeforeID = at, id
	}
	rows, err := h.log.ListCalls(r.Context(), p)
	if err != nil {
		httpx.ServerError(w, r, err)
		return
	}
	out := make([]Entry, 0, len(rows))
	for _, c := range rows {
		e := Entry{ID: c.ID, Direction: "incoming", State: c.State, Reason: c.EndReason.String, StartedAt: c.StartedAt.Time.UTC(),
			AnsweredAt: optTime(c.AnsweredAt), EndedAt: optTime(c.EndedAt)}
		if c.CallerID.Valid && uuid.UUID(c.CallerID.Bytes) == me {
			e.Direction = "outgoing"
		}
		if e.AnsweredAt != nil && e.EndedAt != nil {
			e.DurationMs = e.EndedAt.Sub(*e.AnsweredAt).Milliseconds()
		}
		if c.PeerID.Valid {
			id := uuid.UUID(c.PeerID.Bytes)
			e.Peer = Peer{UserID: &id, Name: c.PeerName.String, DisplayName: users.ShownName(c.PeerName.String, c.PeerDisplayName.String),
				AvatarURL: users.AvatarURLFrom(id, c.PeerAvatarAt)}
		}
		out = append(out, e)
	}
	httpx.JSON(w, http.StatusOK, map[string][]Entry{"calls": out})
}
