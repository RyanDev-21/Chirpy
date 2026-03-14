package chat

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	chatmodel "RyanDev-21.com/Chirpy/internal/chatModel"
	mq "RyanDev-21.com/Chirpy/internal/customMq"
	"RyanDev-21.com/Chirpy/internal/database"
	"RyanDev-21.com/Chirpy/internal/groups"
	"RyanDev-21.com/Chirpy/pkg/middleware"

	// rabbitmq "RyanDev-21.com/Chirpy/internal/rabbitMq"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type ChatService interface {
	upgradeWebsocket(w http.ResponseWriter, r *http.Request) (*websocket.Conn, error)
	initWs(conn *websocket.Conn, userID uuid.UUID)
	//	createGroup(userID uuid.UUID)
	sendMessage(ctx context.Context, userID uuid.UUID, paylod *chatmodel.InCommingMessage) (*uuid.UUID, error)
	fetchMessagePrivate(ctx context.Context, userID, toID uuid.UUID,since string) (*chatmodel.MessageListRes, error) // the chatID will be otherUserID if private
	fetchMessagePublic(ctx context.Context, userID, toID uuid.UUID) (*chatmodel.MessageListRes, error)  // the chatID will be otherUserID if private
	updateLastSeen(ctx context.Context, userID uuid.UUID, payload *chatmodel.InCommingEventForSeen) error
	StartWorkerForAddPrivateMessage(channel chan *mq.Channel)
	StartWorkerForAddPublicMessage(channel chan *mq.Channel)
	StartWorkerForUpdateSeen(channel chan *mq.Channel)
}
type chatService struct {
	chatRepo   ChatRepo
	hub        *chatmodel.Hub
	mq         *mq.MainMQ
	rediscache ChatRepoCache
	groupCache *groups.Cache
	logger     *slog.Logger
}

var (
	ErrNotSupportedTypeMsg = errors.New("not supportted type of message")
	ErrNOtFoundClient      = errors.New("not found client")
)

func NewChatService(chatRepo ChatRepo, hub *chatmodel.Hub, mq *mq.MainMQ, cache ChatRepoCache, groupCache *groups.Cache, logger *slog.Logger) ChatService {
	return &chatService{
		chatRepo:   chatRepo,
		hub:        hub,
		mq:         mq,
		rediscache: cache,
		groupCache: groupCache,
		logger:     logger,
	}
}

func (s *chatService) initWs(conn *websocket.Conn, userID uuid.UUID) {
	client := chatmodel.NewClient(s.hub, conn, make(chan []byte, 256), userID)
	client.Hub.Register <- client

	go client.WritePump()
	go client.ReadPump()
}

func (s *chatService) upgradeWebsocket(w http.ResponseWriter, r *http.Request) (*websocket.Conn, error) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

// send the message struct based on the toId
// WARN : should consider validating the toID
func (s *chatService) sendMessage(ctx context.Context, userID uuid.UUID, payload *chatmodel.InCommingMessage) (*uuid.UUID, error) {
	reqIDVal, _ := middleware.GetContextKey(ctx, "request")

	s.logger.Info("send Message request started", "reqID", reqIDVal, "fromID", userID)

	toID, err := uuid.Parse(payload.ToID)
	if err != nil {
		return nil, chatmodel.ErrNotValidToID
	}

	if payload.ParendID != "" {
		_, err = uuid.Parse(payload.ParendID)
		if err != nil {
			return nil, errors.New("not valid parentID(type uuid)")
		}
	}
	s.logger.Info("checking the connection first ", "reqID", reqIDVal, "fromID", userID)
	valid := s.hub.CheckWsConnection(userID)
	if !valid {
		return nil, chatmodel.ErrNotConnectedToWs
	}

	msgID, err := uuid.NewUUID()
	if err != nil {
		return nil, err
	}

	// i need somehow get the client connection and then use the send one
	s.logger.Info("trying to write into connection ", "reqID", reqIDVal, "fromID", userID)
	err = s.hub.WriteIntoConnection(toID, chatmodel.Event{
		Event: "msg",
		Payload: chatmodel.OutGoingMessage{
			ID:        msgID.String(),
			Content:   payload.Content,
			FromID:    userID.String(),
			ParentID:  payload.ParendID,
			Type:      payload.Type,
			CreatedAt: time.Now().UTC(),
		},
	})
	if err != nil {
		s.logger.Error("failed to parse into bytes", "reqID", reqIDVal, "fromID", userID)
		return nil, err
	}
	msgMeta := getPayload(userID, msgID, payload)
	// handle the reply and stuff
	switch payload.Type {
	case "private":
		err := s.handlePrivateMsg(ctx, userID, toID, msgMeta) // update the cache
		if err != nil {
			s.logger.Error("failed to update the  cache", "reqID", reqIDVal, "fromID", userID, "error", err)
			return nil, err
		}
		s.publishJobHelper(chatmodel.PrivateMessageConstant, *msgMeta) // upadate the db

	case "public":
		err := s.handlePublicMsg(ctx, userID, msgMeta)
		if err != nil {
			s.logger.Error("failed to update the  cache", "reqID", reqIDVal, "fromID", userID, "error", err)
			return nil, err
		}

		s.publishJobHelper(chatmodel.PublicMessageConstant, *msgMeta)

	default:
		return nil, ErrNotSupportedTypeMsg
	}
	return &msgID, err
}

