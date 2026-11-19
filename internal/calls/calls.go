// Package calls runs 1:1 voice calls. Audio goes phone to phone over
// WebRTC (or through a TURN relay when a direct path fails); the server
// only checks who may call whom, rings the callee, relays the WebRTC
// signalling between the two devices, and keeps a call log.
//
// Call state lives in memory, so calls only work with one server (like the
// websocket hub). Calls open when the server restarts are marked
// "interrupted".
package calls

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"RyanDev-21.com/Chirpy/internal/database"
	"RyanDev-21.com/Chirpy/internal/platform/telemetry"
	"RyanDev-21.com/Chirpy/internal/push"
	"RyanDev-21.com/Chirpy/internal/ws"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// Events the app sends.
const (
	EvStart   = "CallStart"   // {"to_id"}
	EvAccept  = "CallAccept"  // {"call_id"}
	EvSignal  = "CallSignal"  // {"call_id","device"?,"data":{...}}: SDP offer/answer or ICE candidate (relay-only while ringing)
	EvDecline = "CallDecline" // {"call_id"}
	EvEnd     = "CallEnd"     // {"call_id"}
)

// Events the server sends.
const (
	EvRinging  = "CallRinging"    // to the caller's device: {"call_id","to_id"}
	EvIncoming = "CallIncoming"   // to all the callee's devices: {"call_id","from_id"}
	EvAccepted = "CallAccepted"   // to the caller's device: {"call_id","device"?}
	EvDevice   = "CallDevice"     // to the caller's device while ringing: {"call_id","device"}: a callee device is ringing
	EvGone     = "CallDeviceGone" // to the caller's device while ringing: {"call_id","device"}: it stopped ringing
	EvEnded    = "CallEnded"      // to both: {"call_id","reason","duration_ms"}
	EvError    = "CallError"      // to the device that sent something refused: {"call_id"?,"error"}
	EvResumed  = "CallResumed"    // to a device that reconnected mid-call: {"call_id","peer_id"}; restart ICE
)

// End reasons (CallEnded.reason) and call log states.
const (
	ReasonEnded        = "ended"              // hung up after talking
	ReasonMissed       = "missed"             // nobody answered in time
	ReasonDeclined     = "declined"           // the callee said no
	ReasonCancelled    = "cancelled"          // the caller gave up while ringing
	ReasonBusy         = "busy"               // the callee is in another call
	ReasonDisconnected = "disconnected"       // a phone lost its connection for too long
	ReasonElsewhere    = "answered_elsewhere" // to the callee's other devices
	ReasonUnfriended   = "unfriended"         // the friendship ended mid-call
	ReasonLimit        = "daily_limit"        // one of you used up today's call minutes
)

// Errors (CallError.error).
const (
	ErrNotFriends  = "not_friends"
	ErrInCall      = "already_in_a_call"
	ErrRateLimited = "rate_limited"
	ErrUnknownCall = "unknown_call"
	ErrBadRequest  = "bad_request"
	ErrUnavailable = "unavailable"
	ErrDailyLimit  = "daily_limit" // you used up your call minutes for today
)

const (
	RingTimeout     = 45 * time.Second
	ReconnectGrace  = 30 * time.Second // an active call survives a short drop
	MaxDuration     = 4 * time.Hour
	maxSignalBytes  = 16 << 10
	maxSignals      = 500 // per side per connection
	maxDevices      = 5   // ringing callee devices that may set up early
	startsPerMinute = 10
)

// Hub is implemented by *ws.Hub.
type Hub interface {
	SendToConn(c ws.Conn, ev ws.Event) bool
	WriteToUsers(targets []uuid.UUID, ev ws.Event) error
	WriteToUserExcept(user, except uuid.UUID, ev ws.Event)
	Online(user uuid.UUID) bool
	Conns(user uuid.UUID) []uuid.UUID
}

// Friends is implemented by friends.Service.
type Friends interface {
	AreFriends(ctx context.Context, a, b uuid.UUID) (bool, error)
}

