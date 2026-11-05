package ws

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"sync"
	"time"

	"github.com/google/uuid"
)

// TicketTTL: a browser opens its websocket right after asking for a ticket.
const TicketTTL = 30 * time.Second

// Tickets lets browsers open the websocket: they can't send an
// Authorization header on the upgrade request, so they first trade their
// access token for a ticket (POST /api/chats/ws/ticket) and pass it as
// ?ticket=. A ticket is signed, names one user, expires after TicketTTL and
// works once. Single use is tracked in memory (one server, like the hub).
type Tickets struct {
	key []byte
	now func() time.Time

	mu   sync.Mutex
	used map[[16]byte]time.Time // nonce -> expiry
}

func NewTickets(secret string) *Tickets {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte("chirpy websocket ticket"))
	return &Tickets{key: m.Sum(nil), now: time.Now, used: map[[16]byte]time.Time{}}
}

const ticketPayload = 16 + 8 + 16 // user, expiry (ms), nonce

func (t *Tickets) sign(p []byte) []byte {
	m := hmac.New(sha256.New, t.key)
	m.Write(p)
	return m.Sum(nil)
}

// Issue returns a ticket for user.
func (t *Tickets) Issue(user uuid.UUID) (string, error) {
	p := make([]byte, ticketPayload)
	copy(p, user[:])
	binary.BigEndian.PutUint64(p[16:], uint64(t.now().Add(TicketTTL).UnixMilli()))
	if _, err := rand.Read(p[24:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(append(p, t.sign(p)...)), nil
}

// Redeem checks and uses up a ticket. issued is when it was made, for the
// revocation check (sessions ended after that must not connect).
func (t *Tickets) Redeem(ticket string) (user uuid.UUID, issued time.Time, ok bool) {
	raw, err := base64.RawURLEncoding.DecodeString(ticket)
	if err != nil || len(raw) != ticketPayload+sha256.Size {
		return uuid.Nil, time.Time{}, false
	}
	p, sig := raw[:ticketPayload], raw[ticketPayload:]
	if !hmac.Equal(sig, t.sign(p)) {
		return uuid.Nil, time.Time{}, false
	}
	exp := time.UnixMilli(int64(binary.BigEndian.Uint64(p[16:24])))
	now := t.now()
	if !now.Before(exp) {
		return uuid.Nil, time.Time{}, false
	}
	var nonce [16]byte
	copy(nonce[:], p[24:])
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, seen := t.used[nonce]; seen {
		return uuid.Nil, time.Time{}, false
	}
	for n, e := range t.used { // forget expired ones; the map stays small
		if !now.Before(e) {
			delete(t.used, n)
		}
	}
	t.used[nonce] = exp
	copy(user[:], p[:16])
	return user, exp.Add(-TicketTTL), true
}
