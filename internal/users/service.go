package users

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	mq "RyanDev-21.com/Chirpy/internal/customMq"
	"RyanDev-21.com/Chirpy/pkg/auth"
	"RyanDev-21.com/Chirpy/pkg/middleware"
	"github.com/google/uuid"
)

// need to fix the logging part
// centralized function for Mq failed logger
func handleMqFail(jobName string, jobStruct interface{}, err error, logger *slog.Logger) {
	logger.Warn("failed to upbload the job to mq", err)
	saveIntoLog(jobName, jobStruct, logger)
}

type UserService interface {
	Register(ctx context.Context, name, email, password string) (*User, error)
	UpdatePassword(ctx context.Context, userID uuid.UUID, oldPass string, newPass string) (*User, error)
	AddFriendSend(ctx context.Context, sendID, recieveID uuid.UUID, label string, friReqID uuid.UUID) error
	ConfirmFriendReq(ctx context.Context, fromID, reqID uuid.UUID, status string) error
	CancelFriReq(ctx context.Context, userID, reqID uuid.UUID) error
	DeleteFriReq(ctx context.Context, userID, reqID uuid.UUID) error
	GetPendingList(ctx context.Context, userID uuid.UUID) (*GetReqList, error)
	GetFriendList(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error)
	StartWorkerForAddFri(channel chan *mq.Channel)
	StartWorkerForConfirmFri(channel chan *mq.Channel)
	StartWorkerForDeleteReq(channel chan *mq.Channel)
	StartWorkerForCancelReq(channel chan *mq.Channel)
}

type userService struct {
	userRepo  UserRepo
	userCache UserCacheItf
	mainMq    *mq.MainMQ
	logger    *slog.Logger
}

func NewUserService(userRepo UserRepo, userCache UserCacheItf, mainMq *mq.MainMQ,logger *slog.Logger) UserService {
	return &userService{
		userRepo:  userRepo,
		userCache: userCache,
		mainMq:    mainMq,
		logger: logger,
	}
}

// name check still have to be implemented
func (s *userService) Register(ctx context.Context, name, email, password string) (*User, error) {

	reqID, err := middleware.GetContextKey(ctx,"request")
	if err != nil {

		s.logger.Error("failed to get the ID")
		return nil, err
	}
	hashpassword, err := auth.HashPassword(password)
	if err != nil {
		s.logger.Error("hash function failed","reqID", reqID)
		return nil, err
	}
	user, err := s.userRepo.Create(ctx, CreateUserInput{Name: name, Email: email, Password: hashpassword})
	if err != nil {
		if err == DuplicateKeyErr {
			s.logger.Info("duplicate key constraint","reqID",reqID);
		}
		if err == DuplicateNameKeyErr {
			s.logger.Info("duplicate name key constraint ","reqID", reqID);
		}
		return nil, err
	}
	s.logger.Info("successfully created the user","reqID", reqID);
	return user, nil
}

func (s *userService) UpdatePassword(ctx context.Context, userID uuid.UUID, oldPassword string, newPassword string) (*User, error) {
	_, pass, err := s.userRepo.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	valid, err := auth.CheckPassword(oldPassword, pass)
	if err != nil {
		return nil, err
	}
	if !valid {
		return nil, auth.ErrPassNotMatch
	}
	hashPassword, err := auth.HashPassword(newPassword)
	if err != nil {
		return nil, err
	}

	payload := UpdateUserPassword{
		UserID:   userID,
		Password: hashPassword,
	}
	user, err := s.userRepo.UpdateUserPassword(ctx, payload)
	if err != nil {
		return nil, err
	}
	return user, nil
}

// will save  record with pending stauts
func (s *userService) AddFriendSend(ctx context.Context, senderID, receiveID uuid.UUID, label string, friReqID uuid.UUID) error {
	//udpate the current user cache
	s.userCache.UpdateUserRs(CacheUpdateStruct{
		UserID: senderID,
		ReqID:  friReqID,
		Lable:  "send",
	})
	//this update the opp user
	s.userCache.UpdateUserRs(CacheUpdateStruct{
		UserID: receiveID,
		ReqID:  friReqID,
		Lable:  "pending",
	})
	publishCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()

	job := &FriendReq{
		ReqID:  friReqID,
		FromID: senderID,
		ToID:   receiveID,
	}
	//	need to publish the job for db
	err := s.mainMq.PublishWithContext(publishCtx, SendRequest, job)
	if err != nil {
		handleMqFail(SendRequest, job, err, s.logger)
		return err
	}
	return nil
}

// this need to return error for failed case didn't do any of that
func (s *userService) ConfirmFriendReq(ctx context.Context, fromID, reqID uuid.UUID, status string) error {
	//this gets the opp userID  of the current one
	toID := s.userCache.GetOtherUserIDByReqID(fromID, reqID, "pending")
	if toID == nil {
		s.logger.Warn("failed to get the toID from cache", errors.New("toID is nil"))
	}
	//this update the pending guy
	s.userCache.CleanUpUserRs(&CacheRsDeleteStruct{
		UserID: fromID,
		ReqID:  reqID,
		Lable:  "pending",
	})

	//this update the sending guy
	s.userCache.CleanUpUserRs(&CacheRsDeleteStruct{
		UserID: *toID,
		ReqID:  reqID,
		Lable:  "send",
	})

	//this update the pending guy
	s.userCache.UpdateUserRs(CacheUpdateFriStruct{
		UserID: fromID,
		ToID:   *toID,
		Lable:  "friend",
	})

	s.userCache.UpdateUserRs(CacheUpdateFriStruct{
		UserID: *toID,
		ToID:   fromID,
		Lable:  "friend",
	})

	context, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()
	job := &FriendReq{
		ReqID: reqID,
	}
	err := s.mainMq.PublishWithContext(context, ConfirmFriendReq, job)
	if err != nil {
		handleMqFail(ConfirmFriendReq, job, err, s.logger)
		return err
	}
	return nil
}

