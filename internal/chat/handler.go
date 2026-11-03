package chat

import (
	"errors"
	"log/slog"
	"net/http"

	"RyanDev-21.com/Chirpy/internal/platform/httpx"
	"RyanDev-21.com/Chirpy/internal/platform/reqctx"
	"RyanDev-21.com/Chirpy/internal/ws"
	"github.com/gorilla/websocket"
)

type ChatHandler struct {
	svc      ChatService
	hub      *ws.Hub
	upgrader *websocket.Upgrader
	logger   *slog.Logger
}

func NewChatHandler(svc ChatService, hub *ws.Hub, upgrader *websocket.Upgrader, logger *slog.Logger) *ChatHandler {
	return &ChatHandler{svc: svc, hub: hub, upgrader: upgrader, logger: logger}
}

func (h *ChatHandler) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotAuthorized), errors.Is(err, ErrNotFriends):
		httpx.Error(w, http.StatusForbidden, err.Error())
	case errors.Is(err, ErrNotValidToID), errors.Is(err, ErrNotValidParentID), errors.Is(err, ErrNotSupportedTypeMsg),
		errors.Is(err, ErrEmptyMessage), errors.Is(err, ErrNotValidTimeFmt), errors.Is(err, ErrNotValidUUID),
		errors.Is(err, ErrMessageNotInChat):
		httpx.Error(w, http.StatusBadRequest, err.Error())
	default:
		httpx.ServerError(w, r, err)
	}
}

func (h *ChatHandler) ServeWs(w http.ResponseWriter, r *http.Request) {
	userID, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if err := h.hub.Serve(h.upgrader, w, r, userID); err != nil {
		h.logger.WarnContext(r.Context(), "websocket upgrade failed", "error", err)
	}
}

// GetMesagesForPrivateID handles GET /api/chat/{otherUser_id}/messages[?since=RFC3339].
func (h *ChatHandler) GetMesagesForPrivateID(w http.ResponseWriter, r *http.Request) {
	me, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	other, err := httpx.PathUUID(r, "otherUser_id")
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid id")
		return
	}
	msgs, err := h.svc.PrivateMessages(r.Context(), me, other, r.URL.Query().Get("since"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, MessageListRes{ChatID: PrivateChatID(me, other), MsgList: msgs})
}

// GetMessagesForPublicID handles GET /api/chat/groups/{group_id}/messages.
func (h *ChatHandler) GetMessagesForPublicID(w http.ResponseWriter, r *http.Request) {
	me, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	groupID, err := httpx.PathUUID(r, "group_id")
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid id")
		return
	}
	msgs, err := h.svc.GroupMessages(r.Context(), me, groupID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, MessageListRes{ChatID: groupID.String(), MsgList: msgs})
}

// SendMessage handles POST /api/chats/message.
func (h *ChatHandler) SendMessage(w http.ResponseWriter, r *http.Request) {
	me, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var in InCommingMessage
	if err := httpx.Decode(w, r, &in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid parameters")
		return
	}
	msgID, err := h.svc.SendMessage(r.Context(), me, in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, ResponseMessageID{MsgID: msgID})
}

// UpdateSeen handles POST /api/chat/{otherUser_id}/messages {"msg_id": "..."}.
func (h *ChatHandler) UpdateSeen(w http.ResponseWriter, r *http.Request) {
	me, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	other, err := httpx.PathUUID(r, "otherUser_id")
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid id")
		return
	}
	var body PayloadForSeen
	if err := httpx.Decode(w, r, &body); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid parameters")
		return
	}
	if err := h.svc.MarkSeen(r.Context(), me, other, body.MsgID); err != nil {
		h.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}