// Store is the call log (implemented by *database.Queries).
type Store interface {
	InsertCall(ctx context.Context, arg database.InsertCallParams) error
	AnswerCall(ctx context.Context, arg database.AnswerCallParams) error
	FinishCall(ctx context.Context, arg database.FinishCallParams) error
	InterruptOpenCalls(ctx context.Context, at pgtype.Timestamp) (int64, error)
	CallSecondsSince(ctx context.Context, arg database.CallSecondsSinceParams) (int64, error)
}

type call struct {
	id             uuid.UUID
	caller, callee uuid.UUID
	callerConn     uuid.UUID // the device that called
	calleeConn     uuid.UUID // the device that answered (zero while ringing)
	active         bool
	started        time.Time
	answered       time.Time
	timer          *time.Timer // ring timeout, then the maximum length
	lost           map[uuid.UUID]*time.Timer
	signals        map[string]int // per sender and connection
	// while ringing: the callee's devices that may set up a connection
	// early (opaque ids for this call only), and which one answered
	devices        map[string]uuid.UUID // device id -> connection
	deviceOf       map[uuid.UUID]string // connection -> device id
	answeredDevice string
}

// addDevice gives a ringing callee connection a device id (under mu); ""
// when there are already maxDevices.
func (c *call) addDevice(conn uuid.UUID) string {
	if d, ok := c.deviceOf[conn]; ok || len(c.devices) >= maxDevices {
		return d
	}
	d := uuid.NewString()[:8] + uuid.NewString()[:8]
	c.devices[d], c.deviceOf[conn] = conn, d
	return d
}

func (c *call) peer(u uuid.UUID) uuid.UUID {
	if u == c.caller {
		return c.callee
	}
	return c.caller
}

func (c *call) conn(u uuid.UUID) uuid.UUID {
	if u == c.caller {
		return c.callerConn
	}
	return c.calleeConn
}

type Manager struct {
	hub     Hub
	friends Friends
	db      Store
	notify  push.Notifier // nil: no push
	logger  *slog.Logger
	now     func() time.Time
	ring    time.Duration
	grace   time.Duration
	// daily: answered call time a user may have in 24 h (0 = no cap)
	daily time.Duration

	mu     sync.Mutex
	calls  map[uuid.UUID]*call
	byUser map[uuid.UUID]*call
	starts map[uuid.UUID][]time.Time
}

func NewManager(hub Hub, friends Friends, db Store, logger *slog.Logger) *Manager {
	return &Manager{hub: hub, friends: friends, db: db, logger: logger, now: time.Now,
		ring: RingTimeout, grace: ReconnectGrace,
		calls: map[uuid.UUID]*call{}, byUser: map[uuid.UUID]*call{}, starts: map[uuid.UUID][]time.Time{}}
}

// SetDailyLimit caps each user's answered call time per 24 h (0: none),
// so the free TURN relay allowance can't be used up.
func (m *Manager) SetDailyLimit(d time.Duration) { m.daily = d }

// remaining is how much call time user has left today (MaxDuration when
// there's no cap).
func (m *Manager) remaining(ctx context.Context, user uuid.UUID, now time.Time) (time.Duration, error) {
	if m.daily <= 0 {
		return MaxDuration, nil
	}
	used, err := m.db.CallSecondsSince(ctx, database.CallSecondsSinceParams{UserID: user, Since: ts(now.Add(-24 * time.Hour)), Now: ts(now)})
	if err != nil {
		return 0, err
	}
	return min(m.daily-time.Duration(used)*time.Second, MaxDuration), nil
}

// SetNotifier enables ringing and missed-call pushes.
func (m *Manager) SetNotifier(n push.Notifier) { m.notify = n }

func ts(t time.Time) pgtype.Timestamp { return pgtype.Timestamp{Time: t.UTC(), Valid: true} }

// Recover closes calls a previous run left open; call it at startup.
func (m *Manager) Recover(ctx context.Context) {
	if n, err := m.db.InterruptOpenCalls(ctx, ts(m.now())); err != nil {
		m.logger.WarnContext(ctx, "failed to close interrupted calls", "error", err)
	} else if n > 0 {
		m.logger.InfoContext(ctx, "closed calls interrupted by a restart", "count", n)
	}
}

