package users

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"RyanDev-21.com/Chirpy/internal/database"
	"github.com/google/uuid"
)

var (
	NoUserFoundErr  = errors.New("no user found")
	DuplicateKeyErr = errors.New("duplicate error")
	DuplicateNameKeyErr = errors.New("duplicate name error")
	NoRecordFoundErr = errors.New("no row found")
)

type UserRepo interface {
	Create(ctx context.Context, input CreateUserInput) (*User, error)
	GetUserByEmail(ctx context.Context, email string) (*User, string, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (*User, string, error)
	UpdateUserPassword(ctx context.Context, payload UpdateUserPassword) (*User, error)
	GetAllUsers(ctx context.Context)(*[]database.User,error)
	GetAllUsersRs(ctx context.Context)(*[]database.UserRelationship,error)
	SendFriendRequest(fromID,toID,friReqID uuid.UUID)error
	GetMyFriReqList(ctx context.Context,userID uuid.UUID)(*[]database.UserRelationship,error)	
	GetMySendFirReqList(ctx context.Context,userID uuid.UUID)(*[]database.UserRelationship,error)
	UpdateFriReq(reqID uuid.UUID)error
}

type userRepo struct {
	queries *database.Queries
}

func toUserFormat(dbUser database.User) *User {
	return &User{
		ID:        dbUser.ID,
		CreatedAt: dbUser.CreatedAt,
		UpdatedAt: dbUser.UpdatedAt,
		Email:     dbUser.Email,
		IsRED:     dbUser.IsChirpyRed.Bool,
	}
}

func NewUserRepo(queries *database.Queries) UserRepo {
	return &userRepo{
		queries: queries,
	}
}

func (r *userRepo) Create(ctx context.Context, input CreateUserInput) (*User, error) {
	user, err := r.queries.CreateUser(ctx, database.CreateUserParams{
		Name:     input.Name,
		Email:    input.Email,
		Password: input.Password,
	})
	if err != nil {
		if strings.Contains(err.Error(), "unique constraint" ) {
			if strings.Contains(err.Error(),"\"users_name_key\""){
				return nil,DuplicateNameKeyErr
			}
			return nil, DuplicateKeyErr
		}
		return nil, err
	}
	return toUserFormat(user), nil
}

// returns deafult user struct with password if needed
func (r *userRepo) GetUserByEmail(ctx context.Context, email string) (*User, string, error) {
	user, err := r.queries.GetUserInfoByEmail(ctx, email)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, "", NoUserFoundErr
		}
		return nil, "", err
	}

	return toUserFormat(user), user.Password, nil
}

func (r *userRepo) UpdateUserPassword(ctx context.Context, payload UpdateUserPassword) (*User, error) {
	err := r.queries.UpdatePassword(ctx, database.UpdatePasswordParams{
		Password: payload.Password,
		ID:       payload.UserID,
	})
	if err != nil {
		return nil, err
	}
	user, err := r.queries.GetUserInfoByID(ctx, payload.UserID)
	if err != nil {
		return nil, err
	}
	return toUserFormat(user), nil
}

// returns same thing as byEmail
func (r *userRepo) GetUserByID(ctx context.Context, userID uuid.UUID) (*User, string, error) {
	user, err := r.queries.GetUserInfoByID(ctx, userID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, "", NoUserFoundErr
		}
		return nil, "", err
	}
	return toUserFormat(user), user.Password, nil
}


//this function is for the sake of upading the cache so no neeed to format
func (r *userRepo)GetAllUsers(ctx context.Context)(*[]database.User,error){
	userList, err :=r.queries.GetAllUser(ctx)
	if err !=nil{
		if err == sql.ErrNoRows{
			return nil,NoUserFoundErr
		}
		return nil,err
	}
	return &userList ,nil
}

func (r *userRepo)GetAllUsersRs(ctx context.Context)(*[]database.UserRelationship,error){
	rsList,err:=r.queries.GetAllUserRs(ctx)
	if err !=nil{
		return nil,err
	}
	return &rsList,nil 
}

func (r *userRepo)SendFriendRequest(fromID,toID,friReqID uuid.UUID)error{
	ctx,cancel := context.WithTimeout(context.Background(),1*time.Second)
	defer cancel()
	err :=r.queries.AddSendReq(ctx,database.AddSendReqParams{
		ID: friReqID,	
		UserID: fromID,
		OtheruserID: toID,
	})
	return err
}

func (r *userRepo)GetMyFriReqList(ctx context.Context,userID uuid.UUID)(*[]database.UserRelationship,error){
	list , err := r.queries.GetFriReqList(ctx,userID)
	if err !=nil{
		if err == sql.ErrNoRows{
			return nil,NoRecordFoundErr
		}
		return nil,err
	}
	return &list,nil
}

func (r *userRepo)GetMySendFirReqList(ctx context.Context,userID uuid.UUID)(*[]database.UserRelationship,error){
	list ,err := r.queries.GetYourSendReqList(ctx,userID)
	if err !=nil{
		if err == sql.ErrNoRows{
			return nil,NoRecordFoundErr
		}
		return nil,err
	}
	return &list,nil
}

func(r *userRepo)UpdateFriReq(reqID uuid.UUID)error{
	ctx,cancel := context.WithTimeout(context.Background(),1*time.Second)
	defer cancel()
	err:= r.queries.UpdateSendReq(ctx,reqID)	
	return err
}	

