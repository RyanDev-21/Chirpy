package calls

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"RyanDev-21.com/Chirpy/internal/database"
	"RyanDev-21.com/Chirpy/internal/push"
	"RyanDev-21.com/Chirpy/internal/ws"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type got struct {
	to ws.Conn
	ev ws.Event
}

// fakeHub: users have devices; events land in one channel, tagged with the
// device they reached.
type fakeHub struct {
	mu      sync.Mutex
	devices map[uuid.UUID][]uuid.UUID
	out     chan got
}

func (h *fakeHub) SendToConn(c ws.Conn, ev ws.Event) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, d := range h.devices[c.User] {
		if d == c.ID {
			h.out <- got{c, ev}
			return true
		}
	}
	return false
}
func (h *fakeHub) WriteToUsers(us []uuid.UUID, ev ws.Event) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, u := range us {
		for _, d := range h.devices[u] {
			h.out <- got{ws.Conn{User: u, ID: d}, ev}
		}
	}
	return nil
}
func (h *fakeHub) WriteToUserExcept(u, except uuid.UUID, ev ws.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, d := range h.devices[u] {
		if d != except {
			h.out <- got{ws.Conn{User: u, ID: d}, ev}
		}
	}
}
func (h *fakeHub) Conns(u uuid.UUID) []uuid.UUID {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]uuid.UUID(nil), h.devices[u]...)
}
func (h *fakeHub) Online(u uuid.UUID) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.devices[u]) > 0
}
func (h *fakeHub) connect(u uuid.UUID) ws.Conn {
	h.mu.Lock()
	defer h.mu.Unlock()
	c := ws.Conn{User: u, ID: uuid.New()}
	h.devices[u] = append(h.devices[u], c.ID)
	return c
}
func (h *fakeHub) drop(c ws.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	list := h.devices[c.User][:0]
	for _, d := range h.devices[c.User] {
		if d != c.ID {
			list = append(list, d)
		}
	}
	h.devices[c.User] = list
}

type fakeFriends map[[2]uuid.UUID]bool

func (f fakeFriends) AreFriends(_ context.Context, a, b uuid.UUID) (bool, error) {
	return f[[2]uuid.UUID{a, b}] || f[[2]uuid.UUID{b, a}], nil
}

type fakeDB struct {
	mu     sync.Mutex
	states map[uuid.UUID]string
	used   map[uuid.UUID]int64 // call seconds today
}

func (d *fakeDB) InsertCall(_ context.Context, p database.InsertCallParams) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.states[p.ID] = p.State
	return nil
}
func (d *fakeDB) AnswerCall(_ context.Context, p database.AnswerCallParams) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.states[p.ID] = "active"
	return nil
}
func (d *fakeDB) FinishCall(_ context.Context, p database.FinishCallParams) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.states[p.ID] = p.State
	return nil
}
func (d *fakeDB) InterruptOpenCalls(context.Context, pgtype.Timestamp) (int64, error) { return 0, nil }
func (d *fakeDB) CallSecondsSince(_ context.Context, p database.CallSecondsSinceParams) (int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.used[p.UserID], nil
}
func (d *fakeDB) state(id uuid.UUID) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.states[id]
}

type fakePush struct {
	mu   sync.Mutex
	jobs []push.Job
}

func (p *fakePush) Notify(_ context.Context, j push.Job) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.jobs = append(p.jobs, j)
}
func (p *fakePush) kinds() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for _, j := range p.jobs {
		out = append(out, j.Kind)
	}
	return out
}

type fixture struct {
	m                    *Manager
	hub                  *fakeHub
	db                   *fakeDB
	push                 *fakePush
	alice, bob, carol, x uuid.UUID // alice-bob, alice-carol and bob-carol are friends
}

func newFixture() *fixture {
	f := &fixture{alice: uuid.New(), bob: uuid.New(), carol: uuid.New(), x: uuid.New()}
	f.hub = &fakeHub{devices: map[uuid.UUID][]uuid.UUID{}, out: make(chan got, 100)}
	f.db = &fakeDB{states: map[uuid.UUID]string{}, used: map[uuid.UUID]int64{}}
	f.push = &fakePush{}
	fr := fakeFriends{{f.alice, f.bob}: true, {f.alice, f.carol}: true, {f.bob, f.carol}: true}
	f.m = NewManager(f.hub, fr, f.db, slog.New(slog.DiscardHandler))
	f.m.SetNotifier(f.push)
	f.m.ring, f.m.grace = 80*time.Millisecond, 80*time.Millisecond
	return f
}