func (s *chatService) fetchMessagePrivate(ctx context.Context, userID, toID uuid.UUID,since string) (*chatmodel.MessageListRes, error) {
	key := getChatKey(userID, toID)
	redisKey := s.rediscache.generateRedisKey(key, "private")

	msgList, err := s.getMessagesFromCache(ctx, redisKey,since)
	if err != nil || msgList == nil || len(*msgList) == 0 {
		s.logger.Debug("cache miss or empty, fetching from DB", "err", err)
		
		message, err := s.getMessagesFromRepoPrivate(ctx,userID,toID,since)
		if err != nil {
			return nil, err
		}
		s.logger.Debug("fetched messages from DB", "count", len(*message))
		for _, v := range *message {
			err := s.rediscache.addMessage(ctx, redisKey, convertFromMessageToMeta(v))
			if err != nil {
				s.logger.Warn("failed to cache message", "err", err)
			}
		}
		msgMetaList, err := convertToMsgMetaList(message)
		if err != nil {
			return nil, err
		}
		return msgMetaList, nil
	}
	return &chatmodel.MessageListRes{
		MsgList: *msgList,
	}, nil
}

func (s *chatService)getMessagesFromCache(ctx context.Context,redisKey string,since string)(*[]chatmodel.MessageMetaDataRes,error){
	if since == ""{
		msgList,err:= s.rediscache.getMessages(ctx,redisKey)
		if err!=nil{
			return nil,err
		}
		return msgList,nil
	}
	timeFmt,err:= convertTimeFromString(since)
	if err !=nil{
		return nil,chatmodel.ErrNotValidTimeFmt
	}
	msgList,err:= s.rediscache.getMessagesWithTime(ctx,redisKey,timeFmt)	
	if err !=nil{
		return nil,err
	}
	return msgList,nil
}

func (s *chatService)getMessagesFromRepoPrivate(ctx context.Context,userID,toID uuid.UUID,since string)(*[]database.Message,error){
	if since == ""{
		msgList,err:=s.chatRepo.getMessagesForPrivate(ctx,userID,toID)	
		if err !=nil{
			return nil,err
		}
		return msgList,nil
	}
	timeFmt,err:=convertTimeFromString(since)
	if err!=nil{
		return nil,chatmodel.ErrNotValidTimeFmt
	}
	msgList,err:=s.chatRepo.getMessagesForPrivateWithTime(ctx,userID,toID,timeFmt)
	if err !=nil{
		return msgList,err
	}
	return msgList,nil

	}


func convertTimeFromString(timeStr string)(time.Time,error){
	timeFmt,err:=time.Parse(time.RFC3339Nano,timeStr)	
	if err !=nil{
		timeFmtFB,err:= time.Parse(time.RFC3339,timeStr)
		if err !=nil{
			return timeFmtFB,chatmodel.ErrNotValidTimeFmt
		}		
		return timeFmtFB,nil
	}
	return timeFmt,nil
}

// func (s *chatService)fetchMessagePrivateWithSince(ctx context.Context,userID,toID uuid.UUID,since time.Time)(*chatmodel.MessageListRes,error){
//
// }

// need a way to check the member in the group or not
func (s *chatService) fetchMessagePublic(ctx context.Context, userID, toID uuid.UUID) (*chatmodel.MessageListRes, error) {
	valid := s.groupCache.CheckNameFromGroup(toID, userID)
	if !valid {
		return nil, chatmodel.ErrNotAuthorized
	}
	rediskey := s.rediscache.generateRedisKey(toID.String(), "public")
	msgList, err := s.rediscache.getMessages(ctx, rediskey)
	if err != nil {
		if errors.Is(err, redis.Nil) { // if miss ,db hit
			// fetch from db
			message, err := s.chatRepo.getMessagesForPublic(ctx, toID)
			if err != nil {
				return nil, err
			}
			// update the cache
			for _, v := range *message {
				err := s.rediscache.addMessage(ctx, rediskey, convertFromGroupMessageToMeta(v))
				if err != nil {
					return nil, err
				}
			}
			// convert to list type
			msgMetaList, err := convertToMsgMetaList(message)
			if err != nil {
				return nil, err
			}

			return msgMetaList, nil
		}
	}

	return &chatmodel.MessageListRes{
		MsgList: *msgList,
	}, nil
}

func (s *chatService) updateLastSeen(ctx context.Context, userID uuid.UUID, payload *chatmodel.InCommingEventForSeen) error {
	reqIDVal, _ := middleware.GetContextKey(ctx, "request")
	s.logger.Info("send Message request started", "reqID", reqIDVal, "fromID", userID)
	parseID, err := uuid.Parse(payload.ToID)
	if err != nil {
		s.logger.Warn("failed to parse toID", "reqID", reqIDVal, "fromID", userID)

		return chatmodel.ErrNotValidUUID
	}
	parseMsgID, err := uuid.Parse(payload.MsgID)
	if err != nil {
		s.logger.Error("failed to parse msgID", "reqID", reqIDVal, "fromID", userID)

		return chatmodel.ErrNotValidUUID
	}
	chatID := getChatKey(userID, parseID)
	key := s.rediscache.generateRedisKeyForSeen(chatID, userID)
	err = s.rediscache.UpdateLastSeen(ctx, key, parseMsgID)
	if err != nil {
		s.logger.Error("failed to update the seen cache", "reqID", reqIDVal, "fromID", userID, "error", err)
		return err
	}
	s.logger.Info("background job posting started", "reqID", reqIDVal, "fromID", userID)
	payloadForJob := chatmodel.JobForSeen{
		MsgID:  parseMsgID,
		SeenID: userID,
		ChatID: chatID,
	}
	err = s.mq.PublishWithContext(ctx, "JobForSeen", payloadForJob)
	if err != nil {
		s.logger.Error("failed to publish to main queue", "reqID", reqIDVal, "userID", userID, "error", err)
		return err
	}
	return nil
}