// need to handle the errorr from that PublishWithContext
func (s *userService) CancelFriReq(ctx context.Context, userID, reqID uuid.UUID) error {
	toID := s.userCache.GetOtherUserIDByReqID(userID, reqID, "pending")

	if toID == nil {
		s.logger.Warn("faield to get the toID from cache", errors.New("toID is nil"))
	}
	//this update the pending guy
	s.userCache.CleanUpUserRs(&CacheRsDeleteStruct{
		UserID: userID,
		ReqID:  reqID,
		Lable:  "pending",
	})

	//this update the sending guy
	s.userCache.CleanUpUserRs(&CacheRsDeleteStruct{
		UserID: *toID,
		ReqID:  reqID,
		Lable:  "send",
	})
	context, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()
	job := &CancelFriendReq{
		ReqID:      reqID,
		UpdateTime: time.Now(),
	}
	err := s.mainMq.PublishWithContext(context, CancelReq, job)
	if err != nil {
		handleMqFail(CancelReq, job, err, s.logger)
		return err
	}
	return nil
}

// rethink about consistency
func (s *userService) DeleteFriReq(ctx context.Context, userID, reqID uuid.UUID) error {
	toID := s.userCache.GetOtherUserIDByReqID(userID, reqID, "send")
	if toID == nil {
		s.logger.Warn("faield to get the toID from cache", errors.New("toID is nil"))
	}
	//this update the sending guy
	s.userCache.CleanUpUserRs(&CacheRsDeleteStruct{
		UserID: userID,
		ReqID:  reqID,
		Lable:  "send",
	})

	//this update the pending guy
	s.userCache.CleanUpUserRs(&CacheRsDeleteStruct{
		UserID: *toID,
		ReqID:  reqID,
		Lable:  "pending",
	})

	context, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()
	job := &DeleteFirReqStruct{
		ReqID: reqID,
	}
	err := s.mainMq.PublishWithContext(context, DeleteFriReq, job)
	if err != nil {
		handleMqFail(DeleteFriReq, job, err, s.logger)
		return err
	}
	return nil
}

// WARN: need to rethink about this
// need to update the cache after finding from the db
func (s *userService) GetFriendList(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	//first need to get from the cache first
	list := s.userCache.GetUserFriList(userID) //maybe should just check whether the user exist or not first
	if list == nil {
		s.logger.Info("fetching from db because cache miss","userID", userID)
		list, err := s.userRepo.GetUserFriListByID(ctx, userID)
		if err != nil {
			s.logger.Error("failed to get friend list from db", err)
			return nil, err
		}
		s.logger.Info("successfully fetched from db","userID", userID)
		return *list, nil
	}
	return *list, nil
}

// WARN:need to update the cache after fetching from db
func (s *userService) GetPendingList(ctx context.Context, userID uuid.UUID) (*GetReqList, error) {
	list := GetReqList{
		PendingIDsList: &map[uuid.UUID]uuid.UUID{},
		RequestIDsList: &map[uuid.UUID]uuid.UUID{},
	}
	check := s.userCache.GetUserRs(userID)
	if !check {
		s.logger.Debug("fetching from db because cache miss","userID", userID)
		reqList, err := s.userRepo.GetMyFriReqList(ctx, userID)
		if err != nil {
			if err != sql.ErrNoRows {
				s.logger.Error("failed to get pending list from db", err)
				return nil, err
			}
		}

		listOne := *list.PendingIDsList
		if reqList != nil {
			for _, v := range *reqList {
				listOne[v.ID] = v.UserID
			}
		}
		reqSendList, err := s.userRepo.GetMySendFirReqList(ctx, userID)
		if err != nil {
			if err != sql.ErrNoRows {
				s.logger.Error("failed to get request send list from db", err)
				return nil, err
			}
		}
		listTwo := *list.RequestIDsList
		if reqSendList != nil {
			for _, v := range *reqSendList {
				listTwo[v.ID] = v.OtheruserID
			}

		}
		list.PendingIDsList = &listOne
		list.RequestIDsList = &listTwo
		return &list, nil
	}
	s.logger.Debug("fetching from cache", userID)
	pendingList := s.userCache.GetUserReqList(userID)
	if pendingList != nil {
		list.PendingIDsList = pendingList
		for k, v := range *list.PendingIDsList {
			s.logger.Debug("pending list", slog.String("reqID", k.String()), slog.String("fromID", v.String()))
		}
	}
	reqList := s.userCache.GetUserSendReqList(userID)
	if reqList != nil {
		list.RequestIDsList = reqList
	}
	return &list, nil
}


//tempory thing for mq fail
func saveIntoLog(jobName string, job interface{}, logger *slog.Logger) {
	saveLog := []byte(fmt.Sprintf("jobName:%v;jobDescrioption:%v;\n", jobName, job))
	path := filepath.Join("../../", "consistency_log.txt")
	f, err := os.Create(path) //create the path
	defer f.Close()
	if err != nil {
		logger.Error("file create failed", err)
	}
	err = os.WriteFile(path, saveLog, 0644)
	if err != nil {
		logger.Error("file wirte process failed", err)
	}
}
