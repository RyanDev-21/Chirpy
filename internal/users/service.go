package users

import (
	"context"

	"RyanDev-21.com/Chirpy/pkg/auth"
)


type UserService interface{
	Register(ctx context.Context,email,password string)(*User,error)
	UpdatePassword(ctx context.Context,token string,password string)(*User,error)
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


func (s *userService)UpdatePassword(ctx context.Context,token string,password string)(*User,error){
	hashPassword, err:= auth.HashPassword(password)
	if err !=nil{
		return nil,err
	}
	auth.ValidateJWT(token)
}
