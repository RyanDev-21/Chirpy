package chat

import (
	"context"
	"errors"
	"net/http"

	chatmodel "RyanDev-21.com/Chirpy/internal/chatModel"
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
	sendMessage(ctx context.Context,userID uuid.UUID,paylod *chatmodel.Message)error
	StartWorkerForAddPrivateMessage(channel chan *mq.Channel)
	StartWorkerForAddPublicMessage(channel chan *mq.Channel)
}
type chatService struct{
	chatRepo ChatRepo
	hub *chatmodel.Hub
	mq *mq.MainMQ
	rediscache ChatRepoCache
}


var ErrNotSupportedTypeMsg = errors.New("not supportted type of message")

func NewChatService(chatRepo ChatRepo,hub *chatmodel.Hub,mq *mq.MainMQ,cache ChatRepoCache)ChatService{

	return &chatService{
		chatRepo: chatRepo,	
		hub: hub,
		mq:mq,
		rediscache: cache,
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

//send the message struct based on the toid
func (s *chatService)sendMessage(ctx context.Context,userID uuid.UUID,payload *chatmodel.Message)error{
	toID,err := uuid.Parse(payload.ToID)
	if err!=nil{
		return errors.New("not valid toID(type uuid)")
	}
	var parentParseID *uuid.UUID
	if payload.ParendID  != ""{
		*parentParseID,err = uuid.Parse(payload.ParendID)
		if err!=nil{
		return errors.New("not valid parentID(type uuid)")
	}
	}

	//handle the reply and stuff	
	switch payload.Type{
	case "private":
		handlePrivateMsg(userID,toID,payload,s.rediscache)//update the cache
		publishJobHelper("addPrivateMessage",payload,s.mq)//upadate the db
	case "public":
		handlePublicMsg(userID,payload,s.rediscache)
		publishJobHelper("addPublicMessage",payload,s.mq)
	default:
		return  ErrNotSupportedTypeMsg	
	}	

	//i need to somehow get the client connection and then use the send one
	err=s.hub.WriteIntoConnection(userID,payload)	
	
	return err

}
