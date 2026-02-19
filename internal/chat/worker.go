package chat

import (
	chatmodel "RyanDev-21.com/Chirpy/internal/chatModel"
	mq "RyanDev-21.com/Chirpy/internal/customMq"
	"RyanDev-21.com/Chirpy/internal/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// parentId is string type from json
// we need to make it nil so that the db will not take this value
// cuz in db i make the parentID optional
// NOTE::maybe there is a better way to do this
func ParentIdIdentifier(parentID string) *uuid.UUID {
	var fakeID *uuid.UUID
	if parentID == "" {
		fakeID = nil
		return fakeID
	}
	*fakeID = uuid.MustParse(parentID)
	return fakeID
}

func getPayLoadForAddMessagePrivate(msgMetaData chatmodel.MessageMetaData, parentID *uuid.UUID) *database.AddMessagePrivateParams {
	if parentID != nil {
		return &database.AddMessagePrivateParams{
			ID:       msgMetaData.ID,
			Parentid: *GetUUIDType(*parentID),
			Content:  *GetStringType(msgMetaData.MsgInfo.Msg.Content),
			FromID:   *GetUUIDType(msgMetaData.MsgInfo.FromID),
			ToID:     *GetUUIDType(uuid.MustParse(msgMetaData.MsgInfo.Msg.ToID)),
		}
	}
	return &database.AddMessagePrivateParams{
		ID:      msgMetaData.ID,
		Content: *GetStringType(msgMetaData.MsgInfo.Msg.Content),
		FromID:  *GetUUIDType(msgMetaData.MsgInfo.FromID),
		ToID:    *GetUUIDType(uuid.MustParse(msgMetaData.MsgInfo.Msg.ToID)),
	}
}

func getPayLoadForAddMessagePublic(msgMetaData chatmodel.MessageMetaData, parentID *uuid.UUID) *database.AddMessagePublicParams {
	if parentID != nil {
		return &database.AddMessagePublicParams{
			ID:       msgMetaData.ID,
			ParentID: *GetUUIDType(*parentID),
			Content:  *GetStringType(msgMetaData.MsgInfo.Msg.Content),
			FromID:   *GetUUIDType(msgMetaData.MsgInfo.FromID),
			GroupID:  *GetUUIDType(uuid.MustParse(msgMetaData.MsgInfo.Msg.ToID)),
		}
	}
	return &database.AddMessagePublicParams{
		ID:      msgMetaData.ID,
		Content: *GetStringType(msgMetaData.MsgInfo.Msg.Content),
		FromID:  *GetUUIDType(msgMetaData.MsgInfo.FromID),
		GroupID: *GetUUIDType(uuid.MustParse(msgMetaData.MsgInfo.Msg.ToID)),
	}
}

// this one will store the msg id and its info into db
func (s *chatService) StartWorkerForAddPrivateMessage(channel chan *mq.Channel) {
	for chen := range channel {
		msg := chen.Msg.(chatmodel.MessageMetaData)
		parentID := ParentIdIdentifier(msg.MsgInfo.Msg.ParendID)

		// this one stores into message table
		payload := getPayLoadForAddMessagePrivate(msg, parentID)
		_, err := s.chatRepo.AddMessagePrivate(payload)
		if err != nil {
			if pgErr, ok := err.(*pgconn.PgError); ok {
				if pgErr.Code == "23503" {
					s.logger.Error("foreign key constraint error, dropping job", "err", err)
					break
				}
			}
			s.logger.Error("failed to add private message to database", "err", err)
			chen.RetriesCount++
			s.mq.Republish(chen, chen.RetriesCount)
			continue
		}
		s.logger.Debug("successfully added private message to database")
	}
}

func (s *chatService) StartWorkerForAddPublicMessage(channel chan *mq.Channel) {
	for chen := range channel {
		msg := chen.Msg.(chatmodel.MessageMetaData)
		parentID := ParentIdIdentifier(msg.MsgInfo.Msg.ParendID)
		payload := getPayLoadForAddMessagePublic(msg, parentID)
		_, err := s.chatRepo.AddMessagePublic(payload)
		if err != nil {
			if pgErr, ok := err.(*pgconn.PgError); ok {
				if pgErr.Code == "23503" {
					s.logger.Error("foreign key constraint error, dropping job", "err", err)
					break
				}
			}
			chen.RetriesCount++
			s.mq.Republish(chen, chen.RetriesCount)
			continue
		}
		s.logger.Debug("successfully added group message to database")
	}
}
