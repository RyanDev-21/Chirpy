package users

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	chatmodel "RyanDev-21.com/Chirpy/internal/chatModel"
	mq "RyanDev-21.com/Chirpy/internal/customMq"
	"RyanDev-21.com/Chirpy/pkg/auth"
	"RyanDev-21.com/Chirpy/pkg/middleware"
	"github.com/google/uuid"
)

// need to fix the logging part
// centralized function for Mq failed logger
func handleMqFail(jobName string, jobStruct interface{}, err error, logger *slog.Logger) {
	logger.Warn("failed to upbload the job to mq", "error", err)
	saveIntoLog(jobName, jobStruct, logger)
}

type UserService interface {
	Register(ctx context.Context, name, email, password string) (*User, error)
	UpdatePassword(ctx context.Context, userID uuid.UUID, oldPass string, newPass string) (*User, error)
	AddFriendSend(ctx context.Context, sendID uuid.UUID, recieverID uuid.UUID, label string) (*uuid.UUID, error)
	ConfirmFriendReq(ctx context.Context, fromID, reqID uuid.UUID, status string) error
	CancelFriReq(ctx context.Context, userID, reqID uuid.UUID) error
	DeleteFriReq(ctx context.Context, userID, reqID uuid.UUID) error
	GetPendingList(ctx context.Context, userID uuid.UUID) (*GetReqList, error)
	GetFriendList(ctx context.Context, userID uuid.UUID) (*[]FriendMetaData, error)
	SearchUser(ctx context.Context, serachName string) (*[]User, error)

	StartWorkerForAddFri(channel chan *mq.Channel)
	StartWorkerForConfirmFri(channel chan *mq.Channel)
	StartWorkerForDeleteReq(channel chan *mq.Channel)
	StartWorkerForCancelReq(channel chan *mq.Channel)
	StartWorkerForUpdateUserCache(channel chan *mq.Channel)
}

type userService struct {
	userRepo  UserRepo
	userCache UserCacheItf
	mainMq    *mq.MainMQ
	hub       *chatmodel.Hub
	logger    *slog.Logger
}

func NewUserService(userRepo UserRepo, userCache UserCacheItf, mainMq *mq.MainMQ, logger *slog.Logger, hub *chatmodel.Hub) UserService {
	return &userService{
		userRepo:  userRepo,
		userCache: userCache,
		mainMq:    mainMq,
		hub:       hub,
		logger:    logger,
	}
}

