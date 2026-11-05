package ws

import "testing"

// Random strings must never redeem as a ticket.
func FuzzRedeemTicket(f *testing.F) {
	tk := NewTickets("secret")
	good, _ := tk.Issue([16]byte{1})
	f.Add(good[:len(good)-1])
	f.Add("")
	f.Fuzz(func(t *testing.T, s string) {
		if s == good {
			return
		}
		if _, _, ok := tk.Redeem(s); ok {
			t.Fatalf("forged ticket accepted: %q", s)
		}
	})
}
