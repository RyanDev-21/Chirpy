package auth

import (
	"context"
	"errors"
	"time"
	"log"
	"RyanDev-21.com/Chirpy/internal/users"
	"RyanDev-21.com/Chirpy/pkg/auth"
	"github.com/google/uuid"
)


var InvalidCredentailErr = errors.New("invalid credentials")

type AuthService interface{
	Login(ctx context.Context,email,password string)(accessToken,refreshToken string,user *users.User,err error)	
	Revoke(ctx context.Context,token string) error
	Refresh(ctx context.Context,token string)(string,string,error)
}


type authService struct{
	authRepo AuthRepo
	userRepo users.UserRepo
	secret string
}

func NewAuthService(userRepo users.UserRepo,authRepo AuthRepo,secret string)AuthService{
	return &authService{
		authRepo : authRepo,
		userRepo: userRepo,
		secret: secret,
	}
}

func (s *authService)Login(ctx context.Context,email,password string)(accessToken,refreshToken string,user *users.User,err error){
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
	accessToken,refreshToken,err =s.generateTokens(ctx,dbUser.ID)
	if err !=nil{
		return "","",nil,err
	}
	return accessToken,refreshToken,dbUser,nil
}

func (s *authService)Revoke(ctx context.Context,token string)error{
	err := s.authRepo.RevokeToken(ctx,token)
	if err !=nil{
		return err
	}
	return nil
	
}


//need to add the logic to check whether the token has already revoked or not 
func (s *authService)Refresh(ctx context.Context,token string)(string,string,error){
	response ,err := s.authRepo.GetRefreshToken(ctx,token)
	if err !=nil{
		return "","",err
	}
	if time.Now().After(response.ExpiresAt){
		return "","",ErrTokenExpired
	}
	err = s.authRepo.RevokeToken(ctx,token)	
	if err !=nil{
		return "","",err
	}

	//returns both tokens alongside the error(if exists)
	accessToken,refreshToken,err :=s.generateTokens(ctx,response.UserID)
	if err !=nil{
		return "","",err
	}
	return accessToken,refreshToken,nil
}

//uses auth pkg simply to create the tokens with specific expire date
func (s *authService)generateTokens(ctx context.Context,userID uuid.UUID)(string,string,error){
	expireIn := 60*time.Minute


	accessToken ,err:= auth.MakeJWT(userID,s.secret,expireIn)
	if err !=nil{
		log.Printf("failed to make accessToken %s",err)
		return 	"","",err
	}
	refreshToken, err:= auth.MakeRefreshToken()
	if err !=nil{
		log.Printf("failed to make a refreshToken %s",err)
		return "","",err	
	}
	refreshTokenExpireDate := 30*(24*time.Hour)

	err= s.authRepo.CreateRefreshToken(ctx,PayloadForRefresh{
		token: refreshToken,
		userID:userID,
		expiresAt:time.Now().Add(refreshTokenExpireDate) ,
	})
	if err!=nil{
		log.Printf("failed to insert into db #%s#",err)
		return "","",err	
	}

	return accessToken,refreshToken,nil

}