type idPayload struct {
	ToID   uuid.UUID       `json:"to_id"`
	CallID uuid.UUID       `json:"call_id"`
	Device string          `json:"device"`
	Data   json.RawMessage `json:"data"`
}

func (m *Manager) fail(to ws.Conn, callID uuid.UUID, code string) {
	p := map[string]string{"error": code}
	if callID != uuid.Nil {
		p["call_id"] = callID.String()
	}
	m.hub.SendToConn(to, ws.Event{Event: EvError, Payload: p})
}

// HandleCall is the hub's entry point for Call* events.
func (m *Manager) HandleCall(ctx context.Context, from ws.Conn, event string, raw json.RawMessage) {
	var p idPayload
	if len(raw) > maxSignalBytes+512 || json.Unmarshal(raw, &p) != nil {
		m.fail(from, uuid.Nil, ErrBadRequest)
		return
	}
	switch event {
	case EvStart:
		m.start(ctx, from, p.ToID)
	case EvAccept:
		m.accept(ctx, from, p.CallID)
	case EvSignal:
		m.signal(from, p.CallID, p.Device, p.Data)
	case EvDecline, EvEnd:
		m.hangUp(ctx, from, p.CallID)
	default:
		m.fail(from, uuid.Nil, ErrBadRequest)
	}
}

// allowStart is a per-user limit on starting calls (under mu).
func (m *Manager) allowStart(u uuid.UUID, now time.Time) bool {
	recent := m.starts[u][:0]
	for _, t := range m.starts[u] {
		if now.Sub(t) < time.Minute {
			recent = append(recent, t)
		}
	}
	if len(recent) >= startsPerMinute {
		m.starts[u] = recent
		return false
	}
	m.starts[u] = append(recent, now)
	if len(m.starts) > 100_000 { // bound memory
		clear(m.starts)
	}
	return true
}

func (m *Manager) start(ctx context.Context, from ws.Conn, to uuid.UUID) {
	if to == uuid.Nil || to == from.User {
		m.fail(from, uuid.Nil, ErrBadRequest)
		return
	}
	ok, err := m.friends.AreFriends(ctx, from.User, to)
	if err != nil {
		m.fail(from, uuid.Nil, ErrUnavailable)
		return
	}
	if !ok { // blocking ends a friendship, so blocks are covered too
		m.fail(from, uuid.Nil, ErrNotFriends)
		return
	}
	now := m.now()
	id, _ := uuid.NewV7()
	if left, err := m.remaining(ctx, from.User, now); err != nil {
		m.fail(from, uuid.Nil, ErrUnavailable)
		return
	} else if left <= 0 {
		m.fail(from, uuid.Nil, ErrDailyLimit)
		return
	}

	m.mu.Lock()
	if !m.allowStart(from.User, now) {
		m.mu.Unlock()
		m.fail(from, uuid.Nil, ErrRateLimited)
		return
	}
	if m.byUser[from.User] != nil {
		m.mu.Unlock()
		m.fail(from, uuid.Nil, ErrInCall)
		return
	}
	if m.byUser[to] != nil {
		m.mu.Unlock()
		m.record(ctx, database.InsertCallParams{ID: id, CallerID: pgUUID(from.User), CalleeID: pgUUID(to), State: ReasonBusy,
			StartedAt: ts(now), EndedAt: ts(now), EndReason: pgtype.Text{String: ReasonBusy, Valid: true}})
		m.hub.SendToConn(from, ws.Event{Event: EvEnded, Payload: endedPayload(id, ReasonBusy, 0)})
		telemetry.Call(ctx, ReasonBusy)
		return
	}
	c := &call{id: id, caller: from.User, callee: to, callerConn: from.ID, started: now,
		lost: map[uuid.UUID]*time.Timer{}, signals: map[string]int{},
		devices: map[string]uuid.UUID{}, deviceOf: map[uuid.UUID]string{}}
	c.timer = time.AfterFunc(m.ring, func() { m.end(context.Background(), id, ReasonMissed) })
	var ringing []string
	for _, conn := range m.hub.Conns(to) {
		if d := c.addDevice(conn); d != "" {
			ringing = append(ringing, d)
		}
	}
	m.calls[id], m.byUser[from.User], m.byUser[to] = c, c, c
	m.mu.Unlock()

	m.record(ctx, database.InsertCallParams{ID: id, CallerID: pgUUID(from.User), CalleeID: pgUUID(to), State: "ringing", StartedAt: ts(now)})
	telemetry.Call(ctx, "started")
	m.hub.SendToConn(from, ws.Event{Event: EvRinging, Payload: map[string]string{"call_id": id.String(), "to_id": to.String()}})
	for _, d := range ringing {
		m.hub.SendToConn(from, deviceEvent(EvDevice, id, d))
	}
	_ = m.hub.WriteToUsers([]uuid.UUID{to}, ws.Event{Event: EvIncoming, Payload: incoming(c)})
	if !m.hub.Online(to) && m.notify != nil {
		m.notify.Notify(ctx, push.Job{Kind: push.KindCall, To: []uuid.UUID{to}, From: from.User, CallID: id})
	}
}

