// Package reqctx stores per-request values (authenticated user, request id)
// on a context so services do not depend on net/http.
package reqctx

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

type key int

const (
	userKey key = iota
	requestIDKey
)

var ErrNoUser = errors.New("no authenticated user in context")

func WithUser(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, userKey, id)
}

// User returns the authenticated user id.
func User(ctx context.Context) (uuid.UUID, error) {
	id, ok := ctx.Value(userKey).(uuid.UUID)
	if !ok || id == uuid.Nil {
		return uuid.Nil, ErrNoUser
	}
	return id, nil
}

func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestID returns the request id, or "" outside a request.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}
