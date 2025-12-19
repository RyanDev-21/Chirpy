package users

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"RyanDev-21.com/Chirpy/internal/database"
	"github.com/google/uuid"
)

var NoUserFoundErr= errors.New("no user found")
var DuplicateKeyErr = errors.New("duplicate error")


type UserRepo interface{
	Create(ctx context.Context,input CreateUserInput)(*User,error)
	GetUserByEmail(ctx context.Context,email string)(*User,string,error)	
	GetUserByID(ctx context.Context,id uuid.UUID)(*User,string,error)
	UpdateUserPassword(ctx context.Context,payload UpdateUserPassword)(*User,error)	
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
		if strings.Contains(err.Error(),"unique constraint"){
			return nil,DuplicateKeyErr
		}
		return nil,err	
	}
	return toUserFormat(user),nil
}



//returns deafult user struct with password if needed
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


func (r *userRepo)UpdateUserPassword(ctx context.Context,payload UpdateUserPassword)(*User,error){
	err := r.queries.UpdatePassword(ctx,database.UpdatePasswordParams{
		Password: payload.Password,
		ID: payload.UserID,
	})
	if err !=nil{
		return nil,err
	}
	user,err := r.queries.GetUserInfoByID(ctx,payload.UserID)
	if err !=nil{
		return nil,err
	}
	return toUserFormat(user),nil
}


//returns same thing as byEmail
func (r *userRepo)GetUserByID(ctx context.Context,userID uuid.UUID)(*User,string,error){
	user, err := r.queries.GetUserInfoByID(ctx,userID)
	if err !=nil{
		if err == sql.ErrNoRows{
			return nil,"",NoUserFoundErr
		}
		return nil,"",err
	}
	return toUserFormat(user),user.Password,nil
}
