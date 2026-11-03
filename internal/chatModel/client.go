package chatmodel

import (
	"encoding/json"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// this is standard way of doing this i guess
const (
	writeWait = 10 * time.Second

	pongWait = 60 * time.Second

	pingPeriod = (pongWait * 9) / 10

	maxMessageSize = 4096
)

type Client struct {
	Hub    *Hub
	Conn   *websocket.Conn
	Send   chan []byte
	UserID uuid.UUID
}

func NewClient(hub *Hub, conn *websocket.Conn, send chan []byte, userID uuid.UUID) *Client {
	return &Client{
		Hub:    hub,
		Conn:   conn,
		Send:   send,
		UserID: userID,
	}
}

var (
	newline = []byte{'\n'}
	space   = []byte{' '}
)

// read message from the connection
// and Send the msg to the Broadcast channel
func (c *Client) ReadPump() {
	defer func() {
		c.Hub.Unregister <- c
		c.Conn.Close()
	}()

	c.Conn.SetReadLimit(maxMessageSize)
	c.Conn.SetReadDeadline(time.Now().Add(pongWait))
	// when it recieve pong message from the connection will add more sec to deadline
	c.Conn.SetPongHandler(func(string) error { c.Conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })

	var rawMsg json.RawMessage
	for {
		// maybe consider only accepting the msg.Content
		// NOTE::this read json as we are writing json maybe consider writing raw bytes
		err := c.Conn.ReadJSON(&rawMsg)
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("error :%v", err)
			}
			break
		}

		var eventMap InCommingEvent
		if err := json.Unmarshal(rawMsg, &eventMap); err != nil {
			continue
		}

		switch eventMap.Event {
		case "Typing":
			var p InCommingEventForTyping
			if err := json.Unmarshal(eventMap.Payload, &p); err != nil {
				log.Printf("not valid json")
				continue
			}
			c.Hub.Broadcast <- Event{
				Event: "Typing",
				Payload: TypoEvent{
					FromID: c.UserID.String(),
					ToID:   p.ToID,
				},
			}
		case "Seen":
			var p InCommingEventForSeen
			if err := json.Unmarshal(eventMap.Payload, &p); err != nil {
				log.Printf("not valid json")
				continue
			}
			c.Hub.Broadcast <- Event{
				Event: "Seen",
				Payload: SeenEvent{
					FromID: c.UserID.String(),
					ToID:   p.ToID,
					MsgID:  p.MsgID,
				},
			}

		}
	}
}

// read from the Send chanel and write it to the connection
func (c *Client) WritePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.Conn.Close()
	}()
	for {
		select {
		case message, ok := <-c.Send:
			c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// The Hub closed the channel
				c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			w, err := c.Conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)
			n := len(c.Send)
			for i := 0; i < n; i++ {
				w.Write(newline)
				w.Write(<-c.Send)
			}

			if err := w.Close(); err != nil {
				return
			}
		case <-ticker.C:
			c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