func (f *fixture) send(t *testing.T, from ws.Conn, event string, p any) {
	t.Helper()
	raw, _ := json.Marshal(p)
	f.m.HandleCall(context.Background(), from, event, raw)
}

// next returns the next event for device c, skipping others'.
func (f *fixture) next(t *testing.T, c ws.Conn) ws.Event {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case g := <-f.hub.out:
			if g.to == c {
				return g.ev
			}
		case <-deadline:
			t.Fatalf("no event for %v", c)
		}
	}
}

func field(ev ws.Event, k string) string {
	raw, _ := json.Marshal(ev.Payload)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	s, _ := m[k].(string)
	return s
}

func (f *fixture) ring(t *testing.T, caller, callee ws.Conn) uuid.UUID {
	t.Helper()
	f.send(t, caller, EvStart, map[string]string{"to_id": callee.User.String()})
	ev := f.next(t, caller)
	if ev.Event != EvRinging {
		t.Fatalf("caller got %s %v", ev.Event, ev.Payload)
	}
	id := uuid.MustParse(field(ev, "call_id"))
	if ev := f.next(t, callee); ev.Event != EvIncoming || field(ev, "from_id") != caller.User.String() {
		t.Fatalf("callee got %s", ev.Event)
	}
	return id
}

func TestCallFlow(t *testing.T) {
	f := newFixture()
	a, b := f.hub.connect(f.alice), f.hub.connect(f.bob)
	bTablet := f.hub.connect(f.bob)
	id := f.ring(t, a, b)
	if ev := f.next(t, bTablet); ev.Event != EvIncoming {
		t.Fatal("every device of the callee rings")
	}

	// while ringing the caller must say which device a signal is for
	f.send(t, a, EvSignal, map[string]any{"call_id": id, "data": map[string]string{"sdp": "x"}})
	if ev := f.next(t, a); ev.Event != EvError || field(ev, "error") != ErrBadRequest {
		t.Fatalf("early signal without a device: %s %v", ev.Event, ev.Payload)
	}

	f.send(t, b, EvAccept, map[string]any{"call_id": id})
	if ev := f.next(t, a); ev.Event != EvAccepted {
		t.Fatalf("caller got %s", ev.Event)
	}
	if ev := f.next(t, bTablet); ev.Event != EvEnded || field(ev, "reason") != ReasonElsewhere {
		t.Fatalf("other device got %s %v", ev.Event, ev.Payload)
	}
	// the tablet that didn't answer can't take part
	f.send(t, bTablet, EvSignal, map[string]any{"call_id": id, "data": map[string]string{"sdp": "x"}})
	if ev := f.next(t, bTablet); ev.Event != EvError {
		t.Fatal("non-answering device relayed a signal")
	}

	// signals go from one device to the other, unchanged
	f.send(t, a, EvSignal, map[string]any{"call_id": id, "data": map[string]string{"type": "offer", "sdp": "v=0"}})
	ev := f.next(t, b)
	raw, _ := json.Marshal(ev.Payload)
	if ev.Event != EvSignal || string(raw) != `{"call_id":"`+id.String()+`","data":{"sdp":"v=0","type":"offer"},"from_id":"`+f.alice.String()+`"}` {
		t.Fatalf("callee got %s %s", ev.Event, raw)
	}
	// strangers can't inject into the call
	xc := f.hub.connect(f.x)
	f.send(t, xc, EvSignal, map[string]any{"call_id": id, "data": map[string]string{"sdp": "evil"}})
	f.send(t, xc, EvEnd, map[string]any{"call_id": id})
	if ev := f.next(t, xc); ev.Event != EvError {
		t.Fatal("stranger signal accepted")
	}

	f.send(t, b, EvEnd, map[string]any{"call_id": id})
	if ev := f.next(t, a); ev.Event != EvEnded || field(ev, "reason") != ReasonEnded {
		t.Fatalf("caller got %s", ev.Event)
	}
	if s := f.db.state(id); s != "ended" {
		t.Fatalf("log state %q", s)
	}
}

