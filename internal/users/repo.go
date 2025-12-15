package users

import (
	"context"
	"database/sql"
	"errors"

	"RyanDev-21.com/Chirpy/internal/database"
	"github.com/google/uuid"
)

var NoUserFoundErr= errors.New("no user found")

type UserRepo interface{
	Create(ctx context.Context,input CreateUserInput)(*User,error)
	GetUserByEmail(ctx context.Context,email string)(*User,string,error)	
	UpdateUserPassword(ctx context.Context,hashpassword string,userID uuid.UUID)(*User,error)	
}

type userRepo struct{
	queries *database.Queries
}

func toUserFormat(dbUser database.User)*User{
	return &User{
		ID: dbUser.ID,
		CreatedAt: dbUser.CreatedAt,
		UpdatedAt: dbUser.UpdatedAt,
		Email: dbUser.Email,
		IsRED: dbUser.IsChirpyRed.Bool,
	}
}

func NewUserRepo(queries *database.Queries)UserRepo{
	return &userRepo{
		queries: queries,
	}
}


func (r *userRepo)Create(ctx context.Context,input CreateUserInput)(*User,error){
	user, err:= r.queries.CreateUser(ctx,database.CreateUserParams{
		Email: input.Email,
		Password: input.Password,
	})
	if err !=nil{
		return nil,err	
	}
	return toUserFormat(user),nil
}


func (r *userRepo)GetUserByEmail(ctx context.Context,email string)(*User,string,error){
	user, err := r.queries.GetUserInfoByEmail(ctx,email)
	if err !=nil{
		if err == sql.ErrNoRows{
			return nil,"",NoUserFoundErr
		}
		return nil,"",err
	}

	return toUserFormat(user),user.Password,nil
}


func (r *userRepo)UpdateUserPassword(ctx context.Context,hashpassword string,userID uuid.UUID)(*User,error){
	err := r.queries.UpdatePassword(ctx,database.UpdatePasswordParams{
		Password: hashpassword,
		ID: userID,
	})
	if err !=nil{
		return nil,err
	}
	user,err := r.queries.GetUserInfoByID(ctx,userID)
	if err !=nil{
		return nil,err
	}
	return toUserFormat(user),nil
}

