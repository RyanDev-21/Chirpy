package chat

import (
	"context"
	"time"

	"RyanDev-21.com/Chirpy/internal/database"
	"github.com/google/uuid"
	// rediscache "RyanDev-21.com/Chirpy/internal/redisCache"
)

type chatRepo struct {
	queries *database.Queries
}

type ChatRepo interface {
	addMessagePrivate(payload *database.AddMessagePrivateParams) (*database.Message, error)
	addMessagePublic(payload *database.AddMessagePublicParams) (*database.Groupmessage, error)
	getMessagesForPrivate(ctx context.Context, fromID, toID uuid.UUID) (*[]database.Message, error)

	getMessagesForPrivateWithTime(ctx context.Context, fromID, toID uuid.UUID,since time.Time) (*[]database.Message, error)
	getMessagesForPublic(ctx context.Context, toID uuid.UUID) (*[]database.Groupmessage, error)
	getAllPrivateMessages(ctx context.Context) (*[]database.Message, error)
	getAllPublicMessages(ctx context.Context) (*[]database.Groupmessage, error)
	updateLastSeen(chatID string,userID uuid.UUID,msgID uuid.UUID)error
}

func NewChatRepo(queries *database.Queries) ChatRepo {
	return &chatRepo{
		queries: queries,
	}
}

func (r *chatRepo) addMessagePrivate(payload *database.AddMessagePrivateParams) (*database.Message, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	res, err := r.queries.AddMessagePrivate(ctx, *payload)
	return &res, err
}

func (r *chatRepo) addMessagePublic(payload *database.AddMessagePublicParams) (*database.Groupmessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	res, err := r.queries.AddMessagePublic(ctx, *payload)
	return &res, err
}

func (r *chatRepo) getMessagesForPrivate(ctx context.Context, fromID, toID uuid.UUID) (*[]database.Message, error) {
	message, err := r.queries.GetMessagesForPrivate(ctx, database.GetMessagesForPrivateParams{
		FromID: *GetUUIDType(fromID),
		ToID:   *GetUUIDType(toID),
	})
	if err != nil {
		return nil, err
	}
	return &message, nil
}

func (r *chatRepo)getMessagesForPrivateWithTime(ctx context.Context, fromID, toID uuid.UUID,since time.Time) (*[]database.Message, error){
	message,err:=r.queries.GetMessagesForPrivateWithTime(ctx,database.GetMessagesForPrivateWithTimeParams{
			FromID: *GetUUIDType(fromID),
			ToID: *GetUUIDType(toID),
			CreatedAt:GetTimeStampType(since) ,
	})
	if err !=nil{
		return nil,err
	}
	return &message,nil
}

func (r *chatRepo) getMessagesForPublic(ctx context.Context, toID uuid.UUID) (*[]database.Groupmessage, error) {
	message, err := r.queries.GetMessagesForPublic(ctx, *GetUUIDType(toID))
	if err != nil {
		return nil, err
	}
	return &message, nil
}

func (r *chatRepo) getAllPrivateMessages(ctx context.Context) (*[]database.Message, error) {
	msgs, err := r.queries.GetMessagesForAllPrivateChats(ctx)
	return &msgs, err
}

func (r *chatRepo) getAllPublicMessages(ctx context.Context) (*[]database.Groupmessage, error) {
	msgs, err := r.queries.GetMessagesForAllPublicChats(ctx)
	return &msgs, err
}

func (r *chatRepo)updateLastSeen(chatID string,userID uuid.UUID,msgID uuid.UUID)error{
	context,cancel:= context.WithTimeout(context.Background(),1*time.Second)
	defer cancel()
	_,err:= r.queries.AddLastSeenMessage(context,database.AddLastSeenMessageParams{
		MessageID: msgID,
		SeenID: *GetUUIDType(userID),
		ChatID: chatID,
	})
	return err
}
