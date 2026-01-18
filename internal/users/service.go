package users

import (
	"context"
	"RyanDev-21.com/Chirpy/pkg/auth"
	"github.com/google/uuid"
)
type UserService interface{
	Register(ctx context.Context,name,email,password string)(*User,error)
	UpdatePassword(ctx context.Context,userID uuid.UUID,oldPass string,newPass string)(*User,error)
	AddFriendSend(ctx context.Context,sendID,recieveID uuid.UUID,label string)error
}

type userService struct{
	userRepo UserRepo
	userCache UserCacheItf
}

func NewUserService(userRepo UserRepo,userCache UserCacheItf)UserService{
	return &userService{
		userRepo: userRepo,
		userCache: userCache,
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
func (s *userService)AddFriendSend(ctx context.Context,senderID,receiveID uuid.UUID,label string)error{
	s.userCache.UpdateUserRs(&CacheUpdateStruct{
		UserID: senderID,	
		toID: receiveID,
		Label: label,
	})			
	//need to publish the job for db
	return nil
}

