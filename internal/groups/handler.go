package groups

import (
	"errors"
	"net/http"

	"RyanDev-21.com/Chirpy/pkg/encoder"
	"RyanDev-21.com/Chirpy/pkg/middleware"
	"RyanDev-21.com/Chirpy/pkg/response"
)

type groupHandler struct {
	groupService GroupService
}

func NewGroupHandler(groupService GroupService) *groupHandler {
	return &groupHandler{groupService: groupService}
}

func writeGroupError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFoundGroup):
		response.Error(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrDuplicateName), errors.Is(err, ErrAlreadyMember), errors.Is(err, ErrGroupFull):
		response.Error(w, http.StatusConflict, err.Error())
	case errors.Is(err, ErrNotMember), errors.Is(err, ErrInvalidGroupReq):
		response.Error(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrLeaderCannotLeave):
		response.Error(w, http.StatusForbidden, err.Error())
	default:
		response.Error(w, http.StatusInternalServerError, "something went wrong")
	}
}

func (h *groupHandler) CreateGroup(w http.ResponseWriter, r *http.Request) {
	var params createGroupRequest
	if err := encoder.Decode(r, &params); err != nil {
		response.Error(w, http.StatusBadRequest, "invalid parameters")
		return
	}
	creatorID, err := middleware.GetContextKey(r.Context(), "user")
	if err != nil {
		response.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	res, err := h.groupService.createGroup(r.Context(), *creatorID, &createGroupStruct{
		GroupName:   params.GroupName,
		Description: params.Description,
		MaxMems:     params.MaxMems,
	})
	if err != nil {
		writeGroupError(w, err)
		return
	}
	response.JSON(w, http.StatusCreated, res)
}

func (h *groupHandler) JoinGroup(w http.ResponseWriter, r *http.Request) {
	groupID, err := middleware.GetPathValue("group_id", r)
	if err != nil {
		response.Error(w, http.StatusBadRequest, "invalid group id")
		return
	}
	userID, err := middleware.GetContextKey(r.Context(), "user")
	if err != nil {
		response.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if err := h.groupService.joinGroup(r.Context(), *groupID, *userID); err != nil {
		writeGroupError(w, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (h *groupHandler) LeaveGroup(w http.ResponseWriter, r *http.Request) {
	groupID, err := middleware.GetPathValue("group_id", r)
	if err != nil {
		response.Error(w, http.StatusBadRequest, "invalid group id")
		return
	}
	userID, err := middleware.GetContextKey(r.Context(), "user")
	if err != nil {
		response.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if err := h.groupService.leaveGroup(r.Context(), *groupID, *userID); err != nil {
		writeGroupError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
