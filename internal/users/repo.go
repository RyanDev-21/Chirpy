package users

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	//	"log"
	"strings"
	"time"

	"RyanDev-21.com/Chirpy/internal/database"
	//"RyanDev-21.com/Chirpy/pkg/encoder"
	"github.com/google/uuid"
)

var (
	NoUserFoundErr      = errors.New("no user found")
	DuplicateKeyErr     = errors.New("duplicate error")
	DuplicateNameKeyErr = errors.New("duplicate name error")
	NoRecordFoundErr    = errors.New("no row found")
)

type UserRepo interface {
	Create(ctx context.Context, input CreateUserInput) (*User, error)
	GetUserByEmail(ctx context.Context, email string) (*User, string, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (*User, string, error)
	UpdateUserPassword(ctx context.Context, payload UpdateUserPassword) (*User, error)
	GetAllUsers(ctx context.Context) (*[]database.User, error)
	GetAllUsersRs(ctx context.Context) (*[]database.GetAllUserRsRow, error)
	SendFriendRequest(fromID, toID, friReqID uuid.UUID) error
	GetMyFriReqList(ctx context.Context, userID uuid.UUID) (*[]database.GetFriReqListRow, error)
	GetMySendFirReqList(ctx context.Context, userID uuid.UUID) (*[]database.GetYourSendReqListRow, error)
	UpdateFriReq(reqID uuid.UUID, fromID uuid.UUID, updateTime time.Time) error // this one confirm the fri req
	GetUserFriListByID(ctx context.Context, userID uuid.UUID) (*[]database.GetUserFriListByIDRow, error)
	CancelFriReq(reqID uuid.UUID, fromID uuid.UUID, updateTime time.Time) error // this one cancel the req
	GetMatchName(ctx context.Context, searchName string) (*[]User, error)
	DeleteFriReq(reqID uuid.UUID, fromID uuid.UUID) error // delete the record
	GetOtherUserIDByReqID(ctx context.Context, userID uuid.UUID, reqID uuid.UUID) (*User, error)
	SaveEleConfig(userID uuid.UUID, eleconfigs *[]ElementCustom) error
	GetAllConfigUser(ctx context.Context, userID uuid.UUID) (*[]ElementCustom, error)
	GetAllUserConfigs(ctx context.Context) (*[]database.Eleconfig, error)
}

type userRepo struct {
	queries *database.Queries
}

//
// type FriendMetaDataItf interface {
// 	GetID() uuid.UUID
// 	GetName() string
// }

func toUserFormat(dbUser database.User) *User {
	return &User{
		ID:        dbUser.ID,
		Name:      dbUser.Name,
		CreatedAt: dbUser.CreatedAt,
		UpdatedAt: dbUser.UpdatedAt,
		Email:     dbUser.Email,
		IsRED:     dbUser.IsChirpyRed.Bool,
	}
}

func toUserFormatForOtherUserInfo(dbUser database.GetOtherUserInfoByReqIDRow) *User {
	return &User{
		ID:        uuid.MustParse(dbUser.ID.String()),
		Email:     dbUser.Email.String,
		Name:      dbUser.Name.String,
		CreatedAt: dbUser.CreatedAt,
		UpdatedAt: dbUser.UpdatedAt,
	}
}

// func toFriendMetaDataFormat[T FriendMetaDataItf](dbRow *[]T) *[]FriendMetaData {
// 	var list []FriendMetaData
// 	for _, v := range *dbRow {
// 		list = append(list, FriendMetaData{
// 			UserID: v.GetID(),
// 			Name:   v.GetName(),
// 		})
// 	}
//
// 	return &list
// }

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
		if strings.Contains(err.Error(), "unique constraint") {
			if strings.Contains(err.Error(), "\"users_name_key\"") {
				return nil, DuplicateNameKeyErr
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

// this function is for the sake of upading the cache so no neeed to format
func (r *userRepo) GetAllUsers(ctx context.Context) (*[]database.User, error) {
	userList, err := r.queries.GetAllUser(ctx)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, NoUserFoundErr
		}
		return nil, err
	}
	return &userList, nil
}

func (r *userRepo) GetAllUsersRs(ctx context.Context) (*[]database.GetAllUserRsRow, error) {
	rsList, err := r.queries.GetAllUserRs(ctx)
	if err != nil {
		return nil, err
	}
	return &rsList, nil
}

func (r *userRepo) SendFriendRequest(fromID, toID, friReqID uuid.UUID) error {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	err := r.queries.AddSendReq(ctx, database.AddSendReqParams{
		ID:          friReqID,
		UserID:      fromID,
		OtheruserID: toID,
	})
	return err
}

func (r *userRepo) GetMyFriReqList(ctx context.Context, userID uuid.UUID) (*[]database.GetFriReqListRow, error) {
	list, err := r.queries.GetFriReqList(ctx, userID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, NoRecordFoundErr
		}
		return nil, err
	}
	return &list, nil
}

