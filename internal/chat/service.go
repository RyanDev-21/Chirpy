package chat

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"time"

	chatmodel "RyanDev-21.com/Chirpy/internal/chatModel"
	mq "RyanDev-21.com/Chirpy/internal/customMq"
	"RyanDev-21.com/Chirpy/internal/database"
	"RyanDev-21.com/Chirpy/internal/groups"
	"RyanDev-21.com/Chirpy/pkg/middleware"

	// rabbitmq "RyanDev-21.com/Chirpy/internal/rabbitMq"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
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
	fetchMessagePrivate(ctx context.Context, userID, toID uuid.UUID, since string) (*chatmodel.MessageListRes, error) // the chatID will be otherUserID if private
	fetchMessagePublic(ctx context.Context, userID, toID uuid.UUID) (*chatmodel.MessageListRes, error)                // the chatID will be otherUserID if private
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

// sendMessage validates the payload, delivers it over the hub, caches it and
// queues the database write.
func (s *chatService) sendMessage(ctx context.Context, userID uuid.UUID, payload *chatmodel.InCommingMessage) (*uuid.UUID, error) {
	reqIDVal, _ := middleware.GetContextKey(ctx, "request")
	s.logger.Info("send message request started", "reqID", reqIDVal, "fromID", userID)

	toID, err := uuid.Parse(payload.ToID)
	if err != nil {
		return nil, chatmodel.ErrNotValidToID
	}
	if payload.ParendID != "" {
		if _, err = uuid.Parse(payload.ParendID); err != nil {
			return nil, chatmodel.ErrNotValidParentID
		}
	}
	switch payload.Type {
	case "private":
		if toID == userID {
			return nil, chatmodel.ErrNotValidToID
		}
	case "public":
		if !s.groupCache.CheckNameFromGroup(toID, userID) {
			return nil, chatmodel.ErrNotAuthorized
		}
	default:
		return nil, ErrNotSupportedTypeMsg
	}

	if !s.hub.CheckWsConnection(userID) {
		return nil, chatmodel.ErrNotConnectedToWs
	}

	msgID, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	msgMeta := getPayload(userID, msgID, payload)

	targets := []uuid.UUID{toID}
	if payload.Type == "public" {
		members, err := s.groupCache.GetFromMember(toID)
		if err != nil {
			return nil, err
		}
		targets = slices.DeleteFunc(members, func(id uuid.UUID) bool { return id == userID })
	}
	if err := s.hub.WriteToUsers(targets, chatmodel.Event{
		Event: "msg",
		Payload: chatmodel.OutGoingMessage{
			ID:        msgID.String(),
			Content:   payload.Content,
			FromID:    userID.String(),
			ParentID:  payload.ParendID,
			Type:      payload.Type,
			CreatedAt: msgMeta.MsgInfo.CreatedAt,
		},
	}); err != nil {
		s.logger.Error("failed to write into connection", "reqID", reqIDVal, "fromID", userID, "err", err)
		return nil, err
	}

	if payload.Type == "private" {
		if err := s.handlePrivateMsg(ctx, userID, toID, msgMeta); err != nil {
			return nil, err
		}
		s.publishJobHelper(chatmodel.PrivateMessageConstant, *msgMeta)
	} else {
		if err := s.handlePublicMsg(ctx, userID, msgMeta); err != nil {
			return nil, err
		}
		s.publishJobHelper(chatmodel.PublicMessageConstant, *msgMeta)
	}
	return &msgID, nil
}

func (s *chatService) fetchMessagePrivate(ctx context.Context, userID, toID uuid.UUID, since string) (*chatmodel.MessageListRes, error) {
	var sinceTime time.Time
	if since != "" {
		t, err := convertTimeFromString(since)
		if err != nil {
			return nil, err
		}
		sinceTime = t
	}
	redisKey := s.rediscache.generateRedisKey(getChatKey(userID, toID), "private")

	msgList, err := s.rediscache.getMessages(ctx, redisKey, sinceTime)
	if err == nil && len(msgList) > 0 {
		return &chatmodel.MessageListRes{MsgList: msgList}, nil
	}
	if err != nil {
		s.logger.Warn("chat cache read failed, falling back to db", "err", err)
	}

	var messages *[]database.Message
	if sinceTime.IsZero() {
		messages, err = s.chatRepo.getMessagesForPrivate(ctx, userID, toID)
	} else {
		messages, err = s.chatRepo.getMessagesForPrivateWithTime(ctx, userID, toID, sinceTime)
	}
	if err != nil {
		return nil, err
	}
	// only warm the cache with a full history read, otherwise the stream
	// would be seeded with a partial window
	if sinceTime.IsZero() {
		for _, v := range *messages {
			if err := s.rediscache.addMessage(ctx, redisKey, convertFromMessageToMeta(v)); err != nil {
				s.logger.Warn("failed to cache message", "err", err)
				break
			}
		}
	}
	return convertToMsgMetaList(messages)
}

func convertTimeFromString(timeStr string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, timeStr); err == nil {
		return t, nil
	}
	t, err := time.Parse(time.RFC3339, timeStr)
	if err != nil {
		return time.Time{}, chatmodel.ErrNotValidTimeFmt
	}
	return t, nil
}

func (s *chatService) fetchMessagePublic(ctx context.Context, userID, toID uuid.UUID) (*chatmodel.MessageListRes, error) {
	if !s.groupCache.CheckNameFromGroup(toID, userID) {
		return nil, chatmodel.ErrNotAuthorized
	}
	redisKey := s.rediscache.generateRedisKey(toID.String(), "public")
	msgList, err := s.rediscache.getMessages(ctx, redisKey, time.Time{})
	if err == nil && len(msgList) > 0 {
		return &chatmodel.MessageListRes{MsgList: msgList}, nil
	}
	if err != nil {
		s.logger.Warn("group cache read failed, falling back to db", "err", err)
	}
	messages, err := s.chatRepo.getMessagesForPublic(ctx, toID)
	if err != nil {
		return nil, err
	}
	for _, v := range *messages {
		if err := s.rediscache.addMessage(ctx, redisKey, convertFromGroupMessageToMeta(v)); err != nil {
			s.logger.Warn("failed to cache group message", "err", err)
			break
		}
	}
	return convertToMsgMetaList(messages)
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
	err = s.mq.PublishWithContext(ctx, chatmodel.SeenMessageConstant, payloadForJob)
	if err != nil {
		s.logger.Error("failed to publish to main queue", "reqID", reqIDVal, "userID", userID, "error", err)
		return err
	}
	return nil
}
