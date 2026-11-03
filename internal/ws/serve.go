package ws

import (
	"net/http"
	"slices"

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
			return origin == "" || anyOrigin || slices.Contains(allowedOrigins, origin)
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
