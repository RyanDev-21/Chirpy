package users

import (
	"fmt"
	"encoding/json"
	"log"
	"net/http"

	"RyanDev-21.com/Chirpy/pkg/auth"
	"RyanDev-21.com/Chirpy/pkg/middleware"
	"RyanDev-21.com/Chirpy/pkg/response"
	"github.com/google/uuid"
)


type UserHandler struct{
	userService UserService
}


func NewUserHandler(userService UserService)*UserHandler{
	return &UserHandler{
		userService: userService,
	}
}
func (h *UserHandler)Register(w http.ResponseWriter,r *http.Request){
	decoder := json.NewDecoder(r.Body)
	params := &DefaultUsersParameters{}
	err := decoder.Decode(params)
	if err !=nil{
		response.Error(w,400,"invalid params")
		return
	}
	user, err:= h.userService.Register(r.Context(),params.Email,params.Password)
	if err !=nil{
		if err == DuplicateKeyErr{
			response.Error(w,400,"the user already exists")
			return
		}
		log.Printf("internal error :#%s#",err)
		response.Error(w,500,"something went wrong")
		return
	}
	fmt.Println("created the user now returning")
	response.JSON(w,200,user)
}


//uses one of user services and then hanlde the http route just as the name suggest
func (h *UserHandler)UpdatePassword(w http.ResponseWriter,r *http.Request){
	type parameters struct{
		OldPass string `json:"old_password"`
		NewPass string `json:"new_password"`
	}
	decoder := json.NewDecoder(r.Body)
	params := &parameters{}
	err := decoder.Decode(params)
	if err !=nil{
		response.Error(w,400,"invalid params")	
		return
	}

	userID,ok := r.Context().Value(middleware.USERCONTEXTKEY).(uuid.UUID)
	if !ok{
		response.Error(w,500,"internal server error")
		return	
	}
	updatedUser,err := h.userService.UpdatePassword(r.Context(),userID,params.OldPass,params.NewPass)
	if err !=nil{
		if err == NoUserFoundErr{
			response.Error(w,404,"no user found error")
			return
		}
		if err == auth.ErrPassNotMatch{
			response.Error(w,401,"unauthorized")
			return
		}

		response.Error(w,500,"Internal server error")
		return
	}
	response.JSON(w,200,updatedUser)
	
}