func deviceEvent(ev string, id uuid.UUID, device string) ws.Event {
	return ws.Event{Event: ev, Payload: map[string]string{"call_id": id.String(), "device": device}}
}

func incoming(c *call) map[string]string {
	return map[string]string{"call_id": c.id.String(), "from_id": c.caller.String()}
}

func endedPayload(id uuid.UUID, reason string, d time.Duration) map[string]any {
	return map[string]any{"call_id": id.String(), "reason": reason, "duration_ms": d.Milliseconds()}
}

func pgUUID(id uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: id != uuid.Nil} }

func (m *Manager) record(ctx context.Context, p database.InsertCallParams) {
	if err := m.db.InsertCall(ctx, p); err != nil {
		m.logger.WarnContext(ctx, "failed to record call", "error", err)
	}
}

func (m *Manager) accept(ctx context.Context, from ws.Conn, id uuid.UUID) {
	now := m.now()
	// the call may last as long as the one with less time left today allows
	m.mu.Lock()
	pending := m.calls[id]
	m.mu.Unlock()
	limit := MaxDuration
	if pending != nil && pending.callee == from.User {
		for _, u := range []uuid.UUID{pending.caller, pending.callee} {
			left, err := m.remaining(ctx, u, now)
			if err != nil {
				m.fail(from, id, ErrUnavailable)
				return
			}
			limit = min(limit, left)
		}
		if limit <= 0 {
			m.end(ctx, id, ReasonLimit)
			return
		}
	}
	m.mu.Lock()
	c := m.calls[id]
	if c == nil || c.callee != from.User || c.active {
		m.mu.Unlock()
		m.fail(from, id, ErrUnknownCall)
		return
	}
	c.active, c.calleeConn, c.answered = true, from.ID, now
	c.answeredDevice = c.deviceOf[from.ID]
	clear(c.devices) // the others' connections are closed; their signals are dropped
	clear(c.deviceOf)
	c.timer.Stop()
	endReason := ReasonEnded
	if limit < MaxDuration {
		endReason = ReasonLimit
	}
	c.timer = time.AfterFunc(limit, func() { m.end(context.Background(), id, endReason) })
	callerConn := ws.Conn{User: c.caller, ID: c.callerConn}
	accepted := map[string]string{"call_id": id.String()}
	if c.answeredDevice != "" {
		accepted["device"] = c.answeredDevice
	}
	m.mu.Unlock()

	if err := m.db.AnswerCall(ctx, database.AnswerCallParams{ID: id, At: ts(now)}); err != nil {
		m.logger.WarnContext(ctx, "failed to record answered call", "error", err)
	}
	telemetry.Call(ctx, "answered")
	m.hub.SendToConn(callerConn, ws.Event{Event: EvAccepted, Payload: accepted})
	m.hub.WriteToUserExcept(from.User, from.ID, ws.Event{Event: EvEnded, Payload: endedPayload(id, ReasonElsewhere, 0)})
}

