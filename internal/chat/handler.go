package chat

import (
	"log/slog"
	"net/http"

	chatmodel "RyanDev-21.com/Chirpy/internal/chatModel"
	"RyanDev-21.com/Chirpy/pkg/encoder"
	"RyanDev-21.com/Chirpy/pkg/middleware"
	"RyanDev-21.com/Chirpy/pkg/response"
)

type chatHandler struct {
	chatService ChatService
	logger      *slog.Logger
}

func NewChatHandler(chatService ChatService, logger *slog.Logger) *chatHandler {
	return &chatHandler{
		chatService: chatService,
		logger:      logger,
	}
}

func (h *chatHandler) ServeWs(w http.ResponseWriter, r *http.Request) {
	userID, err := middleware.GetContextKey(r.Context(), "user")
	if err != nil {
		h.logger.Error("failed to get user from context")
		response.Error(w, 401, "unauthorized")
		return
	}

	conn, err := h.chatService.upgradeWebsocket(w, r)
	if err != nil {
		h.logger.Error("websocket upgrade failed", "err", err)
		response.Error(w, 401, "unauthorized")
		return
	}

	h.chatService.initWs(conn, *userID)
}

func (h *chatHandler) GetMesagesForPrivateID(w http.ResponseWriter, r *http.Request) {
	userID, err := middleware.GetContextKey(r.Context(), "user")
	if err != nil {
		h.logger.Error("failed to get user from context")
		response.Error(w, 401, "unauthorized")
		return
	}
	toID, err := middleware.GetPathValue("id", r)
	if err != nil {
		h.logger.Error("failed to get toID from path", "err", err)
		response.Error(w, 400, "invalid id")
		return
	}

	msgList, err := h.chatService.fetchMessagePrivate(r.Context(), *userID, *toID)
	if err != nil {
		h.logger.Error("failed to fetch private messages", "err", err)
		response.Error(w, 500, "internal server error")
		return
	}
	response.JSON(w, 200, msgList)
}

func (h *chatHandler) GetMessagesForPublicID(w http.ResponseWriter, r *http.Request) {
	userID, err := middleware.GetContextKey(r.Context(), "user")
	if err != nil {
		h.logger.Error("failed to get user from context")
		response.Error(w, 401, "unauthorized")
		return
	}
	toID, err := middleware.GetPathValue("id", r)
	if err != nil {
		h.logger.Error("failed to get groupID from path", "err", err)
		response.Error(w, 400, "invalid id")
		return
	}

	msgList, err := h.chatService.fetchMessagePublic(r.Context(), *userID, *toID)
	if err != nil {
		h.logger.Error("failed to fetch public messages", "err", err)
		response.Error(w, 500, "internal server error")
		return
	}
	response.JSON(w, 200, msgList)
}

func (h *chatHandler) SendMessage(w http.ResponseWriter, r *http.Request) {
	userID, err := middleware.GetContextKey(r.Context(), "user")
	if err != nil {
		h.logger.Error("failed to get user from context")
		response.Error(w, 401, "unauthorized")
		return
	}
	payload := &chatmodel.Message{}
	err = encoder.Decode(r, payload)
	if err != nil {
		h.logger.Error("failed to decode message payload", "err", err)
		response.Error(w, 400, "invalid parameters")
		return
	}
	msgID, err := h.chatService.sendMessage(r.Context(), *userID, payload)
	if err != nil {
		h.logger.Error("failed to send message", "err", err)
		response.Error(w, 500, "internal server error")
		return
	}
	response.JSON(w, 200, chatmodel.ResponseMessageID{MsgID: *msgID})
}
