package chat

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"RyanDev-21.com/Chirpy/internal/media"
	"RyanDev-21.com/Chirpy/internal/platform/httpx"
	"RyanDev-21.com/Chirpy/internal/platform/reqctx"
	"RyanDev-21.com/Chirpy/internal/ws"
	"github.com/google/uuid"
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
	case errors.Is(err, media.ErrDisabled):
		httpx.Error(w, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, media.ErrBadAttachment):
		httpx.Error(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrMessageNotFound):
		httpx.Error(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrNotAuthorized), errors.Is(err, ErrNotFriends), errors.Is(err, ErrNotYourMessage),
		errors.Is(err, ErrEditWindowClosed):
		httpx.Error(w, http.StatusForbidden, err.Error())
	case errors.Is(err, ErrNotValidToID), errors.Is(err, ErrNotValidParentID), errors.Is(err, ErrNotSupportedTypeMsg),
		errors.Is(err, ErrEmptyMessage), errors.Is(err, ErrNotValidTimeFmt), errors.Is(err, ErrNotValidUUID),
		errors.Is(err, ErrMessageNotInChat), errors.Is(err, ErrBadCursor), errors.Is(err, ErrSinceAndBefore),
		errors.Is(err, ErrSinceNotSupported), errors.Is(err, ErrNotAnEmoji), errors.Is(err, ErrNotGroupMessage),
		errors.Is(err, ErrGroupTooBig):
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
	msgs, err := h.svc.PrivateMessages(r.Context(), me, other, historyQuery(r))
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
	msgs, err := h.svc.GroupMessages(r.Context(), me, groupID, historyQuery(r))
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

// Forward handles POST /api/chats/forward {"message_id","to":[{"type","id"}]}.
func (h *ChatHandler) Forward(w http.ResponseWriter, r *http.Request) {
	me, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var b struct {
		MessageID uuid.UUID       `json:"message_id"`
		To        []ForwardTarget `json:"to"`
	}
	if err := httpx.Decode(w, r, &b); err != nil || b.MessageID == uuid.Nil {
		httpx.Error(w, http.StatusBadRequest, "message_id and to are required")
		return
	}
	res, err := h.svc.Forward(r.Context(), me, b.MessageID, b.To)
	if errors.Is(err, ErrBadForward) {
		httpx.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string][]ForwardResult{"results": res})
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

// historyQuery reads ?since=, ?before=<message id> and ?limit= (1-100).
func historyQuery(r *http.Request) HistoryQuery {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	return HistoryQuery{Since: q.Get("since"), Before: q.Get("before"), Limit: limit}
}

// Conversations handles GET /api/chats: every chat with its last message
// and unread count, most recent first.
func (h *ChatHandler) Conversations(w http.ResponseWriter, r *http.Request) {
	me, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	list, err := h.svc.Conversations(r.Context(), me)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string][]Conversation{"conversations": list})
}

// MarkGroupRead handles POST /api/chat/groups/{group_id}/messages {"msg_id"}.
func (h *ChatHandler) MarkGroupRead(w http.ResponseWriter, r *http.Request) {
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
	var body PayloadForSeen
	if err := httpx.Decode(w, r, &body); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid parameters")
		return
	}
	if err := h.svc.MarkGroupRead(r.Context(), me, groupID, body.MsgID); err != nil {
		h.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (h *ChatHandler) messageTarget(w http.ResponseWriter, r *http.Request) (me, msgID uuid.UUID, ok bool) {
	me, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return me, msgID, false
	}
	msgID, err = httpx.PathUUID(r, "msg_id")
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid message id")
		return me, msgID, false
	}
	return me, msgID, true
}

// React handles PUT /api/chat/messages/{msg_id}/reaction {"emoji":"👍"}.
func (h *ChatHandler) React(w http.ResponseWriter, r *http.Request) {
	me, msgID, ok := h.messageTarget(w, r)
	if !ok {
		return
	}
	var body struct {
		Emoji string `json:"emoji"`
	}
	if err := httpx.Decode(w, r, &body); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid parameters")
		return
	}
	if err := h.svc.React(r.Context(), me, msgID, body.Emoji); err != nil {
		h.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Unreact handles DELETE /api/chat/messages/{msg_id}/reaction.
func (h *ChatHandler) Unreact(w http.ResponseWriter, r *http.Request) {
	me, msgID, ok := h.messageTarget(w, r)
	if !ok {
		return
	}
	if err := h.svc.Unreact(r.Context(), me, msgID); err != nil {
		h.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Reactors handles GET /api/chat/messages/{msg_id}/reactions.
func (h *ChatHandler) Reactors(w http.ResponseWriter, r *http.Request) {
	me, msgID, ok := h.messageTarget(w, r)
	if !ok {
		return
	}
	list, err := h.svc.Reactors(r.Context(), me, msgID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string][]Reactor{"reactions": list})
}

// ReadBy handles GET /api/chat/messages/{msg_id}/read-by (own group
// messages, groups of up to 50).
func (h *ChatHandler) ReadBy(w http.ResponseWriter, r *http.Request) {
	me, msgID, ok := h.messageTarget(w, r)
	if !ok {
		return
	}
	list, err := h.svc.ReadBy(r.Context(), me, msgID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string][]Reader{"read_by": list})
}

// EditMessage handles PATCH /api/chat/messages/{msg_id} {"msg":"new text"}.
func (h *ChatHandler) EditMessage(w http.ResponseWriter, r *http.Request) {
	me, msgID, ok := h.messageTarget(w, r)
	if !ok {
		return
	}
	var body struct {
		Content string `json:"msg"`
	}
	if err := httpx.Decode(w, r, &body); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid parameters")
		return
	}
	res, err := h.svc.EditMessage(r.Context(), me, msgID, body.Content)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, res)
}

// DeleteMessage handles DELETE /api/chat/messages/{msg_id}.
func (h *ChatHandler) DeleteMessage(w http.ResponseWriter, r *http.Request) {
	me, msgID, ok := h.messageTarget(w, r)
	if !ok {
		return
	}
	if err := h.svc.DeleteMessage(r.Context(), me, msgID); err != nil {
		h.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