// signal relays WebRTC signalling. Once a call is answered it goes between
// the caller's device and the one that answered, unchanged. While it rings
// it goes between the caller's device and each ringing callee device (the
// caller names the device), relay candidates only (see relayOnly).
func (m *Manager) signal(from ws.Conn, id uuid.UUID, device string, data json.RawMessage) {
	if len(data) == 0 || len(data) > maxSignalBytes || !json.Valid(data) {
		m.fail(from, id, ErrBadRequest)
		return
	}
	m.mu.Lock()
	c := m.calls[id]
	if c == nil || (from.User != c.caller && from.User != c.callee) {
		m.mu.Unlock()
		m.fail(from, id, ErrUnknownCall)
		return
	}
	var to ws.Conn
	var key, tag string // key: the signal budget; tag: the device the caller hears from
	switch {
	case c.active:
		if c.conn(from.User) != from.ID {
			m.mu.Unlock()
			m.fail(from, id, ErrUnknownCall)
			return
		}
		peer := c.peer(from.User)
		to, key = ws.Conn{User: peer, ID: c.conn(peer)}, from.ID.String()
		if from.User == c.callee {
			tag = c.answeredDevice
		}
	case from.User == c.caller && from.ID == c.callerConn:
		if device == "" { // while ringing the caller names the device
			m.mu.Unlock()
			m.fail(from, id, ErrBadRequest)
			return
		}
		conn, ok := c.devices[device]
		if !ok { // gone already (CallDeviceGone)
			m.mu.Unlock()
			return
		}
		to, key = ws.Conn{User: c.callee, ID: conn}, "caller/"+device
	case from.User == c.callee && c.deviceOf[from.ID] != "":
		tag = c.deviceOf[from.ID]
		to, key = ws.Conn{User: c.caller, ID: c.callerConn}, from.ID.String()
	default:
		m.mu.Unlock()
		m.fail(from, id, ErrUnknownCall)
		return
	}
	early := !c.active
	if c.signals[key]++; c.signals[key] > maxSignals {
		m.mu.Unlock()
		m.fail(from, id, ErrRateLimited)
		return
	}
	m.mu.Unlock()

	if early {
		out, keep, err := relayOnly(data)
		if err != nil {
			m.fail(from, id, ErrBadRequest)
			return
		}
		if !keep { // a host or srflx candidate: it would reveal an address
			return
		}
		data = out
	}
	p := map[string]any{"call_id": id.String(), "from_id": from.User.String(), "data": data}
	if tag != "" {
		p["device"] = tag
	}
	if !m.hub.SendToConn(to, ws.Event{Event: EvSignal, Payload: p}) {
		// that device is reconnecting; it gets CallResumed and restarts ICE
		m.logger.Debug("call signal not delivered", "call_id", id)
	}
}

// hangUp is CallDecline or CallEnd from either side.
func (m *Manager) hangUp(ctx context.Context, from ws.Conn, id uuid.UUID) {
	m.mu.Lock()
	c := m.calls[id]
	var reason string
	switch {
	case c == nil || (from.User != c.caller && from.User != c.callee):
		m.mu.Unlock()
		m.fail(from, id, ErrUnknownCall)
		return
	case c.active:
		reason = ReasonEnded
	case from.User == c.caller:
		reason = ReasonCancelled
	default:
		reason = ReasonDeclined
	}
	m.mu.Unlock()
	m.end(ctx, id, reason)
}

