package ws

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestTickets(t *testing.T) {
	tk := NewTickets("secret")
	now := time.Now()
	tk.now = func() time.Time { return now }
	u := uuid.New()
	ticket, err := tk.Issue(u)
	if err != nil || strings.ContainsAny(ticket, "+/=") {
		t.Fatalf("ticket %q must be URL-safe: %v", ticket, err)
	}
	if got, issued, ok := tk.Redeem(ticket); !ok || got != u || !issued.Equal(now.Truncate(time.Millisecond)) {
		t.Fatalf("redeem = %v %v %v", got, issued, ok)
	}
	if _, _, ok := tk.Redeem(ticket); ok {
		t.Fatal("a ticket works only once")
	}
	late, _ := tk.Issue(u)
	now = now.Add(TicketTTL)
	if _, _, ok := tk.Redeem(late); ok {
		t.Fatal("expired ticket accepted")
	}
	// another server secret, a changed user, garbage
	other, _ := NewTickets("other").Issue(u)
	fresh, _ := tk.Issue(u)
	raw := []byte(fresh)
	raw[3] ^= 1
	for _, bad := range []string{other, string(raw), "", "abc", strings.Repeat("A", 200)} {
		if _, _, ok := tk.Redeem(bad); ok {
			t.Errorf("accepted %q", bad)
		}
	}
}
