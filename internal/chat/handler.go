package chat

import (
	"log"
	"net/http"

	chatmodel "RyanDev-21.com/Chirpy/internal/chatModel"
	"RyanDev-21.com/Chirpy/pkg/encoder"
	"RyanDev-21.com/Chirpy/pkg/middleware"
	"RyanDev-21.com/Chirpy/pkg/response"
	"github.com/google/uuid"
)

type chatHandler struct {
	chatService ChatService
}

func NewChatHandler(chatService ChatService) *chatHandler {
	return &chatHandler{
		chatService: chatService,
	}
}

func (h *chatHandler) ServeWs(w http.ResponseWriter, r *http.Request) {
	userID, err := middleware.GetContextKey(r.Context(), "user")
	if err != nil {
		log.Printf("failed to get the id")
		response.Error(w, 401, "unauthorized")
		return
	}
	conn, err := h.chatService.upgradeWebsocket(w, r)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		response.Error(w, 500, "cannot switch to websocket")
		return
	}

	h.chatService.initWs(conn, *userID)
}

// maybe consider abstracting the gettting context key
// NOTE:maybe add validating the content of the message like limit or smth
func (h *chatHandler) SendMessage(w http.ResponseWriter, r *http.Request) {
	userID, err := middleware.GetContextKey(r.Context(), "user")
	if err != nil {
		log.Printf("failed to get the id")
		response.Error(w, 401, "unauthorized")
		return
	}
	payload := chatmodel.Message{}
	err = encoder.Decode(r, &payload)
	if err != nil {
		response.Error(w, 400, "bad request")
		return
	}
	msgID, err := h.chatService.sendMessage(r.Context(), *userID, &payload)
	if err != nil {
		if err == ErrNotSupportedTypeMsg {
			response.Error(w, 400, "bad request")
			return
		}
		if err == chatmodel.ErrNoClientFound {
			response.Error(w, 404, "client not found,consider connecting to ws")
			return
		}
		if err == chatmodel.ErrNotValidToID {
			response.Error(w, 400, "to id is not valid uuid")
			return
		}
		log.Printf("failed to send message #%s#", err)
		response.Error(w, 500, "something went wrong")
		return
	}
	response.JSON(w, 200, chatmodel.ResponseMessageID{
		MsgID: *msgID,
	})
}

func (h *chatHandler) GetMesagesForPrivateID(w http.ResponseWriter, r *http.Request) {
	userID, err := middleware.GetContextKey(r.Context(), "user")
	if err != nil {
		log.Printf("failed to get the id")
		response.Error(w, 401, "unauthorized")
		return
	}
	otherUserID := r.PathValue("otherUser_id") // this becomes userID for private
	if otherUserID == "" {
		response.Error(w, 400, "invalid request")
		return
	}
	othUserID, err := uuid.Parse(otherUserID)
	if err != nil {
		response.Error(w, 400, "invalid request")
		return
	}
	msgList, err := h.chatService.fetchMessagePrivate(r.Context(), *userID, othUserID)
	if err != nil {
		response.Error(w, 500, "something went wrong")
	}
	response.JSON(w, 200, *msgList)
}

func (h *chatHandler) GetMesagesForPublicID(w http.ResponseWriter, r *http.Request) {
	userID, err := middleware.GetContextKey(r.Context(), "user")
	if err != nil {
		log.Printf("failed to get the id")
		response.Error(w, 401, "unauthorized")
		return
	}
	chatIDstr := r.PathValue("group_id") // this becomes userID for private
	if chatIDstr == "" {
		response.Error(w, 400, "invalid request")
		return
	}
	chatID, err := uuid.Parse(chatIDstr)
	if err != nil {
		response.Error(w, 400, "invalid request")
		return
	}
	msgList, err := h.chatService.fetchMessagePublic(r.Context(), *userID, chatID)
	if err != nil {
		if err == chatmodel.ErrNotAuthorized {
			response.Error(w, 403, "forbidden")
			return
		}
		response.Error(w, 500, "something went wrong")
	}
	response.JSON(w, 200, *msgList)
}
