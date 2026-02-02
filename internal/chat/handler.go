package chat

import (
	"log"
	"net/http"

	chatmodel "RyanDev-21.com/Chirpy/internal/chatModel"
	"RyanDev-21.com/Chirpy/pkg/encoder"
	"RyanDev-21.com/Chirpy/pkg/middleware"
	"RyanDev-21.com/Chirpy/pkg/response"
)



type chatHandler struct{
	chatService ChatService
}


func NewChatHandler(chatService ChatService)*chatHandler{
	return &chatHandler{
		chatService: chatService,
	}
}


func (h *chatHandler)ServeWs(w http.ResponseWriter,r *http.Request){
	userID,err:= middleware.GetContextKey(r.Context(),"user")
	if err !=nil{
		log.Printf("failed to get the id")
		response.Error(w,401,"unauthorized")
		return
	}	
	conn, err:=h.chatService.upgradeWebsocket(w,r)
	if err !=nil{
		log.Printf("WebSocket upgrade failed: %v", err)
		response.Error(w,500,"cannot switch to websocket")
		return
	}

	h.chatService.initWs(conn,*userID)

}

//maybe consider abstracting the gettting context key
//NOTE:maybe add validating the content of the message like limit or smth
func (h *chatHandler)SendMessage(w http.ResponseWriter,r *http.Request){
	userID,err:=middleware.GetContextKey(r.Context(),"user")
	if err !=nil{
		log.Printf("failed to get the id")
		response.Error(w,401,"unauthorized")
		return
	}
	payload := chatmodel.Message{}
	err =encoder.Decode(r,&payload)
	if err !=nil{
		response.Error(w,400,"bad request")
		return
	}	
	err = h.chatService.sendMessage(r.Context(),*userID,&payload)
	if err !=nil{
		if err == ErrNotSupportedTypeMsg{
			response.Error(w,400,"bad request")
			return
		}
		if err == chatmodel.ErrNoClientFound{
			response.Error(w,404,"client not found,consider connecting to ws")
			return
		}
		response.Error(w,500,"something went wrong")	
		return
	}	
	w.WriteHeader(201)
}