func TestStartRules(t *testing.T) {
	f := newFixture()
	a, b, c := f.hub.connect(f.alice), f.hub.connect(f.bob), f.hub.connect(f.carol)
	xc := f.hub.connect(f.x)

	f.send(t, a, EvStart, map[string]string{"to_id": f.x.String()})
	if ev := f.next(t, a); field(ev, "error") != ErrNotFriends {
		t.Fatalf("stranger call: %v", ev.Payload)
	}
	f.send(t, xc, EvStart, map[string]string{"to_id": f.alice.String()})
	if ev := f.next(t, xc); field(ev, "error") != ErrNotFriends {
		t.Fatal("a stranger rang alice")
	}
	f.send(t, a, EvStart, map[string]string{"to_id": f.alice.String()})
	if ev := f.next(t, a); field(ev, "error") != ErrBadRequest {
		t.Fatal("called myself")
	}

	f.ring(t, a, b)
	// carol calls bob, who is busy
	f.send(t, c, EvStart, map[string]string{"to_id": f.bob.String()})
	if ev := f.next(t, c); ev.Event != EvEnded || field(ev, "reason") != ReasonBusy {
		t.Fatalf("busy: %s %v", ev.Event, ev.Payload)
	}
	// alice is already calling
	f.send(t, a, EvStart, map[string]string{"to_id": f.carol.String()})
	if ev := f.next(t, a); field(ev, "error") != ErrInCall {
		t.Fatal("two calls at once")
	}
}

func TestMissedAndDeclined(t *testing.T) {
	f := newFixture()
	a := f.hub.connect(f.alice)
	// bob is offline: his phone gets a ringing push, then a missed call
	f.send(t, a, EvStart, map[string]string{"to_id": f.bob.String()})
	id := uuid.MustParse(field(f.next(t, a), "call_id"))
	if ev := f.next(t, a); ev.Event != EvEnded || field(ev, "reason") != ReasonMissed {
		t.Fatalf("after the ring timeout: %s", ev.Event)
	}
	if k := f.push.kinds(); len(k) != 2 || k[0] != push.KindCall || k[1] != push.KindMissedCall {
		t.Fatalf("pushes %v", k)
	}
	if f.db.state(id) != ReasonMissed {
		t.Fatalf("log %q", f.db.state(id))
	}

	b := f.hub.connect(f.bob)
	id = f.ring(t, a, b)
	f.send(t, b, EvDecline, map[string]any{"call_id": id})
	if ev := f.next(t, a); field(ev, "reason") != ReasonDeclined {
		t.Fatalf("declined: %v", ev.Payload)
	}
	id = f.ring(t, a, b)
	f.send(t, a, EvEnd, map[string]any{"call_id": id})
	if ev := f.next(t, b); field(ev, "reason") != ReasonCancelled {
		t.Fatalf("cancelled: %v", ev.Payload)
	}
}

func TestLateDeviceRingsAndReconnects(t *testing.T) {
	f := newFixture()
	a := f.hub.connect(f.alice)
	f.send(t, a, EvStart, map[string]string{"to_id": f.bob.String()})
	id := uuid.MustParse(field(f.next(t, a), "call_id"))
	// bob's phone wakes from the push and connects: it rings
	b := f.hub.connect(f.bob)
	f.m.Connected(b)
	if ev := f.next(t, b); ev.Event != EvIncoming {
		t.Fatalf("late device got %s", ev.Event)
	}
	f.send(t, b, EvAccept, map[string]any{"call_id": id})
	f.next(t, a)

	// alice's connection drops briefly: the call survives and moves over
	f.hub.drop(a)
	f.m.Disconnected(a)
	a2 := f.hub.connect(f.alice)
	f.m.Connected(a2)
	if ev := f.next(t, a2); ev.Event != EvResumed {
		t.Fatalf("reconnected device got %s", ev.Event)
	}
	if ev := f.next(t, b); ev.Event != EvResumed {
		t.Fatal("peer not told to restart ICE")
	}
	f.send(t, a2, EvSignal, map[string]any{"call_id": id, "data": map[string]string{"candidate": "c"}})
	if ev := f.next(t, b); ev.Event != EvSignal {
		t.Fatal("signal from the new device not relayed")
	}

	// a drop that lasts ends it
	f.hub.drop(b)
	f.m.Disconnected(b)
	if ev := f.next(t, a2); ev.Event != EvEnded || field(ev, "reason") != ReasonDisconnected {
		t.Fatalf("got %s %v", ev.Event, ev.Payload)
	}
}

