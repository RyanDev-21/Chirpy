package chat

import (
	"net/http"

	chatmodel "RyanDev-21.com/Chirpy/internal/chat/chatModel"
	"github.com/gorilla/websocket"
)
var upgrader = websocket.Upgrader{
	ReadBufferSize: 1024,
	WriteBufferSize: 1024,
}
type ChatService interface{
	upgradeWebsocket(w http.ResponseWriter,r *http.Request)(*websocket.Conn,error)
	initWs(conn *websocket.Conn)
}


type chatService struct{
	chatRepo ChatRepo
	hub *chatmodel.Hub
}


func NewChatService(chatRepo ChatRepo)ChatService{
	hub := chatmodel.NewHub()
	go hub.Run()

	return &chatService{
		chatRepo: chatRepo,	
		hub: hub,
		}
	}



func (s *chatService)initWs(conn *websocket.Conn){
	client := chatmodel.NewClient(s.hub,conn,make(chan []byte,256))
	client.Hub.Register <- client

	go client.WritePump()
	go client.ReadPump()

}

func (s *chatService)upgradeWebsocket(w http.ResponseWriter,r *http.Request)(*websocket.Conn,error){
	conn, err := upgrader.Upgrade(w,r,nil)
	if err !=nil{
		return nil,err
	}
	return conn, nil
}	

