package users

import (
	"encoding/json"
	"net/http"

	"RyanDev-21.com/Chirpy/pkg/response"
)


type UserHanlder struct{
	userService UserService
}


func NewUserHanlder(userService UserService)*UserHanlder{
	return &UserHanlder{
		userService: userService,
	}
}


func (h *UserHanlder)Register(w http.ResponseWriter,r *http.Request){
	type parameters struct{
		Email string `json:"email"`
		Password string `json:"password"`
	}

	decoder := json.NewDecoder(r.Body)
	params := &parameters{}
	err := decoder.Decode(params)
	if err !=nil{
		response.Error(w,400,"invalid params")
		return
	}
	user, err:= h.userService.Register(r.Context(),params.Email,params.Password)
	if err !=nil{
		response.Error(w,500,"something went wrong")
		return
	}
	response.JSON(w,200,user)
}
