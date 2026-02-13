package chat

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"time"

	chatmodel "RyanDev-21.com/Chirpy/internal/chatModel"
	"RyanDev-21.com/Chirpy/internal/database"
	"RyanDev-21.com/Chirpy/pkg/helper"
	"github.com/google/uuid"
)

func getChatKey(firstID, secondID uuid.UUID) string {
	if firstID.String() < secondID.String() {
		return fmt.Sprintf("%v_%v", firstID, secondID)
	}
	return fmt.Sprintf("%v_%v", secondID, firstID)
}

// func updateChatCache(msgList *[]chatmodel.MessageMetaData)error{
// 	for
// 	return nil
// }

func getPayload(userID, msgID uuid.UUID, msg *chatmodel.Message) *chatmodel.MessageMetaData {
	return &chatmodel.MessageMetaData{
		ID: msgID,
		MsgInfo: &chatmodel.MessageCache{
			FromID: userID,
			Msg:    *msg,
		},
	}
}

// gen the unique msgID and store in cache and db
// this one needs a parseID as the chatID need to generate and stuff
func (s *chatService) handlePrivateMsg(ctx context.Context, userID, parseID uuid.UUID, msg *chatmodel.MessageMetaData) error {
	key := getChatKey(userID, parseID)
	redisKey := s.rediscache.generateRedisKey(userID, key)
	err := s.rediscache.addMessage(ctx, redisKey, msg)
	if err != nil {
		log.Printf("failed to store into the cache \n #%s#", err)
		return err
	}
	// need to send into hub sent
	return nil
}

// gen msgID and store it in cache and group db
func (s *chatService) handlePublicMsg(ctx context.Context, userID uuid.UUID, msg *chatmodel.MessageMetaData) error {
	chatID := msg.MsgInfo.Msg.ToID
	key := s.rediscache.generateRedisKey(userID, chatID)
	err := s.rediscache.addMessage(ctx, key, msg)
	if err != nil {
		log.Printf("failed to stor into the cache \n #%s#", err)
		return err
	}
	return nil
}

// this is just helper fucntion to pub the job with context
func (s *chatService) publishJobHelper(job string, payload interface{}) {
	// dummy logger for now
	logger := slog.Default()
	context, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	err := s.mq.PublishWithContext(context, job, payload)
	if err != nil {
		log.Printf("failed to connect to main queue #%s#", err)
		helper.SaveIntoLog(job, payload, logger)
	}
}

func convertFromMessageToMeta(msg database.Message) *chatmodel.MessageMetaData {
	return &chatmodel.MessageMetaData{
		ID: msg.ID,
		MsgInfo: &chatmodel.MessageCache{
			FromID: uuid.UUID(msg.FromID.Bytes),
			Msg: chatmodel.Message{
				ToID:     msg.ToID.String(),
				Content:  msg.Content.String,
				ParendID: msg.Parentid.String(),
				Type:     "private",
			},
		},
	}
}

func convertFromGroupMessageToMeta(msg database.Groupmessage) *chatmodel.MessageMetaData {
	return &chatmodel.MessageMetaData{
		ID: msg.ID,
		MsgInfo: &chatmodel.MessageCache{
			FromID: uuid.UUID(msg.FromID.Bytes),
			Msg: chatmodel.Message{
				ToID:     msg.GroupID.String(),
				Content:  msg.Content.String,
				ParendID: msg.ParentID.String(),
				Type:     "public",
			},
		},
	}
}

func convertToMsgMetaList[T any](msgList *[]T) (*chatmodel.MessageListRes, error) {
	var msgMetaList []chatmodel.MessageMetaDataRes
	for _, v := range *msgList {
		var payload *chatmodel.MessageMetaDataRes
		switch value := any(v).(type) {
		case database.Message:
			msgMetaData := convertFromMessageToMeta(value)
			payload = &chatmodel.MessageMetaDataRes{
				ID:      msgMetaData.ID,
				MsgInfo: *msgMetaData.MsgInfo,
			}
		case database.Groupmessage:
			msgMetaData := convertFromGroupMessageToMeta(value)
			payload = &chatmodel.MessageMetaDataRes{
				ID:      msgMetaData.ID,
				MsgInfo: *msgMetaData.MsgInfo,
			}
		default:
			return nil, errors.New("not supported type")
		}
		msgMetaList = append(msgMetaList, *payload)
	}

	return &chatmodel.MessageListRes{
		MsgList: msgMetaList,
	}, nil
}
