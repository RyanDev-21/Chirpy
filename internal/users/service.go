package users

import (
	"context"
	"database/sql"
	"log"
	mq "RyanDev-21.com/Chirpy/internal/customMq"
	"RyanDev-21.com/Chirpy/pkg/auth"
	"github.com/google/uuid"
)
type UserService interface{
	Register(ctx context.Context,name,email,password string)(*User,error)
	UpdatePassword(ctx context.Context,userID uuid.UUID,oldPass string,newPass string)(*User,error)
	AddFriendSend(ctx context.Context,sendID,recieveID uuid.UUID,label string,friReqID uuid.UUID)error
	ConfirmFriendReq(ctx context.Context,fromID,toID,reqID uuid.UUID,status string)error
	GetPendingList(ctx context.Context,userID uuid.UUID)(*GetReqList,error)
	StartWorkerForAddFri(channel chan *mq.Channel)
	StartWorkerForConfirmFri(channel chan *mq.Channel)
}

type userService struct{
	userRepo UserRepo
	userCache UserCacheItf
	mainMq *mq.MainMQ	
}

func NewUserService(userRepo UserRepo,userCache UserCacheItf,mainMq *mq.MainMQ)UserService{
	return &userService{
		userRepo: userRepo,
		userCache: userCache,
		mainMq: mainMq,
	}
}

func (s *userService)Register(ctx context.Context,name,email,password string)(*User,error){
	hashpassword, err:= auth.HashPassword(password)
	if err !=nil{
		return nil,err
	}
	user, err:= s.userRepo.Create(ctx,CreateUserInput{Name:name,Email: email,Password: hashpassword})
	if err !=nil{
		return nil,err
	}
	return user,nil
}


func (s *userService)UpdatePassword(ctx context.Context,userID uuid.UUID,oldPassword string,newPassword string)(*User,error){
	_,pass, err:= s.userRepo.GetUserByID(ctx,userID)
	if err !=nil{
		return nil, err	
	}
	valid ,err:= auth.CheckPassword(oldPassword,pass)
	if err !=nil{
		return nil,err
	}
	if !valid {
		return nil,auth.ErrPassNotMatch
	}
	hashPassword , err:= auth.HashPassword(newPassword)
	if err !=nil{
		return nil,err
	}

	payload := UpdateUserPassword{
		UserID: userID,
		Password: hashPassword,
	}
	user,err := s.userRepo.UpdateUserPassword(ctx,payload)
	if err !=nil{
		return nil,err
	}
	return user,nil
}


//will save  record with pending stauts 
func (s *userService)AddFriendSend(ctx context.Context,senderID,receiveID uuid.UUID,label string,friReqID uuid.UUID)error{
	//udpate the current user cache
	s.userCache.UpdateUserRs(CacheUpdateStruct{
		UserID: senderID,	
		ReqID: friReqID,
		Lable: "send",
	})
	//this update the opp user
	s.userCache.UpdateUserRs(CacheUpdateStruct{
		UserID: receiveID,
		ReqID: friReqID,
		Lable: "pending",
	})
//	need to publish the job for db
	s.mainMq.Publish("sendRequest",&FriendReq{
		ReqID : friReqID,		
		FromID: senderID,
		ToID: receiveID,
	})
	return nil
}

//this need to return error for failed case didn't do any of that 
func (s *userService)ConfirmFriendReq(ctx context.Context,fromID,toID,reqID uuid.UUID,status string)error{
	//this update the pending guy
	s.userCache.CleanUpUserRs(&CacheRsDeleteStruct{
		UserID: fromID,
		ReqID: reqID,
		Lable: "pending",
	})

	//this update the sending guy
	s.userCache.CleanUpUserRs(&CacheRsDeleteStruct{
		UserID: toID,
		ReqID: reqID,
		Lable: "send",
	})
	
	//this update the pending guy
	s.userCache.UpdateUserRs(CacheUpdateFriStruct{
		UserID: fromID,
		ToID: toID,
		Lable:"friend",
	})

	s.userCache.UpdateUserRs(CacheUpdateFriStruct{
		UserID: toID,
		ToID: fromID,
		Lable: "friend",
	})

	s.mainMq.Publish("confirmFriReq",&FriendReq{
		ReqID: reqID,
	})
	 
	return nil
}

//NOTE:: need to rethink about this
// func (s *userService)GetFriendList(ctx context.Context,userID uuid.UUID)([]uuid.UUID,error){
// 	list:= s.userCache.GetUserFriList(userID)									
// 	if list ==nil{
// 		log.Print("cannot find in the cache")	
// 		list,err := s.userRepo.GetAllUsersRs(ctx)
// 		return []uuid.UUID{},nil
// 	}
// 	return *list,nil
// }

func (s *userService)GetPendingList(ctx context.Context,userID uuid.UUID)(*GetReqList,error){
	list := GetReqList{
		PendingIDsList: &map[uuid.UUID]uuid.UUID{},
		RequestIDsList: &map[uuid.UUID]uuid.UUID{},
	}
	check:= s.userCache.GetUserRs(userID)
	if !check{
		log.Print("are we deadass in side the not found")
		reqList,err	:= s.userRepo.GetMyFriReqList(ctx,userID)
		if err !=nil{
			if err !=sql.ErrNoRows{
				return nil,err
			}
		}

		listOne := *list.PendingIDsList	
		if reqList !=nil{
			for _,v := range *reqList{
			listOne[v.ID] = v.UserID
		}
		}	
		reqSendList,err	:= s.userRepo.GetMySendFirReqList(ctx,userID)
		if err !=nil{
			if err != sql.ErrNoRows{
				return nil, err	
			}	
		}
		listTwo := *list.RequestIDsList
		if reqSendList !=nil{
			for _,v := range *reqSendList{
			listTwo[v.ID] = v.OtheruserID		
			}


		}
		list.PendingIDsList = &listOne
		list.RequestIDsList = &listTwo
		return &list, nil
	}
	log.Print("outside  the check")

	pendingList := s.userCache.GetUserReqList(userID)
	if pendingList !=nil{
		list.PendingIDsList = pendingList
		for k,v := range *list.PendingIDsList{
			log.Printf("%v:%v",k,v)
		}	
	}
	reqList := s.userCache.GetUserSendReqList(userID)
	if reqList !=nil{
		list.RequestIDsList = reqList
	}
	return &list,nil	
}

