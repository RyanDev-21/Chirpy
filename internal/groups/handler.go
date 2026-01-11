package groups

import (
	"encoding/json"
	"log"
	"net/http"

	"RyanDev-21.com/Chirpy/pkg/middleware"
	"RyanDev-21.com/Chirpy/pkg/response"
	"github.com/google/uuid"
)




type groupHandler struct{
	groupService GroupService
}


func NewGroupHandler(groupService GroupService)*groupHandler{
	return &groupHandler{
		groupService: groupService,
	}
}

//has to create the common id for the chat
func (h *groupHandler)CreateGroup(w http.ResponseWriter,r *http.Request){

	parameters := createGroupRequest{}
	decoder := json.NewDecoder(r.Body)
	err := decoder.Decode(&parameters)
	if err !=nil{

		log.Printf("failed to decode the params #%s#",err)
		response.Error(w,400,"invalid credentails")
		return
	}


	createrID, ok := r.Context().Value(middleware.USERCONTEXTKEY).(uuid.UUID)
	if !ok{
		response.Error(w,401,"not authorized")
		return
	}
	
	//will return the common chatID
	responseStruct, err := h.groupService.createGroup(r.Context(),createrID,&parameters)
	if err !=nil{
		if err == ErrDuplicateName{
			response.Error(w,400,err.Error())
			return
		}
		response.Error(w,500,"somthing went wrong")
		return
	}
	response.JSON(w,200,responseStruct)	

}

func (h *groupHandler)JoinGroup(w http.ResponseWriter, r *http.Request){
	stringGroupID := r.PathValue("group_id")
	if stringGroupID == ""{
		response.Error(w,400,"invalid request")
		return
	}
	groupID,err := uuid.Parse(stringGroupID)
	if err !=nil{
		response.Error(w,400,"invalid request")
		return
	}


	userID, ok := r.Context().Value(middleware.USERCONTEXTKEY).(uuid.UUID)
	if !ok{
		response.Error(w,401,"not authorized")
		return
	}

	err = h.groupService.joinGroup(r.Context(),groupID,userID)
	if err !=nil{
		response.Error(w,500,"something went wrong")
		return
	}
	
	w.WriteHeader(201)	
}

func (h *groupHandler)LeaveGroup(w http.ResponseWriter, r *http.Request){
	stringGroupID := r.PathValue("group_id")
	if stringGroupID == ""{
		response.Error(w,400,"invalid request")
		return
	}
	groupID,err := uuid.Parse(stringGroupID)
	if err !=nil{
		response.Error(w,400,"invalid request")
		return
	}


	userID, ok := r.Context().Value(middleware.USERCONTEXTKEY).(uuid.UUID)
	if !ok{
		response.Error(w,401,"not authorized")
		return
	}

	err = h.groupService.leaveGroup(r.Context(),groupID,userID)
	if err !=nil{
		response.Error(w,500,"something went wrong")
		return
	}
	
	w.WriteHeader(201)	
}


