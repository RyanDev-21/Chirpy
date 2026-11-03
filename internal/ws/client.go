package ws

import (
	"context"
	"encoding/json"
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

var newline = []byte{'\n'}

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
				c.Hub.logger.Debug("websocket closed unexpectedly", "user_id", c.UserID, "error", err)
			}
			break
		}

		var eventMap InCommingEvent
		if err := json.Unmarshal(rawMsg, &eventMap); err != nil {
			continue
		}

		c.handleEvent(eventMap)
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

// handleEvent routes a client-sent event through the hub's policy. Rejected
// or malformed events are dropped without telling the sender.
func (c *Client) handleEvent(ev InCommingEvent) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	switch ev.Event {
	case EventTyping:
		var p InCommingEventForTyping
		to, err := decodeTarget(ev.Payload, &p, func() string { return p.ToID })
		if err != nil {
			c.Hub.logger.Debug("ignoring malformed websocket event", "user_id", c.UserID, "event", ev.Event)
			return
		}
		group := p.Type == "public"
		targets, err := c.Hub.policy.TypingTargets(ctx, c.UserID, to, group)
		if err != nil {
			c.Hub.logger.Warn("typing policy failed", "user_id", c.UserID, "error", err)
			return
		}
		out := OutGoingEventForTyping{FromID: c.UserID.String()}
		if group {
			out.GroupID = to.String()
		}
		_ = c.Hub.WriteToUsers(targets, Event{Event: EventTyping, Payload: out})
	case EventSeen:
		var p InCommingEventForSeen
		to, err := decodeTarget(ev.Payload, &p, func() string { return p.ToID })
		if err != nil {
			c.Hub.logger.Debug("ignoring malformed websocket event", "user_id", c.UserID, "event", ev.Event)
			return
		}
		ok, err := c.Hub.policy.SeenAllowed(ctx, c.UserID, to)
		if err != nil {
			c.Hub.logger.Warn("seen policy failed", "user_id", c.UserID, "error", err)
			return
		}
		if ok {
			_ = c.Hub.WriteIntoConnection(to, Event{Event: EventSeen, Payload: OutGoingEventForSeen{MsgID: p.MsgID, FromID: c.UserID.String()}})
		}
	}
}

func decodeTarget[T any](raw json.RawMessage, v *T, id func() string) (uuid.UUID, error) {
	if err := json.Unmarshal(raw, v); err != nil {
		return uuid.Nil, err
	}
	return uuid.Parse(id())
}
