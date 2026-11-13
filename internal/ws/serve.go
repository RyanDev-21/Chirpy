package ws

import (
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// Upgrader returns a websocket upgrader that accepts requests without an
// Origin header (native clients) or from one of the allowed origins.
func Upgrader(allowedOrigins []string) *websocket.Upgrader {
	anyOrigin := slices.Contains(allowedOrigins, "*")
	return &websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			return origin == "" || anyOrigin || slices.Contains(allowedOrigins, origin) || sameHost(origin, r.Host)
		},
	}
}

// Serve upgrades the connection and registers the client with the hub.
func (h *Hub) Serve(up *websocket.Upgrader, w http.ResponseWriter, r *http.Request, userID uuid.UUID) error {
	conn, err := up.Upgrade(w, r, nil)
	if err != nil {
		return err // the upgrader already wrote an HTTP error
	}
	client := NewClient(h, conn, make(chan []byte, 256), userID)
	h.Register <- client
	go client.WritePump()
	go client.ReadPump()
	return nil
}

// sameHost reports whether origin names the server itself. React Native on
// Android sends the API's own address as Origin. A page on another site
// sends its own host, so it's still refused (that's what the check stops).
func sameHost(origin, host string) bool {
	u, err := url.Parse(origin)
	return err == nil && u.Host != "" && strings.EqualFold(u.Host, host)
}
