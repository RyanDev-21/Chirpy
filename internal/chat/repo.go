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
	AddMessagePrivate(payload *database.AddMessagePrivateParams) (*database.Message, error)
	AddMessagePublic(payload *database.AddMessagePublicParams) (*database.Groupmessage, error)
	GetMessagesForPrivate(ctx context.Context, fromID, toID uuid.UUID) (*[]database.Message, error)
	GetMessagesForPublic(ctx context.Context, toID uuid.UUID) (*[]database.Groupmessage, error)
	GetAllPrivateMessages(ctx context.Context) (*[]database.Message, error)
	GetAllPublicMessages(ctx context.Context) (*[]database.Groupmessage, error)
}

func NewChatRepo(queries *database.Queries) ChatRepo {
	return &chatRepo{
		queries: queries,
	}
}

func (r *chatRepo) AddMessagePrivate(payload *database.AddMessagePrivateParams) (*database.Message, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	res, err := r.queries.AddMessagePrivate(ctx, *payload)
	return &res, err
}

func (r *chatRepo) AddMessagePublic(payload *database.AddMessagePublicParams) (*database.Groupmessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	res, err := r.queries.AddMessagePublic(ctx, *payload)
	return &res, err
}

func (r *chatRepo) GetMessagesForPrivate(ctx context.Context, fromID, toID uuid.UUID) (*[]database.Message, error) {
	message, err := r.queries.GetMessagesForPrivate(ctx, database.GetMessagesForPrivateParams{
		FromID: *GetUUIDType(fromID),
		ToID:   *GetUUIDType(toID),
	})
	if err != nil {
		return nil, err
	}
	return &message, nil
}

func (r *chatRepo) GetMessagesForPublic(ctx context.Context, toID uuid.UUID) (*[]database.Groupmessage, error) {
	message, err := r.queries.GetMessagesForPublic(ctx, *GetUUIDType(toID))
	if err != nil {
		return nil, err
	}
	return &message, nil
}

func (r *chatRepo) GetAllPrivateMessages(ctx context.Context) (*[]database.Message, error) {
	msgs, err := r.queries.GetMessagesForAllPrivateChats(ctx)
	return &msgs, err
}

func (r *chatRepo) GetAllPublicMessages(ctx context.Context) (*[]database.Groupmessage, error) {
	msgs, err := r.queries.GetMessagesForAllPublicChats(ctx)
	return &msgs, err
}