func TestUnfriendEndsCallAndLimits(t *testing.T) {
	f := newFixture()
	a, b := f.hub.connect(f.alice), f.hub.connect(f.bob)
	id := f.ring(t, a, b)
	f.send(t, b, EvAccept, map[string]any{"call_id": id})
	f.next(t, a)
	f.m.EndBetween(context.Background(), f.bob, f.alice)
	if ev := f.next(t, a); field(ev, "reason") != ReasonUnfriended {
		t.Fatalf("got %v", ev.Payload)
	}

	// oversize and malformed signalling is refused
	f.send(t, a, EvSignal, map[string]any{"call_id": id, "data": string(make([]byte, maxSignalBytes+1))})
	if ev := f.next(t, a); ev.Event != EvError {
		t.Fatal("oversize payload accepted")
	}
	f.m.HandleCall(context.Background(), a, EvStart, json.RawMessage(`{"to_id":`))
	if ev := f.next(t, a); field(ev, "error") != ErrBadRequest {
		t.Fatal("malformed payload")
	}

	// starting calls is rate limited
	c := f.hub.connect(f.carol)
	limited := false
	for range startsPerMinute + 1 {
		f.send(t, c, EvStart, map[string]string{"to_id": f.alice.String()})
		ev := f.next(t, c)
		if field(ev, "error") == ErrRateLimited {
			limited = true
			break
		}
		if ev.Event == EvRinging {
			f.send(t, c, EvEnd, map[string]any{"call_id": field(ev, "call_id")})
			for f.next(t, c).Event != EvEnded { // CallDevice for alice's device first
			}
		}
	}
	if !limited {
		t.Fatal("no limit on starting calls")
	}
}

func TestDailyCallLimit(t *testing.T) {
	f := newFixture()
	f.m.SetDailyLimit(time.Hour)
	a, b := f.hub.connect(f.alice), f.hub.connect(f.bob)

	// alice talked for an hour today: she can't start another call
	f.db.used[f.alice] = 3600
	f.send(t, a, EvStart, map[string]string{"to_id": f.bob.String()})
	if ev := f.next(t, a); field(ev, "error") != ErrDailyLimit {
		t.Fatalf("over the limit: %s %v", ev.Event, ev.Payload)
	}

	// bob calls her: the call can't be answered either, it ends at once
	f.db.used[f.alice] = 3600
	id := f.ring(t, b, a)
	f.send(t, a, EvAccept, map[string]any{"call_id": id})
	if ev := f.next(t, b); ev.Event != EvEnded || field(ev, "reason") != ReasonLimit {
		t.Fatalf("answering over the limit: %s %v", ev.Event, ev.Payload)
	}

	// with a little time left, the call ends when it runs out
	f.db.used[f.alice] = 3600 - 1 // 1 s left (the fixture runs on real time)
	id = f.ring(t, b, a)
	f.send(t, a, EvAccept, map[string]any{"call_id": id})
	if ev := f.next(t, b); ev.Event != EvAccepted {
		t.Fatalf("got %s", ev.Event)
	}
	deadline := time.After(3 * time.Second)
	for {
		select {
		case g := <-f.hub.out:
			if g.to == b && g.ev.Event == EvEnded {
				if field(g.ev, "reason") != ReasonLimit {
					t.Fatalf("ended with %v", g.ev.Payload)
				}
				return
			}
		case <-deadline:
			t.Fatal("the call outlived the daily limit")
		}
	}
}

