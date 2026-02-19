package chatmodel

import (
	//	"bytes"
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

	maxMessageSize = 512
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

		var msgMap map[string]interface{}
		if err := json.Unmarshal(rawMsg, &msgMap); err != nil {
			continue
		}

		// Check if it's an InCommingEvent (has event, no type)
		if _, hasEvent := msgMap["event"]; hasEvent {
			if _, hasType := msgMap["type"]; !hasType {
				var inEvent InCommingEvent
				json.Unmarshal(rawMsg, &inEvent)
				c.Hub.Broadcast <- Event{
					FromID: c.UserID.String(),
					ToID:   inEvent.ToID,
					Event:  inEvent.Event,
				}
				continue
			}
		}

		// Otherwise treat as Message
		var message Message
		json.Unmarshal(rawMsg, &message)
		c.Hub.Broadcast <- *convertIntoInCommingStruct(message, c.UserID)

	}
}

func convertIntoInCommingStruct(msg Message, userID uuid.UUID) *OutGoingMessage {
	return &OutGoingMessage{
		Content:  msg.Content,
		ToID:     msg.ToID,
		ParentID: msg.ParendID,
		FromID:   userID.String(),
		Type:     msg.Type,
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
