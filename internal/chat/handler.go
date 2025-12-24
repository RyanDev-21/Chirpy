package chat

import (
	"log"
	"net/http"

	"RyanDev-21.com/Chirpy/pkg/middleware"
	"RyanDev-21.com/Chirpy/pkg/response"
	"github.com/google/uuid"
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
	userID ,ok:= r.Context().Value(middleware.USERCONTEXTKEY).(uuid.UUID)
	if !ok{
		response.Error(w,401,"unauthorized")
		return
	}
		
	conn, err:=h.chatService.upgradeWebsocket(w,r)
	if err !=nil{
		log.Printf("WebSocket upgrade failed: %v", err)
		response.Error(w,500,"cannot switch to websocket")
		return
	}

	h.chatService.initWs(conn,userID)

}
