package users

// NOTE::if have time,refactor the code and abstract the decode and encode

import (
	//"fmt"
	"encoding/json"
	"log"
	"net/http"
	"regexp"

	"RyanDev-21.com/Chirpy/internal/platform/httpx"
	"RyanDev-21.com/Chirpy/internal/platform/reqctx"
	"RyanDev-21.com/Chirpy/internal/platform/secure"
	"RyanDev-21.com/Chirpy/internal/ws"
	"github.com/google/uuid"
)

type UserHandler struct {
	userService UserService
}

func NewUserHandler(userService UserService) *UserHandler {
	return &UserHandler{
		userService: userService,
	}
}

func (h *UserHandler) Register(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	params := &DefaultUsersParameters{}
	err := decoder.Decode(params)
	if err != nil {
		httpx.Error(w, 400, "invalid params")
		return
	}
	if params.Email == "" || params.Name == "" || params.Password == "" {
		httpx.Error(w, 400, "all fields need to have value")
		return
	}

	if len(params.Password) < 8 {
		httpx.Error(w, 400, "password must be at least 8 characters")
		return
	}
	if !regexp.MustCompile(`[@$!%*?&]`).MatchString(params.Password) {
		httpx.Error(w, 400, "password must contain at least one special character (@$!%*?&)")
		return
	}

	user, err := h.userService.Register(r.Context(), params.Name, params.Email, params.Password)
	if err != nil {
		if err == DuplicateKeyErr {
			httpx.Error(w, 400, "the user already exists")
			return
		}
		if err == DuplicateNameKeyErr {
			httpx.Error(w, 400, "the user name already exists")
			return
		}
		log.Printf("internal error :#%s#", err)
		httpx.Error(w, 500, "something went wrong")
		return
	}
	httpx.JSON(w, 200, user)
}

// uses one of user services and then hanlde the http route just as the name suggest
// this one has to go into put users/one
func (h *UserHandler) UpdatePassword(w http.ResponseWriter, r *http.Request) {
	params := &PasswordUpdateStruct{}

	err := httpx.Decode(w, r, params)
	if err != nil {
		httpx.Error(w, 400, "invalid params")
		return
	}

	passwordRegex := regexp.MustCompile(`[@$!%*?&]`)
	if len(params.NewPass) < 8 || !passwordRegex.MatchString(params.NewPass) {
		httpx.Error(w, 400, "password must be at least 8 characters with at least one special character (@$!%*?&)")
		return
	}

	userID, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, 500, "internal server error")
		return
	}

	updatedUser, err := h.userService.UpdatePassword(r.Context(), userID, params.OldPass, params.NewPass)
	if err != nil {
		if err == NoUserFoundErr {
			httpx.Error(w, 404, "no user found error")
			return
		}
		if err == secure.ErrPassNotMatch {
			httpx.Error(w, 401, "unauthorized")
			return
		}
		if err == ErrNoRedFound || err == ErrReqExist {
			httpx.Error(w, 400, "invalid request")
			return
		}

		httpx.Error(w, 500, "Internal server error")
		return
	}
	httpx.JSON(w, 200, updatedUser)
}

// func (h *UserHandler) UpdateUserInfo(w http.ResponseWriter, r *http.Request) {
// 	userID, err := middleware.GetContextKey(r.Context())
// 	if err != nil {
// 		httpx.Error(w, 500, "internal server error")
// 		return
// 	}
// }

// can use the job for add friend
func (h *UserHandler) AddFriend(w http.ResponseWriter, r *http.Request) {
	payload := &StatusFriendParameters{}
	err := httpx.Decode(w, r, payload)
	if err != nil {
		httpx.Error(w, 400, "invalid parameters")
		return
	}
	if payload.ToID == uuid.Nil {
		httpx.Error(w, 400, "missing to_id")
		return
	}
	userID, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, 500, "internal server error")
		return
	}

	friReqID, err := h.userService.AddFriendSend(r.Context(), userID, payload.ToID, "pending")
	if err != nil {
		if err == ErrReqExist || err == ErrNotValidReq {
			httpx.Error(w, 400, "invalid request")
			return
		}
		if err == ws.ErrNotConnectedToWs {
			httpx.Error(w, 403, "not connected to ws")
			return
		}

		httpx.Error(w, 500, "internal server error")
		return
	}
	httpx.JSON(w, 200, ReesponseForAddFriend{
		ReqID: *friReqID,
	})
}