// end finishes a call (once) and tells both sides.
func (m *Manager) end(ctx context.Context, id uuid.UUID, reason string) {
	now := m.now()
	m.mu.Lock()
	c := m.calls[id]
	if c == nil {
		m.mu.Unlock()
		return
	}
	delete(m.calls, id)
	if m.byUser[c.caller] == c {
		delete(m.byUser, c.caller)
	}
	if m.byUser[c.callee] == c {
		delete(m.byUser, c.callee)
	}
	c.timer.Stop()
	for _, t := range c.lost {
		t.Stop()
	}
	wasActive := c.active
	var talked time.Duration
	if wasActive {
		talked = now.Sub(c.answered)
	}
	m.mu.Unlock()

	state := reason
	switch {
	case wasActive:
		state = ReasonEnded // answered: the log says "ended", the reason says why
	case reason == ReasonDisconnected || reason == ReasonUnfriended || reason == ReasonLimit:
		state = "failed"
	}
	if err := m.db.FinishCall(ctx, database.FinishCallParams{ID: id, State: state, EndReason: pgtype.Text{String: reason, Valid: true}, At: ts(now)}); err != nil {
		m.logger.WarnContext(ctx, "failed to record call end", "error", err)
	}
	telemetry.Call(ctx, reason)
	telemetry.CallSeconds(ctx, int64(talked.Seconds()))
	_ = m.hub.WriteToUsers([]uuid.UUID{c.caller, c.callee}, ws.Event{Event: EvEnded, Payload: endedPayload(id, reason, talked)})
	if reason == ReasonMissed && m.notify != nil {
		m.notify.Notify(ctx, push.Job{Kind: push.KindMissedCall, To: []uuid.UUID{c.callee}, From: c.caller, CallID: id})
	}
}

// Connected: a device came online. If its user is being called, it rings
// too; if it rejoins an active call whose device dropped, the call moves
// to it.
func (m *Manager) Connected(conn ws.Conn) {
	m.mu.Lock()
	c := m.byUser[conn.User]
	if c == nil {
		m.mu.Unlock()
		return
	}
	if !c.active {
		ringing := conn.User == c.callee
		var device string
		if ringing {
			device = c.addDevice(conn.ID)
		}
		callerConn := ws.Conn{User: c.caller, ID: c.callerConn}
		m.mu.Unlock()
		if ringing {
			m.hub.SendToConn(conn, ws.Event{Event: EvIncoming, Payload: incoming(c)})
			if device != "" {
				m.hub.SendToConn(callerConn, deviceEvent(EvDevice, c.id, device))
			}
		}
		return
	}
	t, dropped := c.lost[conn.User]
	if !dropped {
		m.mu.Unlock()
		return
	}
	t.Stop()
	delete(c.lost, conn.User)
	if conn.User == c.caller {
		c.callerConn = conn.ID
	} else {
		c.calleeConn = conn.ID
	}
	peer := c.peer(conn.User)
	peerConn := ws.Conn{User: peer, ID: c.conn(peer)}
	m.mu.Unlock()
	m.hub.SendToConn(conn, ws.Event{Event: EvResumed, Payload: map[string]string{"call_id": c.id.String(), "peer_id": peer.String()}})
	m.hub.SendToConn(peerConn, ws.Event{Event: EvResumed, Payload: map[string]string{"call_id": c.id.String(), "peer_id": conn.User.String()}})
}

// Disconnected: a device went away. A caller's device leaving while it
// rings cancels the call; a device in an active call gets ReconnectGrace
// to come back.
func (m *Manager) Disconnected(conn ws.Conn) {
	m.mu.Lock()
	c := m.byUser[conn.User]
	if c != nil && !c.active && conn.User == c.callee {
		// a ringing device went away: the caller closes its connection
		device := c.deviceOf[conn.ID]
		delete(c.devices, device)
		delete(c.deviceOf, conn.ID)
		callerConn := ws.Conn{User: c.caller, ID: c.callerConn}
		m.mu.Unlock()
		if device != "" {
			m.hub.SendToConn(callerConn, deviceEvent(EvGone, c.id, device))
		}
		return
	}
	if c == nil || c.conn(conn.User) != conn.ID {
		m.mu.Unlock()
		return
	}
	if !c.active {
		m.mu.Unlock()
		if conn.User == c.caller {
			m.end(context.Background(), c.id, ReasonCancelled)
		}
		return
	}
	if _, already := c.lost[conn.User]; !already {
		id := c.id
		c.lost[conn.User] = time.AfterFunc(m.grace, func() { m.end(context.Background(), id, ReasonDisconnected) })
	}
	m.mu.Unlock()
}

// EndBetween ends any call between a and b (they stopped being friends).
func (m *Manager) EndBetween(ctx context.Context, a, b uuid.UUID) {
	m.mu.Lock()
	c := m.byUser[a]
	m.mu.Unlock()
	if c != nil && c.peer(a) == b {
		m.end(ctx, c.id, ReasonUnfriended)
	}
}
