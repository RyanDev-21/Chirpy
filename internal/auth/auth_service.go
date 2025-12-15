package auth

import (
	"context"
	"errors"

	"RyanDev-21.com/Chirpy/internal/database"
	"RyanDev-21.com/Chirpy/internal/users"
	"RyanDev-21.com/Chirpy/pkg/auth"
)


var InvalidCredentailErr = errors.New("invalid credentials")

type AuthService interface{
	Login(ctx context.Context,email,password string)(accessToken,refreshToken string,user *users.User,err error)	
}


type authService struct{
	userRepo users.UserRepo
	secret string
	queries *database.Queries
}

func NewAuthService(userRepo users.UserRepo,secret string,queries *database.Queries)AuthService{
	return &authService{
		userRepo: userRepo,
		secret: secret,
		queries: queries,
	}
}



func (s *authService)Login(ctx context.Context,email,password string)(accessToken,refreshToken string,user *users.User,err error)	{
	dbUser,hashPassword,err:= s.userRepo.GetUserByEmail(ctx,email)
	if err !=nil{
		if err == users.NoUserFoundErr{
			return "","",nil,users.NoUserFoundErr
		}
		return "","",nil,InvalidCredentailErr
	}

	valid, err := auth.CheckPassword(password,hashPassword)
	if err !=nil || !valid{
		return "","",nil,InvalidCredentailErr
	}
	accessToken,refreshToken,err = auth.GetAccessTokenAndRefreshToken(ctx,dbUser.ID,s.secret,s.queries)
	if err !=nil{
		return "","",nil,err
	}
	
		
	return accessToken,refreshToken,dbUser,nil	
}

