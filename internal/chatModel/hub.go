package chatmodel

import (
	"encoding/json"
	"errors"
	"log"
	"sync"

	"github.com/google/uuid"
)

type Hub struct {
	// contains all the chatId which the user register
	UsertoChannel map[string]map[string]bool

	// contains all the users id of the specific chat
	ChatToUser map[string]map[string]bool
		
	Clients    map[string]*Client
	ClientMux  sync.RWMutex
	// register and unregister are for the connection
	Register   chan *Client
	Unregister chan *Client

	// the client use this channel to broadcast the message onto the hub
	Broadcast chan interface{}

	// group sepcific channel
	JoinChan  chan GroupActionInfo
	LeaveChan chan GroupActionInfo
}

type JoinStruct struct {
	userID  uuid.UUID
	groupID uuid.UUID
	role    string
}

func NewHub() *Hub {
	return &Hub{
		UsertoChannel: make(map[string]map[string]bool),
		ChatToUser:    make(map[string]map[string]bool),
		Broadcast:     make(chan interface{},1000),
		Register:      make(chan *Client),
		Unregister:    make(chan *Client),
		Clients:       make(map[string]*Client),
		JoinChan:      make(chan GroupActionInfo, 256),
		LeaveChan:     make(chan GroupActionInfo, 256),
	}
}

var ErrNoClientFound = errors.New("no client exist")

func (h *Hub) CheckWsConnection(clientID uuid.UUID) bool {
	h.ClientMux.RLock()
	defer h.ClientMux.RUnlock()
	if _, ok := h.Clients[clientID.String()]; ok {
		return true
	}
	return false
}

// this one should correctly write into conneciton
func (h *Hub) WriteIntoConnection(clientID uuid.UUID, payload Event) error {
	var targetIds []string
	switch payload.Event {
	case "msg":
		msg := payload.Payload.(OutGoingMessage)
		targetIds = append(targetIds, h.getTarGetIdsForMsg(msg.Type, clientID)...)
	case "AddFri":
		targetIds = append(targetIds, clientID.String())
	case "AcceptFri":
		targetIds = append(targetIds, clientID.String())
	case "DenyFri":
		targetIds = append(targetIds, clientID.String())

	}
	bytes, err := json.Marshal(payload)
	if err != nil {
		log.Printf("failed to pare into bytes")
		return err
	}

	h.broadcast(targetIds, bytes)
	return nil
}

// func (h *Hub)CheckOnline(clientID uuid.UUID)bool{
// 	h.ClientMux.RLock()
// 	defer h.ClientMux.RUnlock()
// 	if _,ok:= h.Clients[clientID.String()];ok{
// 		return true
// 	}
// 	return false
// }