// name check still have to be implemented
func (s *userService) Register(ctx context.Context, name, email, password string) (*User, error) {
	reqID, err := middleware.GetContextKey(ctx, "request")
	if err != nil {

		s.logger.Error("failed to get the ID")
		return nil, err
	}
	hashpassword, err := auth.HashPassword(password)
	if err != nil {
		s.logger.Error("hash function failed", "reqID", reqID)
		return nil, err
	}
	user, err := s.userRepo.Create(ctx, CreateUserInput{Name: name, Email: email, Password: hashpassword})
	if err != nil {
		if err == DuplicateKeyErr {
			s.logger.Info("duplicate key constraint", "reqID", reqID)
		}
		if err == DuplicateNameKeyErr {
			s.logger.Info("duplicate name key constraint ", "reqID", reqID)
		}
		return nil, err
	}
	s.logger.Info("successfully created the user", "reqID", reqID)
	s.userCache.UpdateUserCache(user)
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

// you should not store both the req and otherUserID
// will save  record with pending stauts
func (s *userService) AddFriendSend(ctx context.Context, senderID uuid.UUID, recieverID uuid.UUID, label string) (*uuid.UUID, error) {
	reqID, _ := middleware.GetContextKey(ctx, "request")

	s.logger.Info("add friend request started", "reqID", reqID, "senderID", senderID, "receiverID", recieverID)

	friReqID, err := uuid.NewV7()
	if err != nil {
		s.logger.Error("failed to generate friend request ID", "reqID", reqID, "error", err)
		return &friReqID, err
	}

	userName := s.userCache.GetUserNameByID(senderID)
	if userName == "" {
		s.logger.Warn("sender name not found in cache", "reqID", reqID, "senderID", senderID)
	}

	otherUserName := s.userCache.GetUserNameByID(recieverID)
	if otherUserName == "" {
		s.logger.Info("receiver not in cache, fetching from DB", "reqID", reqID, "receiverID", recieverID)
		user, _, err := s.userRepo.GetUserByID(ctx, recieverID)
		if err != nil {
			s.logger.Error("failed to get receiver from DB", "reqID", reqID, "error", err)
			return &friReqID, err
		}
		s.userCache.UpdateUserCache(user)
		otherUserName = user.Name
	}
	// otherUserName :=s.userCache.GetUserNameByID(receiveID)
	// if other

	exist := s.userCache.CheckUserRsWithLable(senderID, "send", recieverID)
	if exist {
		return nil, ErrReqExist
	}

	exist = s.userCache.CheckUserFriWithOtherUserID(senderID, recieverID)
	if exist {
		return nil, ErrReqExist
	}

	s.userCache.UpdateUserRs(CacheUpdateStruct{
		UserID: senderID,
		OtherUserInfo: FriendMetaData{
			UserID: recieverID,
			Name:   otherUserName,
		},
		ReqID: friReqID,
		Lable: "send",
	})
	// this update the opp user
	s.userCache.UpdateUserRs(CacheUpdateStruct{
		UserID: recieverID,
		OtherUserInfo: FriendMetaData{
			UserID: senderID,
			Name:   userName,
		},
		ReqID: friReqID,
		Lable: "pending",
	})

	s.logger.Info("checking the ws connection ...", "reqID", reqID, "fromID", senderID)
	valid := s.hub.CheckWsConnection(senderID)
	if !valid {
		s.logger.Warn("the client is not connected to ws", "reqID", reqID, "fromID", senderID)
		return nil, chatmodel.ErrNotConnectedToWs
	}

	s.logger.Info("writing into connection", "reqID", reqID, "fromID", senderID)
	// need to change this to get the infriEvent
	err = s.hub.WriteIntoConnection(recieverID, chatmodel.Event{
		Event: "AddFri",
		Payload: chatmodel.OutFriEvent{
			ReqID:  friReqID.String(),
			FromID: senderID.String(),
		},
	})
	if err != nil {
		s.logger.Info("failed to parse into bytes", "reqID", reqID, "fromID", senderID, "toID", recieverID)
		return nil, err
	}

	publishCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()
	s.logger.Info("publishing friend request to MQ", "reqID", reqID, "friReqID", friReqID)

	job := &FriendReq{
		ReqID:  friReqID,
		FromID: senderID,
		ToID:   recieverID,
	}
	//	need to publish the job for db
	err = s.mainMq.PublishWithContext(publishCtx, SendRequest, job)
	if err != nil {
		s.logger.Error("failed to publish friend request to MQ", "reqID", reqID, "error", err)
		handleMqFail(SendRequest, job, err, s.logger)
		return &friReqID, err
	}

	s.logger.Info("friend request sent successfully", "reqID", reqID, "friReqID", friReqID)
	return &friReqID, nil
}

// this need to return error for failed case didn't do any of that
func (s *userService) ConfirmFriendReq(ctx context.Context, fromID, reqID uuid.UUID, status string) error {
	reqIDVal, _ := middleware.GetContextKey(ctx, "request")

	s.logger.Info("confirm friend request started", "reqID", reqIDVal, "fromID", fromID, "reqID", reqID)

	// this gets the opp userID  of the current one
	toID := s.userCache.GetOtherUserIDByReqID(fromID, reqID, "pending")
	if toID == nil {
		s.logger.Warn("cache miss for confirm friend request, fetching from DB", "reqID", reqIDVal, "fromID", fromID, "reqID", reqID)
		// cache miss db fetch
		user, err := s.userRepo.GetOtherUserIDByReqID(ctx, fromID, reqID)
		if err != nil {
			if exists := strings.ContainsAny(err.Error(), "no rows"); exists {
				s.logger.Error("failed to get the otherUserInfo", "reqID", reqIDVal, "fromID", fromID, "error", err)
				return ErrNoRedFound
			}
			s.logger.Error("failed to get the otherUserInfo", "reqID", reqIDVal, "fromID", fromID, "error", err)
			return err
		}
		s.userCache.UpdateUserCache(user) // update the cache
		toID = &FriendMetaData{
			UserID: user.ID,
			Name:   user.Name,
		}

	}
	exist := s.userCache.CheckUserFriWithOtherUserID(fromID, toID.UserID)
	if exist {
		return ErrReqExist
	}

	s.logger.Info("cleaning up pending/send requests and adding friends", "reqID", reqIDVal, "fromID", fromID, "toID", toID.UserID)

	// this update the pending guy
	s.userCache.CleanUpUserRs(&CacheRsDeleteStruct{
		UserID: fromID,
		ReqID:  reqID,
		Lable:  "pending",
	})

	// this update the sending guy
	s.userCache.CleanUpUserRs(&CacheRsDeleteStruct{
		UserID: toID.UserID,
		ReqID:  reqID,
		Lable:  "send",
	})

	// this update the pending guy
	s.userCache.UpdateUserRs(CacheUpdateFriStruct{
		UserID: fromID,
		ToID:   *toID,
		Lable:  "friend",
	})
	getFromName := s.userCache.GetUserNameByID(fromID)
	if getFromName == "" {
		s.logger.Warn("sender name not found in cache during confirm", "reqID", reqIDVal, "fromID", fromID)
	}
	s.userCache.UpdateUserRs(CacheUpdateFriStruct{
		UserID: toID.UserID,
		ToID: FriendMetaData{
			UserID: fromID,
			Name:   getFromName,
		},
		Lable: "friend",
	})
	s.logger.Info("checking the ws connection ...", "reqID", reqIDVal, "fromID", fromID)
	valid := s.hub.CheckWsConnection(fromID)
	if !valid {
		s.logger.Warn("the client is not connected to ws", "reqID", reqIDVal, "fromID", fromID)
		return chatmodel.ErrNotConnectedToWs
	}

	s.logger.Info("writing into connection", "reqID", reqIDVal, "fromID", fromID)
	// need to change this to get the infriEvent
	err := s.hub.WriteIntoConnection(toID.UserID, chatmodel.Event{
		Event: "AcceptFri",
		Payload: chatmodel.OutFriEvent{
			ReqID:  reqID.String(),
			FromID: fromID.String(),
		},
	})
	if err != nil {
		s.logger.Info("failed to parse into bytes", "reqID", reqIDVal, "fromID", fromID, "toID", toID.UserID)
		return err
	}

	context, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()
	job := &FriendReq{
		FromID:     fromID,
		ReqID:      reqID,
		UpdateTime: time.Now(),
	}
	err = s.mainMq.PublishWithContext(context, ConfirmFriendReq, job)
	if err != nil {
		handleMqFail(ConfirmFriendReq, job, err, s.logger)
		return err
	}
	return nil
}

// need to handle the errorr from that PublishWithContext
func (s *userService) CancelFriReq(ctx context.Context, userID, reqID uuid.UUID) error {
	reqIDVal, _ := middleware.GetContextKey(ctx, "request")

	s.logger.Info("confirm friend request started", "reqID", reqIDVal, "fromID", userID, "reqID", reqID)
	toID := s.userCache.GetOtherUserIDByReqID(userID, reqID, "pending")

	if toID == nil {
		s.logger.Warn("faield to get the toID from cache", "error", "toID is nil")
		user, err := s.userRepo.GetOtherUserIDByReqID(ctx, userID, reqID)
		if err != nil {
			if exists := strings.ContainsAny(err.Error(), "no rows"); exists {
				s.logger.Error("failed to get the otherUserInfo", "reqID", reqIDVal, "fromID", userID, "error", err)
				return ErrNoRedFound
			}
			s.logger.Error("failed to get the otherUserInfo", "reqID", reqIDVal, "fromID", userID, "error", err)
			return err
		}
		s.userCache.UpdateUserCache(user) // update the cache
		toID = &FriendMetaData{
			UserID: user.ID,
			Name:   user.Name,
		}
	}

	exists := s.userCache.CheckUserFriWithOtherUserID(userID, toID.UserID)
	if exists {
		return ErrReqExist
	}
	// this update the pending guy
	s.userCache.CleanUpUserRs(&CacheRsDeleteStruct{
		UserID: userID,
		ReqID:  reqID,
		Lable:  "pending",
	})

	// this update the sending guy
	s.userCache.CleanUpUserRs(&CacheRsDeleteStruct{
		UserID: toID.UserID,
		ReqID:  reqID,
		Lable:  "send",
	})
	s.logger.Info("checking the ws connection ...", "reqID", reqIDVal, "fromID", userID)
	valid := s.hub.CheckWsConnection(userID)
	if !valid {
		s.logger.Warn("the client is not connected to ws", "reqID", reqIDVal, "fromID", userID)
		return chatmodel.ErrNotConnectedToWs
	}
	s.logger.Info("writing into connection", "reqID", reqIDVal, "fromID", userID)
	err := s.hub.WriteIntoConnection(toID.UserID, chatmodel.Event{
		Event: "DenyFri",
		Payload: chatmodel.OutFriEvent{
			ReqID:  reqID.String(),
			FromID: userID.String(),
		},
	})
	if err != nil {
		s.logger.Warn("failed to pare into bytes", "reqID", reqIDVal, "fromID", userID, "toID", toID.UserID)
		return err
	}

	context, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()
	job := &CancelFriendReq{
		FromID:     userID,
		ReqID:      reqID,
		UpdateTime: time.Now(),
	}
	err = s.mainMq.PublishWithContext(context, CancelReq, job)
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
		s.logger.Warn("faield to get the toID from cache", "error", "toID is nil")
		user, err := s.userRepo.GetOtherUserIDByReqID(ctx, userID, reqID)
		if err != nil {
			return err
		}
		s.userCache.UpdateUserCache(user) // update the cache
		toID = &FriendMetaData{
			UserID: user.ID,
			Name:   user.Name,
		}
	}
	// this update the sending guy
	s.userCache.CleanUpUserRs(&CacheRsDeleteStruct{
		UserID: userID,
		ReqID:  reqID,
		Lable:  "send",
	})

	// this update the pending guy
	s.userCache.CleanUpUserRs(&CacheRsDeleteStruct{
		UserID: toID.UserID,
		ReqID:  reqID,
		Lable:  "pending",
	})

	context, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()
	job := &DeleteFirReqStruct{
		ReqID:  reqID,
		FromID: userID,
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
// i didn't  handle the no row error
// need to update the cache after successfull db fetch
func (s *userService) GetFriendList(ctx context.Context, userID uuid.UUID) (*[]FriendMetaData, error) {
	// first need to get from the cache first
	list := s.userCache.GetUserFriList(userID) // maybe should just check whether the user exist or not first
	if list == nil {
		s.logger.Info("fetching from db because cache miss", "userID", userID)
		list, err := s.userRepo.GetUserFriListByID(ctx, userID)
		if err != nil {
			s.logger.Error("failed to get friend list from db", "error", err)
			return nil, err
		}
		var friendList []FriendMetaData
		if list != nil && len(*list) > 0 {
			for _, v := range *list {
				s.userCache.UpdateUserRs(CacheUpdateFriStruct{
					UserID: userID,
					ToID: FriendMetaData{
						UserID: v.FriendID,
						Name:   v.FriendName,
					},
					Lable: "friend",
				})
				friendList = append(friendList, FriendMetaData{
					UserID: v.FriendID,
					Name:   v.FriendName,
				})
			}
		}
		s.logger.Info("successfully fetched from db", "userID", userID)
		return &friendList, nil
	}
	return list, nil
}

// WARN:need to update the cache after fetching from db
func (s *userService) GetPendingList(ctx context.Context, userID uuid.UUID) (*GetReqList, error) {
	list := GetReqList{
		PendingIDsList: &map[uuid.UUID]FriendMetaData{},
		RequestIDsList: &map[uuid.UUID]FriendMetaData{},
	}
	check := s.userCache.GetUserRs(userID)
	if !check {
		s.logger.Debug("fetching from db because cache miss", "userID", userID)
		reqList, err := s.userRepo.GetMyFriReqList(ctx, userID)
		if err != nil {
			if err != sql.ErrNoRows {
				s.logger.Error("failed to get pending list from db", "error", err)
				return nil, err
			}
		}

		listOne := *list.PendingIDsList
		if reqList != nil {
			for _, v := range *reqList {
				listOne[v.ID] = FriendMetaData{
					UserID: v.UserID,
					Name:   v.Name.String,
				}
				s.userCache.UpdateUserRs(CacheUpdateStruct{
					UserID: userID,
					ReqID:  v.ID,
					OtherUserInfo: FriendMetaData{
						UserID: v.UserID,
						Name:   v.Name.String,
					},
					Lable: "pending",
				})
			}
		}
		reqSendList, err := s.userRepo.GetMySendFirReqList(ctx, userID)
		if err != nil {
			if err != sql.ErrNoRows {
				s.logger.Error("failed to get request send list from db", "error", err)
				return nil, err
			}
		}
		listTwo := *list.RequestIDsList
		if reqSendList != nil {
			for _, v := range *reqSendList {
				listTwo[v.ID] = FriendMetaData{
					UserID: v.OtheruserID,
					Name:   v.Name.String,
				}
				s.userCache.UpdateUserRs(CacheUpdateStruct{
					UserID: userID,
					ReqID:  v.ID,
					OtherUserInfo: FriendMetaData{
						UserID: v.OtheruserID,
						Name:   v.Name.String,
					},
					Lable: "send",
				})
			}
		}
		list.PendingIDsList = &listOne
		list.RequestIDsList = &listTwo

		return &list, nil
	}
	s.logger.Debug("fetching from cache", "userID", userID)
	pendingList := s.userCache.GetUserReqList(userID)
	if pendingList != nil {
		list.PendingIDsList = pendingList
		for k, v := range *list.PendingIDsList {
			s.logger.Debug("pending list", slog.String("reqID", k.String()), slog.String("fromID", v.UserID.String()))
		}
	}
	reqList := s.userCache.GetUserSendReqList(userID)
	if reqList != nil {
		list.RequestIDsList = reqList
	}
	return &list, nil
}

// tempory thing for mq fail
// maybe use helper one i don't konw have to think about this for sure
func saveIntoLog(jobName string, job interface{}, logger *slog.Logger) {
	saveLog := []byte(fmt.Sprintf("jobName:%v;jobDescrioption:%v;\n", jobName, job))
	path := filepath.Join("../../", "consistency_log.txt")
	f, err := os.Create(path) // create the path
	defer f.Close()
	if err != nil {
		logger.Error("file create failed", "error", err)
	}
	err = os.WriteFile(path, saveLog, 0o644)
	if err != nil {
		logger.Error("file wirte process failed", "error", err)
	}
}

func (s *userService) SearchUser(ctx context.Context, serachName string) (*[]User, error) {
	reqID, _ := middleware.GetContextKey(ctx, "request")

	s.logger.Info("search user started", "reqID", reqID, "searchName", serachName)

	userList, err := s.userRepo.GetMatchName(ctx, serachName)
	if err != nil {
		if err == sql.ErrNoRows {
			s.logger.Info("no users found", "reqID", reqID, "searchName", serachName)
			return &[]User{}, nil
		}
		s.logger.Error("failed to search users", "reqID", reqID, "error", err)
		return nil, err
	}

	err = s.mainMq.PublishWithContext(ctx, "updateUserCache", userList)
	if err != nil {
		handleMqFail("updateUserCache", *userList, err, s.logger)
	}

	s.logger.Info("search user completed", "reqID", reqID, "searchName", serachName, "count", len(*userList))

	return userList, nil
}