// The connection can be set up while the phone rings: one per ringing
// device, relay candidates only, so the caller learns no IP of the callee
// (nor the callee of the caller) unless the call is answered.
func TestConnectWhileRinging(t *testing.T) {
	f := newFixture()
	a := f.hub.connect(f.alice)
	phone, tablet := f.hub.connect(f.bob), f.hub.connect(f.bob)
	f.send(t, a, EvStart, map[string]string{"to_id": f.bob.String()})
	var id string
	devices := map[string]bool{}
	for len(devices) < 2 {
		ev := f.next(t, a)
		switch ev.Event {
		case EvRinging:
			id = field(ev, "call_id")
		case EvDevice:
			if field(ev, "call_id") != id {
				t.Fatalf("device for %v", ev.Payload)
			}
			devices[field(ev, "device")] = true
		default:
			t.Fatalf("caller got %s", ev.Event)
		}
	}
	f.next(t, phone)  // CallIncoming
	f.next(t, tablet) // CallIncoming
	var devPhone, devTablet string
	for d := range devices {
		// find which id is which: offer to d, see who gets it
		f.send(t, a, EvSignal, map[string]any{"call_id": id, "device": d, "data": map[string]string{"type": "offer", "sdp": "v=0\r\nc=IN IP4 171.96.138.164\r\na=" + hostCand + "\r\na=" + relayCand + "\r\n"}})
		g := <-f.hub.out
		if g.ev.Event != EvSignal {
			t.Fatalf("got %s", g.ev.Event)
		}
		raw, _ := json.Marshal(g.ev.Payload)
		noLeak(t, string(raw))
		if g.to == phone {
			devPhone = d
		} else if g.to == tablet {
			devTablet = d
		} else {
			t.Fatalf("offer for %s reached %v", d, g.to)
		}
	}
	if devPhone == "" || devTablet == "" {
		t.Fatal("each device gets its own offer")
	}

	// the callee's host and srflx candidates never reach the caller; relay
	// ones do, tagged with the device, with the related address blanked
	f.send(t, phone, EvSignal, map[string]any{"call_id": id, "data": json.RawMessage(cand(srflxCand))})
	f.send(t, phone, EvSignal, map[string]any{"call_id": id, "data": json.RawMessage(cand(hostCand))})
	f.send(t, phone, EvSignal, map[string]any{"call_id": id, "data": json.RawMessage(cand(relayCand))})
	ev := f.next(t, a)
	raw, _ := json.Marshal(ev.Payload)
	if ev.Event != EvSignal || field(ev, "device") != devPhone || !json.Valid(raw) {
		t.Fatalf("caller got %s %v", ev.Event, ev.Payload)
	}
	noLeak(t, string(raw))
	if !strings.Contains(string(raw), "typ relay") {
		t.Fatalf("relay candidate missing: %s", raw)
	}
	// anything that isn't an offer, answer or candidate is refused
	f.send(t, phone, EvSignal, map[string]any{"call_id": id, "data": map[string]string{"type": "chat", "msg": "hi"}})
	if ev := f.next(t, phone); ev.Event != EvError || field(ev, "error") != ErrBadRequest {
		t.Fatalf("odd signal: %s", ev.Event)
	}
	// strangers can't take part
	x := f.hub.connect(f.carol)
	f.send(t, x, EvSignal, map[string]any{"call_id": id, "device": devPhone, "data": json.RawMessage(cand(relayCand))})
	if ev := f.next(t, x); field(ev, "error") != ErrUnknownCall {
		t.Fatalf("stranger: %s", ev.Event)
	}

	// a device that goes away is announced; one that comes online rings too
	f.hub.drop(tablet)
	f.m.Disconnected(tablet)
	if ev := f.next(t, a); ev.Event != EvGone || field(ev, "device") != devTablet {
		t.Fatalf("caller got %s %v", ev.Event, ev.Payload)
	}
	late := f.hub.connect(f.bob)
	f.m.Connected(late)
	if ev := f.next(t, a); ev.Event != EvDevice || field(ev, "device") == "" || field(ev, "device") == devTablet {
		t.Fatalf("late device: %s %v", ev.Event, ev.Payload)
	}

	// answering: the caller learns which device; from then on signals pass
	// unchanged between the two, and no more devices are announced
	f.send(t, phone, EvAccept, map[string]any{"call_id": id})
	if ev := f.next(t, a); ev.Event != EvAccepted || field(ev, "device") != devPhone {
		t.Fatalf("accepted: %s %v", ev.Event, ev.Payload)
	}
	if ev := f.next(t, late); field(ev, "reason") != ReasonElsewhere {
		t.Fatalf("late device: %v", ev.Payload)
	}
	f.send(t, phone, EvSignal, map[string]any{"call_id": id, "data": json.RawMessage(cand(srflxCand))})
	if ev := f.next(t, a); field(ev, "device") != devPhone {
		t.Fatalf("after accept: %v", fmt.Sprint(ev.Payload))
	} else if raw, _ := json.Marshal(ev.Payload); !strings.Contains(string(raw), "typ srflx") {
		t.Fatalf("after accept signals pass unchanged: %s", raw)
	}
	another := f.hub.connect(f.bob)
	f.m.Connected(another)
	select {
	case g := <-f.hub.out:
		if g.to == a {
			t.Fatalf("caller got %s after accept", g.ev.Event)
		}
	case <-time.After(50 * time.Millisecond):
	}
}