func (r *userRepo) GetMySendFirReqList(ctx context.Context, userID uuid.UUID) (*[]database.GetYourSendReqListRow, error) {
	list, err := r.queries.GetYourSendReqList(ctx, userID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, NoRecordFoundErr
		}
		return nil, err
	}
	return &list, nil
}

func (r *userRepo) UpdateFriReq(reqID uuid.UUID, fromID uuid.UUID, updateTime time.Time) error {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	err := r.queries.UpdateSendReq(ctx, database.UpdateSendReqParams{
		ID:          reqID,
		UpdatedAt:   updateTime,
		OtheruserID: fromID,
	})
	return err
}

// NOTE:the db returns interface type so need to type assert every element in the list
func (r *userRepo) GetUserFriListByID(ctx context.Context, userID uuid.UUID) (*[]database.GetUserFriListByIDRow, error) {
	list, err := r.queries.GetUserFriListByID(ctx, userID)
	if err != nil {
		return nil, err
	}

	return &list, nil
}

func (r *userRepo) CancelFriReq(reqID uuid.UUID, fromID uuid.UUID, updateTime time.Time) error {
	context, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	err := r.queries.CancelFriReqStatus(context, database.CancelFriReqStatusParams{
		ID:          reqID,
		UpdatedAt:   updateTime,
		OtheruserID: fromID,
	})
	return err
}

func (r *userRepo) DeleteFriReq(reqID uuid.UUID, fromID uuid.UUID) error {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	err := r.queries.DeleteFriReq(ctx, database.DeleteFriReqParams{
		ID:     reqID,
		UserID: fromID,
	})
	return err
}

func (r *userRepo) GetOtherUserIDByReqID(ctx context.Context, userID uuid.UUID, reqID uuid.UUID) (*User, error) {
	user, err := r.queries.GetOtherUserInfoByReqID(ctx, database.GetOtherUserInfoByReqIDParams{
		UserID: userID,
		ID:     reqID,
	})
	if err != nil {
		return nil, err
	}
	return toUserFormatForOtherUserInfo(user), nil
}

func (r *userRepo) GetMatchName(ctx context.Context, searchName string) (*[]User, error) {
	userList, err := r.queries.SearchNameSiml(ctx, searchName)
	if err != nil {
		return nil, err
	}
	var userInfoList []User
	for _, v := range userList {
		userInfoList = append(userInfoList, User{
			ID:        v.ID,
			Name:      v.Name,
			Email:     v.Email,
			CreatedAt: v.CreatedAt,
			UpdatedAt: v.UpdatedAt,
			IsRED:     v.IsChirpyRed.Bool,
		})
	}
	return &userInfoList, nil
}

func (r *userRepo) SaveEleConfig(userID uuid.UUID, eleconfigs *[]ElementCustom) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Millisecond)
	defer cancel()
	var byteArr []byte
	byteArr, err := json.Marshal(eleconfigs)
	if err != nil {
		return err
	}
	err = r.queries.SavePosition(ctx, database.SavePositionParams{
		UserID:  userID,
		Column2: byteArr,
	})
	if err != nil {
		return err
	}
	return nil
}

func (r *userRepo) GetAllConfigUser(ctx context.Context, userID uuid.UUID) (*[]ElementCustom, error) {
	bytes, err := r.queries.GetAllConfigForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	var configList []ElementCustom
	err = json.Unmarshal(bytes, &configList)
	if err != nil {
		return nil, err
	}
	return &configList, nil
}

func (r *userRepo) GetAllUserConfigs(ctx context.Context) (*[]database.Eleconfig, error) {
	res, err := r.queries.GetAllUsersConfig(ctx)
	if err != nil {
		return nil, err
	}
	return &res, nil
}
