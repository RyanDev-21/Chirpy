package chat

import (
	"context"
	"errors"
	"fmt"
	"time"

	chatmodel "RyanDev-21.com/Chirpy/internal/chatModel"
	"RyanDev-21.com/Chirpy/internal/database"
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

func getPayload(userID, msgID uuid.UUID, msg *chatmodel.InCommingMessage) *chatmodel.MessageMetaData {
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
		s.logger.Error("failed to store message into cache", "err", err)
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
		s.logger.Error("failed to store group message into cache", "err", err)
		return err
	}
	return nil
}

// this is just helper fucntion to pub the job with context
func (s *chatService) publishJobHelper(job string, payload interface{}) {
	context, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	err := s.mq.PublishWithContext(context, job, payload)
	if err != nil {
		s.logger.Error("failed to publish to message queue", "err", err, "job", job)
	}
}

func convertFromMessageToMeta(msg database.Message) *chatmodel.MessageMetaData {
	return &chatmodel.MessageMetaData{
		ID: msg.ID,
		MsgInfo: &chatmodel.MessageCache{
			FromID: uuid.UUID(msg.FromID.Bytes),
			Msg: chatmodel.InCommingMessage{
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
			Msg: chatmodel.InCommingMessage{
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
