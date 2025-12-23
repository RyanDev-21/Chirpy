package chatmodel


//might take chat_id and then stores the client from as its value 
//i don't know might fix it later
type Hub struct{
	Clients map[*Client]bool
	Register chan *Client
	Unregister chan *Client
	Broadcast chan []byte
}


func NewHub()*Hub{
	return &Hub{
		Broadcast: make(chan []byte),
		Register: make(chan *Client),
		Unregister: make(chan *Client),
		Clients: make(map[*Client]bool),
	}
}


func (h *Hub)Run(){
	for {
		select{
		case client := <-h.Register:
			h.Clients[client] = true
		case client := <-h.Unregister:
			if _,ok:= h.Clients[client];ok{
				delete(h.Clients,client)
				close(client.Send)
			}
		case message := <-h.Broadcast:
			for client := range h.Clients{
				select {
				case client.Send <- message:
				default:
					close(client.Send)
					delete(h.Clients,client)
				}
				
			}	

		}
	}
}
