package groups

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"

	"RyanDev-21.com/Chirpy/internal/platform/httpx"
	"RyanDev-21.com/Chirpy/internal/platform/reqctx"
	"RyanDev-21.com/Chirpy/internal/users"
	"github.com/google/uuid"
)

type GroupHandler struct {
	groupService GroupService
}

func NewGroupHandler(groupService GroupService) *GroupHandler {
	return &GroupHandler{groupService: groupService}
}

func writeGroupError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFoundGroup), errors.Is(err, ErrNoAvatar):
		httpx.Error(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrAlreadyMember), errors.Is(err, ErrGroupFull):
		httpx.Error(w, http.StatusConflict, err.Error())
	case errors.Is(err, ErrNotMember), errors.Is(err, ErrInvalidGroupReq), errors.Is(err, httpx.ErrBadPathParam),
		errors.Is(err, ErrKickSelf), errors.Is(err, ErrAlreadyAdmin), errors.Is(err, ErrNothingToUpdate),
		errors.Is(err, ErrInvalidGroupInfo), errors.Is(err, ErrBadRole), errors.Is(err, ErrSelfRole), errors.Is(err, ErrBadSearch):
		httpx.Error(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, users.ErrBadImage):
		httpx.Error(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, users.ErrImageTooLarge):
		httpx.Error(w, http.StatusRequestEntityTooLarge, err.Error())
	case errors.Is(err, ErrInvalidInviteCode):
		// same answer as an unknown code: don't help anyone probe the format
		httpx.Error(w, http.StatusNotFound, "invalid invite code")
	case errors.Is(err, ErrNoPermission), errors.Is(err, ErrNotFriend), errors.Is(err, ErrCantRemoveAdmin):
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
	// an admin may name who takes over: ?successor={user_id}
	var successor uuid.UUID
	if v := r.URL.Query().Get("successor"); v != "" {
		if successor, err = uuid.Parse(v); err != nil {
			httpx.Error(w, http.StatusBadRequest, "successor must be a user id")
			return
		}
	}
	if err := h.groupService.LeaveGroup(r.Context(), groupID, userID, successor); err != nil {
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

type inviteRequest struct {
	Code string `json:"code"`
}

// JoinByInvite handles POST /api/chats/groups/join {"code":"K7M9-QX2P"}.
func (h *GroupHandler) JoinByInvite(w http.ResponseWriter, r *http.Request) {
	userID, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req inviteRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid parameters")
		return
	}
	id, err := h.groupService.JoinByInvite(r.Context(), userID, req.Code)
	if err != nil {
		if errors.Is(err, ErrNotFoundGroup) {
			err = ErrInvalidInviteCode
		}
		writeGroupError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, GroupInfo{ChatID: id})
}

// PreviewInvite handles GET /api/chats/groups/invites/{code}.
func (h *GroupHandler) PreviewInvite(w http.ResponseWriter, r *http.Request) {
	p, err := h.groupService.PreviewInvite(r.Context(), r.PathValue("code"))
	if err != nil {
		if errors.Is(err, ErrNotFoundGroup) {
			err = ErrInvalidInviteCode
		}
		writeGroupError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, p)
}

// InviteCode handles GET /api/chats/groups/{group_id}/invite (members).
func (h *GroupHandler) InviteCode(w http.ResponseWriter, r *http.Request) {
	h.invite(w, r, h.groupService.InviteCode)
}

// RegenerateInvite handles POST /api/chats/groups/{group_id}/invite (leader).
func (h *GroupHandler) RegenerateInvite(w http.ResponseWriter, r *http.Request) {
	h.invite(w, r, h.groupService.RegenerateInvite)
}

func (h *GroupHandler) invite(w http.ResponseWriter, r *http.Request, fn func(context.Context, uuid.UUID, uuid.UUID) (string, error)) {
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
	code, err := fn(r.Context(), groupID, userID)
	if err != nil {
		writeGroupError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, InviteCode{Code: code})
}

// groupAction reads the caller and the group id.
func groupAction(w http.ResponseWriter, r *http.Request) (groupID, me uuid.UUID, ok bool) {
	groupID, err := httpx.PathUUID(r, "group_id")
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid group id")
		return groupID, me, false
	}
	me, err = reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return groupID, me, false
	}
	return groupID, me, true
}

// UpdateGroup handles PATCH /api/chats/groups/{group_id}
// {"group_name"?, "description"?}.
func (h *GroupHandler) UpdateGroup(w http.ResponseWriter, r *http.Request) {
	groupID, me, ok := groupAction(w, r)
	if !ok {
		return
	}
	var req updateGroupRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid parameters")
		return
	}
	g, err := h.groupService.Update(r.Context(), groupID, me, req)
	if err != nil {
		writeGroupError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, g)
}

