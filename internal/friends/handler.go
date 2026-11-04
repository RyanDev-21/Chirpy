package friends

import (
	"errors"
	"net/http"

	"RyanDev-21.com/Chirpy/internal/platform/httpx"
	"RyanDev-21.com/Chirpy/internal/platform/reqctx"
	"github.com/google/uuid"
)

type Handler struct {
	svc Service
}

func NewHandler(svc Service) *Handler { return &Handler{svc: svc} }

func writeErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrSelfRequest), errors.Is(err, ErrSelfBlock):
		httpx.Error(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrUnknownUser), errors.Is(err, ErrNotFound), errors.Is(err, ErrNotFriends):
		httpx.Error(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrRequestExists), errors.Is(err, ErrAlreadyFriends), errors.Is(err, ErrYouBlocked):
		httpx.Error(w, http.StatusConflict, err.Error())
	default:
		httpx.ServerError(w, r, err)
	}
}

// Send handles POST /api/friends/requests {"to_id": "..."}.
func (h *Handler) Send(w http.ResponseWriter, r *http.Request) {
	me, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var p StatusFriendParameters
	if err := httpx.Decode(w, r, &p); err != nil || p.ToID == uuid.Nil {
		httpx.Error(w, http.StatusBadRequest, "to_id is required")
		return
	}
	reqID, err := h.svc.Send(r.Context(), me, p.ToID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, ResponseForAddFriend{ReqID: reqID})
}

// Update handles PUT /api/friends/requests/{request_id} {"status": "confirm"|"cancel"}
// for requests the caller received.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	me, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	reqID, err := httpx.PathUUID(r, "request_id")
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid request id")
		return
	}
	var p StatusFriendParameters
	if err := httpx.Decode(w, r, &p); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid parameters")
		return
	}
	switch p.Status {
	case "confirm":
		err = h.svc.Accept(r.Context(), me, reqID)
	case "cancel":
		err = h.svc.Deny(r.Context(), me, reqID)
	default:
		httpx.Error(w, http.StatusBadRequest, `status must be "confirm" or "cancel"`)
		return
	}
	if err != nil {
		writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

// Cancel handles DELETE /api/friends/requests/{request_id} for requests the
// caller sent.
func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	me, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	reqID, err := httpx.PathUUID(r, "request_id")
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid request id")
		return
	}
	if err := h.svc.Cancel(r.Context(), me, reqID); err != nil {
		writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Requests handles GET /api/friends/requests.
func (h *Handler) Requests(w http.ResponseWriter, r *http.Request) {
	me, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	snap, err := h.svc.Relationships(r.Context(), me)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, ResponseReqList{PendingIDsList: snap.Incoming, RequestIDsList: snap.Outgoing})
}

// List handles GET /api/friends.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	me, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	snap, err := h.svc.Relationships(r.Context(), me)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, ResponseFriListStruct{FriendList: snap.Friends})
}

// Unfriend handles DELETE /api/friends/{user_id}.
func (h *Handler) Unfriend(w http.ResponseWriter, r *http.Request) {
	me, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	other, err := httpx.PathUUID(r, "user_id")
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid user id")
		return
	}
	if err := h.svc.Unfriend(r.Context(), me, other); err != nil {
		writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Block handles POST /api/users/blocks {"user_id": "..."}.
func (h *Handler) Block(w http.ResponseWriter, r *http.Request) {
	me, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var p struct {
		UserID uuid.UUID `json:"user_id"`
	}
	if err := httpx.Decode(w, r, &p); err != nil || p.UserID == uuid.Nil {
		httpx.Error(w, http.StatusBadRequest, "user_id is required")
		return
	}
	if err := h.svc.Block(r.Context(), me, p.UserID); err != nil {
		writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Unblock handles DELETE /api/users/blocks/{user_id}.
func (h *Handler) Unblock(w http.ResponseWriter, r *http.Request) {
	me, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	other, err := httpx.PathUUID(r, "user_id")
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid user id")
		return
	}
	if err := h.svc.Unblock(r.Context(), me, other); err != nil {
		writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Blocked handles GET /api/users/blocks.
func (h *Handler) Blocked(w http.ResponseWriter, r *http.Request) {
	me, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	list, err := h.svc.Blocked(r.Context(), me)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string][]BlockedUser{"blocked": list})
}
