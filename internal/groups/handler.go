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

func writeGroupError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFoundGroup):
		httpx.Error(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrDuplicateName), errors.Is(err, ErrAlreadyMember), errors.Is(err, ErrGroupFull):
		httpx.Error(w, http.StatusConflict, err.Error())
	case errors.Is(err, ErrNotMember), errors.Is(err, ErrInvalidGroupReq):
		httpx.Error(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrLeaderCannotLeave):
		httpx.Error(w, http.StatusForbidden, err.Error())
	default:
		httpx.Error(w, http.StatusInternalServerError, "something went wrong")
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
	res, err := h.groupService.createGroup(r.Context(), creatorID, &createGroupStruct{
		GroupName:   params.GroupName,
		Description: params.Description,
		MaxMems:     params.MaxMems,
	})
	if err != nil {
		writeGroupError(w, err)
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
	if err := h.groupService.joinGroup(r.Context(), groupID, userID); err != nil {
		writeGroupError(w, err)
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
	if err := h.groupService.leaveGroup(r.Context(), groupID, userID); err != nil {
		writeGroupError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
