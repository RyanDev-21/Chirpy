package auth

import (
	"context"
	"database/sql"
	"errors"

	"RyanDev-21.com/Chirpy/internal/database"
)

var ErrNotAuthorized = errors.New("not found matching token")
var ErrTokenExpired = errors.New("token expired")

type authRepo struct{
	queries *database.Queries	
}


type AuthRepo interface{
	RevokeToken(ctx context.Context,token string)error
	GetRefreshToken(ctx context.Context, token string)(*RefreshToken,error)
	CreateRefreshToken(ctx context.Context,payload PayloadForRefresh)error
}

func NewAuthRepo(queries *database.Queries)AuthRepo{
	return &authRepo{
		queries:queries ,
	}
}

func convertToDomainModel(payload database.RefreshToken)*RefreshToken{
	return &RefreshToken{
		Token: payload.Token,
		UserID: payload.UserID,
		ExpiresAt: payload.ExpireAt,
		UpdatedAt: payload.UpdatedAt,
	}
}

func (r *authRepo)RevokeToken(ctx context.Context,token string)error{
	err := r.queries.RevokeRefreshToken(ctx,token)
	if err !=nil{
		if err == sql.ErrNoRows{
			return ErrNotAuthorized 
		}
		return err
	}
	return nil
}

func (r *authRepo)GetRefreshToken(ctx context.Context,token string)(*RefreshToken,error){
	response , err:= r.queries.GetRefreshToken(ctx,token)
	if err != nil{
		if err == sql.ErrNoRows{
			return convertToDomainModel(response),ErrNotAuthorized
		}
		return convertToDomainModel(response), err
	}
	return convertToDomainModel(response),nil
}

func (r *authRepo)CreateRefreshToken(ctx context.Context,payload PayloadForRefresh)error{
	_,err := r.queries.CreateARefreshToken(ctx,database.CreateARefreshTokenParams{
		Token: payload.token,
		UserID: payload.userID,
		ExpireAt: payload.expiresAt,
	})	
	return err
}
