package chat

import (
	"log"
	"net/http"

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
	conn, err:=h.chatService.upgradeWebsocket(w,r)
	if err !=nil{
		log.Printf("WebSocket upgrade failed: %v", err)
		response.Error(w,500,"cannot switch to websocket")
		return
	}

	h.chatService.initWs(conn)

}