// this is getting bloated need to refactor later
func (h *Hub) Run() {
	for {
		select {
		// stores the userID as key and its client as connection
		case client := <-h.Register:
			h.Clients[client.UserID.String()] = client
		// remove from the hub where the id match
		// and search user's chatidlist
		// and delete the userId in that each of chattouser's chatidlist
		// and then finally delete the userid from the usertochat

		// when join update the user relation with the chat in both
		// userToChat && chatToUser
		case client := <-h.JoinChan:
			userID := client.UserID.String()
			groupID := client.GroupID.String()
			if h.UsertoChannel[userID] == nil {
				h.UsertoChannel[userID] = make(map[string]bool)
			}
			h.UsertoChannel[userID][groupID] = true
			if h.ChatToUser[groupID] == nil {
				h.ChatToUser[groupID] = make(map[string]bool)
			}
			h.ChatToUser[groupID][userID] = true

		// same thing just the opposite of the join
		case client := <-h.LeaveChan:
			groupID := client.GroupID.String()
			userID := client.UserID.String()
			if usersIDList, ok := h.ChatToUser[groupID]; ok {
				delete(usersIDList, userID)
				if len(usersIDList) == 0 {
					delete(h.ChatToUser, groupID)
				}
			}
			if chatsIDList, ok := h.UsertoChannel[userID]; ok {
				delete(chatsIDList, groupID)
				if len(chatsIDList) == 0 {
					delete(h.UsertoChannel, userID)
				}
			}

		case client := <-h.Unregister:
			if _, ok := h.Clients[client.UserID.String()]; ok {
				delete(h.Clients, client.UserID.String())
				close(client.Send)
			}
			if chatIds, ok := h.UsertoChannel[client.UserID.String()]; ok {
				for chat := range chatIds {
					if usersInChat, ok := h.ChatToUser[chat]; ok {
						delete(usersInChat, client.UserID.String())
						if len(usersInChat) == 0 {
							delete(h.ChatToUser, chat)
						}
					}
				}
			}
			delete(h.UsertoChannel, client.UserID.String())

		// basically stores the targetIds based on the type of the message
		// if it is private you find in the clients and then write to it
		// if it is public/group you find the id in the chatTouser based on
		// the chat id and then write to the every single connection of those ids
		// of that chat
		case event := <-h.Broadcast:
			var payload Event
			var targetIds []string
			eventType := event.(Event)      // this typecast into Event
			evePayload := eventType.Payload // this get the payload ofEvent
			switch inPayload := evePayload.(type) {
			case TypoEvent:
				payload = Event{
					Event:   eventType.Event,
					Payload: inPayload.FromID,
				}
				targetIds = append(targetIds, inPayload.ToID)
				// case OutFriEvent:
				// 	targetIds = append(targetIds, inPayload.)
				// 	payload = Event
				// 		Event: eventType.Event,
				// 		Payload: OutFriEvent{
				// 			ReqID:  inPayload.reqID,
				// 			FromID: inPayload.fromID,
				// 		},
				// 	}
		
		case SeenEvent:		
				payload = Event{
					Event: eventType.Event,
					Payload: OutGoingEventForSeen{
						FromID: inPayload.FromID,
						MsgID: inPayload.MsgID,
					},

				}
			targetIds  = append(targetIds, inPayload.ToID)
			}
			bytes, err := json.Marshal(payload)
			if err != nil {
				log.Fatal("failed to marshal into bytes")
				return
			}
			h.broadcast(targetIds, bytes)
		}
	}
}

func (h *Hub) broadcast(targetIds []string, bytes []byte) {
	if len(targetIds) > 0 {
		for _, clientID := range targetIds {
			h.ClientMux.Lock()
			client, ok := h.Clients[clientID]
			h.ClientMux.Unlock()
			if !ok{
				continue
			}
			select {
				case client.Send <- bytes:
				default:
			
				h.ClientMux.Lock()
				close(h.Clients[clientID].Send)
					delete(h.Clients, clientID)
				h.ClientMux.Unlock()
			}
			
		}

	}
}

func (h *Hub) getTarGetIdsForMsg(msgType string, toID uuid.UUID) []string {
	var targetIds []string
	switch msgType {
	case "private":
		targetIds = append(targetIds, toID.String())
	case "public":
		log.Printf("userIds list and its chats #%v#", h.ChatToUser[toID.String()])
		if userIdsInChat, ok := h.ChatToUser[toID.String()]; ok {

			for userID := range userIdsInChat {
				targetIds = append(targetIds, userID)
			}
		}
	}
	return targetIds
}

// func (h *Hub) handleMessageStruct(msg Message) (*OutGoingMessage, []string) {
// 	var payload OutGoingMessage
// 	var targetIds []string
// 	targetIds = h.getTarGetIdsForMsg(msg)
// 	payload = OutGoingMessage{
// 		Content:  msg.Content,
// 		FromID:   msg.FromID,
// 		Type:     msg.Type,
// 		ParentID: msg.ParentID,
// 	}
//
// 	return &payload, targetIds
// }
