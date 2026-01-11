package users

import (
	"context"
	"RyanDev-21.com/Chirpy/pkg/auth"
	"github.com/google/uuid"
)


type UserService interface{
	Register(ctx context.Context,email,password string)(*User,error)
	UpdatePassword(ctx context.Context,userID uuid.UUID,oldPass string,newPass string)(*User,error)
}


type userService struct{
	userRepo UserRepo
}


func NewUserService(userRepo UserRepo)UserService{
	return &userService{
		userRepo: userRepo,
	}
}


func (s *userService)Register(ctx context.Context,email,password string)(*User,error){
	hashpassword, err:= auth.HashPassword(password)
	if err !=nil{
		return nil,err
	}
	user, err:= s.userRepo.Create(ctx,CreateUserInput{Email: email,Password: hashpassword})
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


