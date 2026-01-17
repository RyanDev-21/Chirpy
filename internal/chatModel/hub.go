package chatmodel

import (
	"fmt"
	"log"

	"github.com/google/uuid"
)




type Hub struct{
	//contains all the chatId which the user register
	UsertoChannel map[string]map[string]bool

	//contains all the users id of the specific chat
	ChatToUser map[string]map[string]bool
	Clients map[string]*Client
	//register and unregister are for the connection	
	Register chan *Client
	Unregister chan *Client

	//the client use this channel to broadcast the message onto the hub
	Broadcast chan Message

	//group sepcific channel
	JoinChan chan GroupActionInfo
	LeaveChan chan GroupActionInfo
}


type JoinStruct struct{
	userID uuid.UUID
	groupID uuid.UUID
	role string
}

func NewHub()*Hub{
	return &Hub{
		UsertoChannel:make(map[string]map[string]bool) ,
		ChatToUser: make(map[string]map[string]bool),
		Broadcast: make(chan Message),
		Register: make(chan *Client),
		Unregister: make(chan *Client),
		Clients: make(map[string]*Client),
		JoinChan: make(chan GroupActionInfo,256),
		LeaveChan: make(chan GroupActionInfo, 256),
	}
}



//this is getting bloated need to refactor later
func (h *Hub)Run(){
	for {
		select{
		//stores the userID as key and its client as connection
		case client := <-h.Register:
			h.Clients[client.UserID.String()] = client
		//remove from the hub where the id match
		//and search user's chatidlist
		// and delete the userId in that each of chattouser's chatidlist
		//and then finally delete the userid from the usertochat

		//when join update the user relation with the chat in both 
		//userToChat && chatToUser
		case client := <-h.JoinChan:
			userID := client.UserID.String()
			groupID := client.GroupID.String()
			if h.UsertoChannel[userID] == nil{
				h.UsertoChannel[userID] = make(map[string]bool)
			}
			h.UsertoChannel[userID][groupID] = true
			if h.ChatToUser[groupID] == nil{
				h.ChatToUser[groupID] = make(map[string]bool)
			}
			h.ChatToUser[groupID][userID] = true
					
		//same thing just the opposite of the join
		case client := <- h.LeaveChan:
			groupID := client.GroupID.String()
			userID := client.UserID.String()
			if usersIDList,ok := h.ChatToUser[groupID]; ok{
				delete(usersIDList,userID)
				if len(usersIDList)==0{
					delete(h.ChatToUser,groupID)
				}
			}	
			if chatsIDList,ok := h.UsertoChannel[userID];ok{
				delete(chatsIDList,groupID)
				if len(chatsIDList)==0{
					delete(h.UsertoChannel,userID)
				}	
			}
			
		case client := <-h.Unregister:
			if _,ok:= h.Clients[client.UserID.String()];ok{
				delete(h.Clients,client.UserID.String())
				close(client.Send)
			}
			if chatIds, ok := h.UsertoChannel[client.UserID.String()]; ok{
				for chat := range chatIds{
					if usersInChat,ok := h.ChatToUser[chat]; ok{
						delete(usersInChat,client.UserID.String())	
						if len(usersInChat)== 0{
							delete(h.ChatToUser,chat)
						}
					}
				}
			}
			delete(h.UsertoChannel,client.UserID.String())
		
		//basically stores the targetIds based on the type of the message
		//if it is private you find in the clients and then write to it 
		//if it is public/group you find the id in the chatTouser based on 
		//the chat id and then write to the every single connection of those ids 
		//of that chat
		case message := <-h.Broadcast:
			var targetIds []string	
			//update the cache first
			switch message.Type{
			//just for the sake of this i put the type in th msg struct	
			case "private":
				targetIds = append(targetIds, message.ToID)
			case "public":
				log.Printf("userIds list and its chats #%v#",h.ChatToUser[message.ToID])
				if userIdsInChat ,ok:= h.ChatToUser[message.ToID];ok{
					for userID := range userIdsInChat{
						targetIds = append(targetIds, userID)
					}
				}
			}	
					
			if len(targetIds)>0{
				for _,clientID:= range targetIds{
					//i need to implement mutex lock or smth
					if _,ok:= h.Clients[clientID];ok{
						select {
						case h.Clients[clientID].Send<- []byte(message.Content):
						default: 
						close(h.Clients[clientID].Send)
						delete(h.Clients,clientID)
						}		
					}

					}
				
			}else{
				fmt.Println("stored it in db")
			}	
		}
				}
}