// DeleteGroup handles DELETE /api/chats/groups/{group_id}.
func (h *GroupHandler) DeleteGroup(w http.ResponseWriter, r *http.Request) {
	groupID, me, ok := groupAction(w, r)
	if !ok {
		return
	}
	if err := h.groupService.Delete(r.Context(), groupID, me); err != nil {
		writeGroupError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// KickMember handles DELETE /api/chats/groups/{group_id}/members/{user_id}.
func (h *GroupHandler) KickMember(w http.ResponseWriter, r *http.Request) {
	groupID, me, ok := groupAction(w, r)
	if !ok {
		return
	}
	target, err := httpx.PathUUID(r, "user_id")
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid user id")
		return
	}
	if err := h.groupService.Kick(r.Context(), groupID, me, target); err != nil {
		writeGroupError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// TransferAdmin handles PUT /api/chats/groups/{group_id}/admin {"user_id"}.
func (h *GroupHandler) TransferAdmin(w http.ResponseWriter, r *http.Request) {
	groupID, me, ok := groupAction(w, r)
	if !ok {
		return
	}
	var body struct {
		UserID uuid.UUID `json:"user_id"`
	}
	if err := httpx.Decode(w, r, &body); err != nil || body.UserID == uuid.Nil {
		httpx.Error(w, http.StatusBadRequest, "user_id is required")
		return
	}
	if err := h.groupService.TransferAdmin(r.Context(), groupID, me, body.UserID); err != nil {
		writeGroupError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// AddMember handles POST /api/chats/groups/{group_id}/members {"user_id"}:
// the leader adds one of their friends.
func (h *GroupHandler) AddMember(w http.ResponseWriter, r *http.Request) {
	groupID, me, ok := groupAction(w, r)
	if !ok {
		return
	}
	var body struct {
		UserID uuid.UUID `json:"user_id"`
	}
	if err := httpx.Decode(w, r, &body); err != nil || body.UserID == uuid.Nil {
		httpx.Error(w, http.StatusBadRequest, "user_id is required")
		return
	}
	if err := h.groupService.AddFriend(r.Context(), groupID, me, body.UserID); err != nil {
		writeGroupError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

// UploadAvatar handles PUT /api/chats/groups/{group_id}/avatar (leader);
// the body is the image (JPEG, PNG or WebP, at most 5 MB).
func (h *GroupHandler) UploadAvatar(w http.ResponseWriter, r *http.Request) {
	groupID, me, ok := groupAction(w, r)
	if !ok {
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, users.MaxAvatarUpload))
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		httpx.Error(w, http.StatusRequestEntityTooLarge, users.ErrImageTooLarge.Error())
		return
	case err != nil:
		httpx.Error(w, http.StatusBadRequest, "could not read the upload")
		return
	}
	url, err := h.groupService.SetAvatar(r.Context(), groupID, me, raw)
	if err != nil {
		writeGroupError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]*string{"avatar_url": url})
}

// DeleteAvatar handles DELETE /api/chats/groups/{group_id}/avatar (leader).
func (h *GroupHandler) DeleteAvatar(w http.ResponseWriter, r *http.Request) {
	groupID, me, ok := groupAction(w, r)
	if !ok {
		return
	}
	if err := h.groupService.DeleteAvatar(r.Context(), groupID, me); err != nil {
		writeGroupError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Avatar handles GET /api/chats/groups/{group_id}/avatar (members): the JPEG.
func (h *GroupHandler) Avatar(w http.ResponseWriter, r *http.Request) {
	groupID, me, ok := groupAction(w, r)
	if !ok {
		return
	}
	img, at, err := h.groupService.Avatar(r.Context(), groupID, me)
	if err != nil {
		writeGroupError(w, r, err)
		return
	}
	etag := `"` + strconv.FormatInt(at.UnixMilli(), 10) + `"`
	w.Header().Set("ETag", etag)
	// versioned URL; private because only members may fetch it
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	_, _ = w.Write(img)
}

// SetRole handles PUT /api/chats/groups/{group_id}/members/{user_id}/role
// {"role":"moderator"|"member","permissions":[...]} (admin only).
func (h *GroupHandler) SetRole(w http.ResponseWriter, r *http.Request) {
	groupID, me, ok := groupAction(w, r)
	if !ok {
		return
	}
	target, err := httpx.PathUUID(r, "user_id")
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid user id")
		return
	}
	var req roleRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, ErrBadRole.Error())
		return
	}
	if err := h.groupService.SetRole(r.Context(), groupID, me, target, req); err != nil {
		writeGroupError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// JoinPublic handles POST /api/chats/groups/{group_id}/join (public groups).
func (h *GroupHandler) JoinPublic(w http.ResponseWriter, r *http.Request) {
	groupID, me, ok := groupAction(w, r)
	if !ok {
		return
	}
	if err := h.groupService.JoinPublic(r.Context(), groupID, me); err != nil {
		writeGroupError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, GroupInfo{ChatID: groupID})
}

// Search handles GET /api/chats/groups/search?q= (public groups by name).
func (h *GroupHandler) Search(w http.ResponseWriter, r *http.Request) {
	me, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	list, err := h.groupService.Search(r.Context(), me, r.URL.Query().Get("q"))
	if err != nil {
		writeGroupError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string][]GroupPreview{"groups": list})
}
