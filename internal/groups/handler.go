package groups

import (
	"errors"
	"net/http"

	"RyanDev-21.com/Chirpy/internal/platform/httpx"
	"RyanDev-21.com/Chirpy/internal/platform/reqctx"
)

type GroupHandler struct {
	groupService GroupService
}

func NewGroupHandler(groupService GroupService) *GroupHandler {
	return &GroupHandler{groupService: groupService}
}

func writeGroupError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFoundGroup):
		httpx.Error(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrDuplicateName), errors.Is(err, ErrAlreadyMember), errors.Is(err, ErrGroupFull):
		httpx.Error(w, http.StatusConflict, err.Error())
	case errors.Is(err, ErrNotMember), errors.Is(err, ErrInvalidGroupReq), errors.Is(err, httpx.ErrBadPathParam):
		httpx.Error(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrLeaderCannotLeave):
		httpx.Error(w, http.StatusForbidden, err.Error())
	default:
		httpx.ServerError(w, r, err)
	}
}

func (h *GroupHandler) CreateGroup(w http.ResponseWriter, r *http.Request) {
	var params createGroupRequest
	if err := httpx.Decode(w, r, &params); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid parameters")
		return
	}
	creatorID, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	res, err := h.groupService.CreateGroup(r.Context(), creatorID, params)
	if err != nil {
		writeGroupError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, res)
}

func (h *GroupHandler) JoinGroup(w http.ResponseWriter, r *http.Request) {
	groupID, err := httpx.PathUUID(r, "group_id")
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid group id")
		return
	}
	userID, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if err := h.groupService.JoinGroup(r.Context(), groupID, userID); err != nil {
		writeGroupError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (h *GroupHandler) LeaveGroup(w http.ResponseWriter, r *http.Request) {
	groupID, err := httpx.PathUUID(r, "group_id")
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid group id")
		return
	}
	userID, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if err := h.groupService.LeaveGroup(r.Context(), groupID, userID); err != nil {
		writeGroupError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// MyGroups handles GET /api/chats/groups.
func (h *GroupHandler) MyGroups(w http.ResponseWriter, r *http.Request) {
	userID, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	list, err := h.groupService.MyGroups(r.Context(), userID)
	if err != nil {
		writeGroupError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string][]GroupSummary{"groups": list})
}

// Detail handles GET /api/chats/groups/{group_id}.
func (h *GroupHandler) Detail(w http.ResponseWriter, r *http.Request) {
	groupID, err := httpx.PathUUID(r, "group_id")
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid group id")
		return
	}
	userID, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	d, err := h.groupService.GroupDetail(r.Context(), groupID, userID)
	if err != nil {
		writeGroupError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, d)
}
