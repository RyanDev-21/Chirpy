package chat

import (
	"errors"
	"log/slog"
	"net/http"

	"RyanDev-21.com/Chirpy/internal/platform/httpx"
	"RyanDev-21.com/Chirpy/internal/platform/reqctx"
	"RyanDev-21.com/Chirpy/internal/ws"
)

type ChatHandler struct {
	chatService ChatService
	logger      *slog.Logger
}

func NewChatHandler(chatService ChatService, logger *slog.Logger) *ChatHandler {
	return &ChatHandler{
		chatService: chatService,
		logger:      logger,
	}
}

func (h *ChatHandler) ServeWs(w http.ResponseWriter, r *http.Request) {
	userID, err := reqctx.User(r.Context())
	if err != nil {
		h.logger.Error("failed to get user from context")
		httpx.Error(w, 401, "unauthorized")
		return
	}

	conn, err := h.chatService.upgradeWebsocket(w, r)
	if err != nil {
		h.logger.Error("websocket upgrade failed", "err", err)
		httpx.Error(w, 401, "unauthorized")
		return
	}

	h.chatService.initWs(conn, userID)
}

func (h *ChatHandler) GetMesagesForPrivateID(w http.ResponseWriter, r *http.Request) {
	userID, err := reqctx.User(r.Context())
	if err != nil {
		h.logger.Error("failed to get user from context")
		httpx.Error(w, 401, "unauthorized")
		return
	}
	toID, err := httpx.PathUUID(r, "otherUser_id")
	if err != nil {
		h.logger.Error("failed to get toID from path", "err", err)
		httpx.Error(w, 400, "invalid id")
		return

	}
	sinceStr := r.URL.Query().Get("since")

	msgList, err := h.chatService.fetchMessagePrivate(r.Context(), userID, toID, sinceStr)
	if err != nil {
		h.logger.Error("failed to fetch private messages", "err", err)
		httpx.Error(w, 500, "internal server error")
		return
	}
	httpx.JSON(w, 200, msgList)
}

func (h *ChatHandler) GetMessagesForPublicID(w http.ResponseWriter, r *http.Request) {
	userID, err := reqctx.User(r.Context())
	if err != nil {
		h.logger.Error("failed to get user from context")
		httpx.Error(w, 401, "unauthorized")
		return
	}
	toID, err := httpx.PathUUID(r, "group_id")
	if err != nil {
		h.logger.Error("failed to get groupID from path", "err", err)
		httpx.Error(w, 400, "invalid id")
		return
	}

	msgList, err := h.chatService.fetchMessagePublic(r.Context(), userID, toID)
	if err != nil {
		if errors.Is(err, ErrNotAuthorized) {
			httpx.Error(w, 403, "not a member of this group")
			return
		}
		h.logger.Error("failed to fetch public messages", "err", err)
		httpx.Error(w, 500, "internal server error")
		return
	}
	httpx.JSON(w, 200, msgList)
}

func (h *ChatHandler) SendMessage(w http.ResponseWriter, r *http.Request) {
	userID, err := reqctx.User(r.Context())
	if err != nil {
		h.logger.Error("failed to get user from context")
		httpx.Error(w, 401, "unauthorized")
		return
	}
	payload := &InCommingMessage{}
	err = httpx.Decode(w, r, payload)
	if err != nil {
		h.logger.Error("failed to decode message payload", "err", err)
		httpx.Error(w, 400, "invalid parameters")
		return
	}
	msgID, err := h.chatService.sendMessage(r.Context(), userID, payload)
	if err != nil {
		switch {
		case errors.Is(err, ws.ErrNotConnectedToWs):
			httpx.Error(w, 409, "connect to ws first to send message")
			return
		case errors.Is(err, ErrNotAuthorized):
			httpx.Error(w, 403, "not a member of this group")
			return
		case errors.Is(err, ErrNotValidToID), errors.Is(err, ErrNotValidParentID), errors.Is(err, ErrNotSupportedTypeMsg):
			httpx.Error(w, 400, err.Error())
			return
		}
		h.logger.Error("failed to send message", "err", err)
		httpx.Error(w, 500, "internal server error")
		return
	}
	httpx.JSON(w, 200, ResponseMessageID{MsgID: *msgID})
}

func (h *ChatHandler) UpdateSeen(w http.ResponseWriter, r *http.Request) {
	userID, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, 401, "unauthorized")
		return
	}
	toID, err := httpx.PathUUID(r, "otherUser_id")
	if err != nil {
		httpx.Error(w, 400, "invalid request")
		return
	}
	//there might be better one than this
	//i am too tired to rethink
	body := &PayloadForSeen{}
	err = httpx.Decode(w, r, body)
	if err != nil {
		h.logger.Error("failed to decode message payload", "err", err)
		httpx.Error(w, 400, "invalid parameters")
		return
	}
	payload := &ws.InCommingEventForSeen{
		ToID:  toID.String(),
		MsgID: body.MsgID,
	}
	err = h.chatService.updateLastSeen(r.Context(), userID, payload)
	if err != nil {
		if err == ErrNotValidUUID {
			httpx.Error(w, 400, "invalid request")
			return
		}
		httpx.Error(w, 500, "something went wrong")
		return
	}
	w.WriteHeader(http.StatusCreated)
}