// refactor this later after you done this feature there is duplicate code
func (h *UserHandler) UpdateReq(w http.ResponseWriter, r *http.Request) {
	reqID, err := httpx.PathUUID(r, "request_id")
	if err != nil {
		log.Printf("failed to get request_id err:%v", err)
		httpx.Error(w, 400, "invalid request")
		return
	}
	payload := &StatusFriendParameters{}
	err = httpx.Decode(w, r, payload)
	if err != nil {
		httpx.Error(w, 400, "invalid parameters")
		return
	}
	userID, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, 500, "internal server error")
		return
	}
	switch payload.Status {
	case "confirm":
		err = h.userService.ConfirmFriendReq(r.Context(), userID, reqID, "confirm")
	case "cancel":
		err = h.userService.CancelFriReq(r.Context(), userID, reqID)

	default:
		httpx.Error(w, 400, "invalid request")
		return

	}
	if err != nil {
		if err == ws.ErrNoClientFound {
			httpx.Error(w, 400, "client is not connect to ws, consider connecting first")
			return
		}
		if err == ws.ErrNotConnectedToWs {
			httpx.Error(w, 403, "not connected to ws")
			return
		}
		if err == ErrNoRedFound {
			httpx.Error(w, 400, "invalid request")
			return
		}
		httpx.Error(w, 500, "internal server error")
		return
	}
	w.WriteHeader(201)
}

func (h *UserHandler) DeleteFriReq(w http.ResponseWriter, r *http.Request) {
	userID, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, 500, "internal server error")
		return
	}

	reqID, err := httpx.PathUUID(r, "request_id")
	if err != nil {
		httpx.Error(w, 400, "invalid request")
		return
	}

	err = h.userService.DeleteFriReq(r.Context(), userID, reqID)
	if err != nil {
		httpx.Error(w, 500, "internal server error")
		return
	}
	w.WriteHeader(204)
}

func (h *UserHandler) GetPendingList(w http.ResponseWriter, r *http.Request) {
	userID, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, 400, "invalid request")
		return
	}

	list, err := h.userService.GetPendingList(r.Context(), userID)
	if err != nil {
		httpx.Error(w, 500, "internal server error")
		return
	}

	httpx.JSON(w, 200, ResponseReqList{
		PendingIDsList: *list.PendingIDsList,
		RequestIDsList: *list.RequestIDsList,
	})
}

func (h *UserHandler) GetFriendList(w http.ResponseWriter, r *http.Request) {
	userID, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, 400, "invalid request")
		return
	}
	list, err := h.userService.GetFriendList(r.Context(), userID)
	if err != nil {
		httpx.Error(w, 500, "internal server error")
		return
	}

	httpx.JSON(w, 200, ResponseFriListStruct{
		FriendList: *list,
	})
}

func (h *UserHandler) SearchUser(w http.ResponseWriter, r *http.Request) {
	_, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, 400, "invalid request")
		return
	}
	searchName := r.URL.Query().Get("q")
	if searchName == "" {
		httpx.Error(w, 400, "search name is not valid")
		return
	}
	userList, err := h.userService.SearchUser(r.Context(), searchName)
	if err != nil {
		httpx.Error(w, 500, "internal server error")
		return
	}

	if userList == nil || len(*userList) == 0 {
		httpx.JSON(w, 200, FoundUserListRes{
			UserList: []User{},
		})
		return
	}

	httpx.JSON(w, 200, FoundUserListRes{
		UserList: *userList,
	})
}

func (h *UserHandler) SaveConfig(w http.ResponseWriter, r *http.Request) {
	payload := &ConfigList{}
	err := httpx.Decode(w, r, payload)
	if err != nil {
		httpx.Error(w, 400, "invalid parameters")
		return
	}
	userID, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, 400, "invalid request")
		return
	}

	err = h.userService.SaveConfig(r.Context(), userID, payload)
	if err != nil {
		if err == ErrNoRedFound {
			httpx.Error(w, 404, "not found ")
			return
		}
		httpx.Error(w, 500, "something went wrong")
		return
	}
	w.WriteHeader(201)
}

// right now this returns the whoel config list
func (h *UserHandler) GetConfig(w http.ResponseWriter, r *http.Request) {
	userID, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, 400, "invalid request")
		return
	}
	configList, err := h.userService.GetConfig(r.Context(), userID)
	if err != nil {
		httpx.Error(w, 500, "something went wrong")
		return
	}
	httpx.JSON(w, 200, ConfigList{
		List: configList.List,
	})
}
