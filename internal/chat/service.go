package chat

import (
	"context"
	"errors"

	//	"log"
	"net/http"

	chatmodel "RyanDev-21.com/Chirpy/internal/chatModel"
	mq "RyanDev-21.com/Chirpy/internal/customMq"
	"RyanDev-21.com/Chirpy/internal/groups"

	// rabbitmq "RyanDev-21.com/Chirpy/internal/rabbitMq"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

type ChatService interface {
	upgradeWebsocket(w http.ResponseWriter, r *http.Request) (*websocket.Conn, error)
	initWs(conn *websocket.Conn, userID uuid.UUID)
	//	createGroup(userID uuid.UUID)
	sendMessage(ctx context.Context, userID uuid.UUID, paylod *chatmodel.Message) (*uuid.UUID, error)
	fetchMessagePrivate(ctx context.Context, userID, toID uuid.UUID) (*chatmodel.MessageListRes, error) // the chatID will be otherUserID if private
	fetchMessagePublic(ctx context.Context, userID, toID uuid.UUID) (*chatmodel.MessageListRes, error)  // the chatID will be otherUserID if private
	StartWorkerForAddPrivateMessage(channel chan *mq.Channel)
	StartWorkerForAddPublicMessage(channel chan *mq.Channel)
}
type chatService struct {
	chatRepo   ChatRepo
	hub        *chatmodel.Hub
	mq         *mq.MainMQ
	rediscache ChatRepoCache
	groupCache *groups.Cache
}

var (
	ErrNotSupportedTypeMsg = errors.New("not supportted type of message")
	ErrNOtFoundClient      = errors.New("not found client")
)

func NewChatService(chatRepo ChatRepo, hub *chatmodel.Hub, mq *mq.MainMQ, cache ChatRepoCache, groupCache *groups.Cache) ChatService {
	return &chatService{
		chatRepo:   chatRepo,
		hub:        hub,
		mq:         mq,
		rediscache: cache,
		groupCache: groupCache,
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
func (s *chatService) sendMessage(ctx context.Context, userID uuid.UUID, payload *chatmodel.Message) (*uuid.UUID, error) {
	toID, err := uuid.Parse(payload.ToID)
	if err != nil {
		return nil, chatmodel.ErrNotValidToID
	}

	var parentParseID *uuid.UUID
	// there might be a better way to write this
	if payload.ParendID != "" {
		*parentParseID, err = uuid.Parse(payload.ParendID)
		if err != nil {
			return nil, errors.New("not valid parentID(type uuid)")
		}
	}

	// i need to somehow get the client connection and then use the send one
	err = s.hub.WriteIntoConnection(userID, payload)
	if err != nil {
		return nil, err
	}

	msgID, err := uuid.NewUUID()
	if err != nil {
		return nil, err
	}
	msgMeta := getPayload(userID, msgID, payload)
	// handle the reply and stuff
	switch payload.Type {
	case "private":
		err := s.handlePrivateMsg(ctx, userID, toID, msgMeta) // update the cache
		if err != nil {
			return nil, err
		}
		s.publishJobHelper(chatmodel.PrivateMessageConstant, *msgMeta) // upadate the db

	case "public":
		err := s.handlePublicMsg(ctx, userID, msgMeta)
		if err != nil {
			return nil, err
		}

		s.publishJobHelper(chatmodel.PublicMessageConstant, *msgMeta)

	default:
		return nil, ErrNotSupportedTypeMsg
	}
	return &msgID, err
}

func (s *chatService) fetchMessagePrivate(ctx context.Context, userID, toID uuid.UUID) (*chatmodel.MessageListRes, error) {
	key := getChatKey(userID, toID)
	redisKey := s.rediscache.generateRedisKey(userID, key)
	msgList, err := s.rediscache.getMessages(ctx, redisKey)
	if err != nil {
		if errors.Is(err, redis.Nil) { // if miss,db hit
			// need to fetchMessage from db
			message, err := s.chatRepo.GetMessagesForPrivate(ctx, userID, toID)
			if err != nil {
				return nil, err
			}
			// update the cache
			for _, v := range *message {
				err := s.rediscache.addMessage(ctx, redisKey, convertFromMessageToMeta(v))
				if err != nil {
					return nil, err
				}
			}
			// type convertion to return
			msgMetaList, err := convertToMsgMetaList(message)
			if err != nil {
				return nil, err
			}
			return msgMetaList, nil

		}
		return nil, err
	}
	return &chatmodel.MessageListRes{
		MsgList: *msgList,
	}, nil
}

// need a way to check the member in the group or not
func (s *chatService) fetchMessagePublic(ctx context.Context, userID, toID uuid.UUID) (*chatmodel.MessageListRes, error) {
	valid := s.groupCache.CheckNameFromGroup(toID, userID)
	if !valid {
		return nil, chatmodel.ErrNotAuthorized
	}
	rediskey := s.rediscache.generateRedisKey(userID, toID.String())
	msgList, err := s.rediscache.getMessages(ctx, rediskey)
	if err != nil {
		if errors.Is(err, redis.Nil) { // if miss ,db hit
			// fetch from db
			message, err := s.chatRepo.GetMessagesForPublic(ctx, toID)
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
