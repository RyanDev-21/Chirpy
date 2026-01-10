package chat

import (
	"net/http"

	chatmodel "RyanDev-21.com/Chirpy/internal/chat/chatModel"
	mq "RyanDev-21.com/Chirpy/internal/customMq"
	//rabbitmq "RyanDev-21.com/Chirpy/internal/rabbitMq"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)
var upgrader = websocket.Upgrader{
	ReadBufferSize: 1024,
	WriteBufferSize: 1024,
}
type ChatService interface{
	upgradeWebsocket(w http.ResponseWriter,r *http.Request)(*websocket.Conn,error)
	initWs(conn *websocket.Conn,userID uuid.UUID)
//	createGroup(userID uuid.UUID)
}


type chatService struct{
	chatRepo ChatRepo
	hub *chatmodel.Hub
	mq *mq.MainMQ
}


func NewChatService(chatRepo ChatRepo,hub *chatmodel.Hub,mq *mq.MainMQ)ChatService{

	return &chatService{
		chatRepo: chatRepo,	
		hub: hub,
		mq:mq,
	}
	}



func (s *chatService)initWs(conn *websocket.Conn,userID uuid.UUID){
	client := chatmodel.NewClient(s.hub,conn,make(chan []byte,256),userID)
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

