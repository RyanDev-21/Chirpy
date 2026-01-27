package users

//NOTE::if have time,refactor the code and abstract the decode and encode

import (
	//"fmt"
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"RyanDev-21.com/Chirpy/pkg/auth"
	"RyanDev-21.com/Chirpy/pkg/encoder"
	"RyanDev-21.com/Chirpy/pkg/middleware"
	"RyanDev-21.com/Chirpy/pkg/response"
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
		response.Error(w, 400, "invalid params")
		return
	}
	if params.Email == "" || params.Name == "" || params.Password == "" {
		response.Error(w, 400, "all fields need to have value")
		return
	}
	user, err := h.userService.Register(r.Context(), params.Name, params.Email, params.Password)
	if err != nil {
		if err == DuplicateKeyErr {
			response.Error(w, 400, "the user already exists")
			return
		}
		if err == DuplicateNameKeyErr {
			response.Error(w, 400, "the user name already exists")
			return
		}
		log.Printf("internal error :#%s#", err)
		response.Error(w, 500, "something went wrong")
		return
	}
	response.JSON(w, 200, user)
}

// uses one of user services and then hanlde the http route just as the name suggest
// this one has to go into put users/one
func (h *UserHandler) UpdatePassword(w http.ResponseWriter, r *http.Request) {
	params := &PasswordUpdateStruct{}

	err := encoder.Decode(r, params)
	if err != nil {
		response.Error(w, 400, "invalid params")
		return
	}

	userID, err := middleware.GetUserContextKey(r.Context())
	if err != nil {
		response.Error(w, 500, "internal server error")
		return
	}

	updatedUser, err := h.userService.UpdatePassword(r.Context(), *userID, params.OldPass, params.NewPass)
	if err != nil {
		if err == NoUserFoundErr {
			response.Error(w, 404, "no user found error")
			return
		}
		if err == auth.ErrPassNotMatch {
			response.Error(w, 401, "unauthorized")
			return
		}

		response.Error(w, 500, "Internal server error")
		return
	}
	response.JSON(w, 200, updatedUser)

}

// func (h *UserHandler) UpdateUserInfo(w http.ResponseWriter, r *http.Request) {
// 	userID, err := middleware.GetUserContextKey(r.Context())
// 	if err != nil {
// 		response.Error(w, 500, "internal server error")
// 		return
// 	}
// }

// can use the job for add friend
func (h *UserHandler) AddFriend(w http.ResponseWriter, r *http.Request) {
	payload := &StatusFriendParameters{}
	err := encoder.Decode(r, payload)
	if err != nil {
		response.Error(w, 400, "invalid parameters")
		return
	}
	userID, err := middleware.GetUserContextKey(r.Context())
	if err != nil {
		response.Error(w, 500, "internal server error")
		return
	}
	friReqID, err := uuid.NewV7()
	if err != nil {
		log.Printf("failed to gen the friReqId something went wrong")
	}

	err = h.userService.AddFriendSend(r.Context(), *userID, payload.ToID, "pending", friReqID)
	if err != nil {
		response.Error(w, 500, "internal server error")
		return
	}
	w.WriteHeader(201)

}

// refactor this later after you done this feature there is duplicate code
func (h *UserHandler) UpdateReq(w http.ResponseWriter, r *http.Request) {
	reqID, err := middleware.GetPathValue("request_id", r)
	if err != nil {
		response.Error(w, 400, "invalid request")
		return
	}
	payload := &StatusFriendParameters{}
	err = encoder.Decode(r, payload)
	if err != nil {
		response.Error(w, 400, "invalid parameters")
		return
	}
	userID, err := middleware.GetUserContextKey(r.Context())
	if err != nil {
		response.Error(w, 500, "internal server error")
		return
	}
	switch payload.Status {
	case "confirm":
		err = h.userService.ConfirmFriendReq(r.Context(), *userID, *reqID, "confirm")
	case "cancel":
		err = h.userService.CancelFriReq(r.Context(), *userID, *reqID)

	default:
		err = errors.New("no supported status")

	}
	if err != nil {
		response.Error(w, 500, "internal server error")
		return
	}
	w.WriteHeader(201)
}

func (h *UserHandler) DeleteFriReq(w http.ResponseWriter, r *http.Request) {
	userID, err := middleware.GetUserContextKey(r.Context())
	if err != nil {
		response.Error(w, 500, "internal server error")
		return
	}

	reqID, err := middleware.GetPathValue("request_id", r)
	if err != nil {
		response.Error(w, 400, "invalid request")
		return
	}

	err = h.userService.DeleteFriReq(r.Context(), *userID, *reqID)
	if err != nil {
		response.Error(w, 500, "internal server error")
		return
	}
	w.WriteHeader(204)
}

func (h *UserHandler) GetPendingList(w http.ResponseWriter, r *http.Request) {
	userID, err := middleware.GetUserContextKey(r.Context())
	if err != nil {
		response.Error(w, 400, "invalid request")
		return
	}

	list, err := h.userService.GetPendingList(r.Context(), *userID)
	if err != nil {
		response.Error(w, 500, "internal server error")
		return
	}

	response.JSON(w, 200, ResponseReqList{
		PendingIDsList: *list.PendingIDsList,
		RequestIDsList: *list.RequestIDsList,
	})
}

func (h *UserHandler) GetFriendList(w http.ResponseWriter, r *http.Request) {
	userID, err := middleware.GetUserContextKey(r.Context())
	if err != nil {
		response.Error(w, 400, "invalid request")
		return
	}
	list, err := h.userService.GetFriendList(r.Context(), *userID)
	if err != nil {
		response.Error(w, 500, "internal server error")
		return
	}
	response.JSON(w, 200, ResponseFriListStruct{
		FriendList: list,
	})
}

//WARN::rethinking about this
// //route for cancel
// func (h *UserHandler)CancelFriendReq(w http.ResponseWriter,r *http.Request){
// 	userID, ok := r.Context().Value(middleware.USERCONTEXTKEY).(uuid.UUID)
// 	if !ok{
// 		response.Error(w,400,"invalid request")
// 		return
// 	}
// 	//err := h.userService.CancelFriendReq(ctx,userID)
//
// }
