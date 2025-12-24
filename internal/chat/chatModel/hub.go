package chatmodel

import "fmt"

//import "strings"

//might take chat_id and then stores the client from as its value
//i don't know might fix it later
type Hub struct{
	//contains all the chatId whcih the user register
	UsertoChat map[string]map[string]bool

	//contains all the users id of the specific chat
	ChatToUser map[string]map[string]bool
	Clients map[string]*Client
	Register chan *Client
	Unregister chan *Client
	Broadcast chan Message
}


func NewHub()*Hub{
	return &Hub{
		UsertoChat:make(map[string]map[string]bool) ,
		ChatToUser: make(map[string]map[string]bool),
		Broadcast: make(chan Message),
		Register: make(chan *Client),
		Unregister: make(chan *Client),
		Clients: make(map[string]*Client),
	}
}


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
		case client := <-h.Unregister:
			if _,ok:= h.Clients[client.UserID.String()];ok{
				delete(h.Clients,client.UserID.String())
				close(client.Send)
			}
			if chatIds, ok := h.UsertoChat[client.UserID.String()]; ok{
				for chat := range chatIds{
					if usersInChat,ok := h.ChatToUser[chat]; ok{
						delete(usersInChat,client.UserID.String())	
						if len(usersInChat)== 0{
							delete(h.ChatToUser,chat)
						}
					}
				}
			}
			delete(h.UsertoChat,client.UserID.String())
		
		//basically stores the targetIds based on the type of the message
		//if it is private you find in the clients and then write to it 
		//if it is public/group you find the id in the chatTouser based on 
		//the chat id and then write to the every single connection of those ids 
		//of that chat
		case message := <-h.Broadcast:
			var targetIds []string	
			switch message.Type{
			case "private":
				targetIds = append(targetIds, message.ToID)
			case "public":
				if userInChat ,ok:= h.ChatToUser[message.ToID];ok{
					for userID := range userInChat{
						targetIds = append(targetIds, userID)
					}

				}
				
			}	
			
			if len(targetIds)>0{
				for _,clientID:= range targetIds{
					select{
					case h.Clients[clientID].Send <- []byte(message.Content):
					default: 
						close(h.Clients[clientID].Send)
						delete(h.Clients,message.ToID)
					}
				}
			}else{
				fmt.Println("stored it in db")
			}	
		}

				}
	}

