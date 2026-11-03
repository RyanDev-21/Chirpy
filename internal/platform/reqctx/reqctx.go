// Package reqctx stores per-request values (request id, authenticated user)
// on a context so services do not depend on net/http, and provides a slog
// handler that adds them to every log record.
package reqctx

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/google/uuid"
)

type key int

const infoKey key = iota

var ErrNoUser = errors.New("no authenticated user in context")

// Info is shared by every layer of one request. The access logger creates
// it before authentication runs, so the user id is stored atomically and
// the access log line can include it after the handler returns.
type Info struct {
	RequestID string
	ClientIP  string
	user      atomic.Pointer[uuid.UUID]
}

func (i *Info) UserID() (uuid.UUID, bool) {
	if p := i.user.Load(); p != nil {
		return *p, true
	}
	return uuid.Nil, false
}

func infoFrom(ctx context.Context) *Info {
	i, _ := ctx.Value(infoKey).(*Info)
	return i
}

// Start attaches a fresh Info carrying requestID.
func Start(ctx context.Context, requestID string) (context.Context, *Info) {
	i := &Info{RequestID: requestID}
	return context.WithValue(ctx, infoKey, i), i
}

// WithUser records the authenticated user (creating an Info if needed).
func WithUser(ctx context.Context, id uuid.UUID) context.Context {
	i := infoFrom(ctx)
	if i == nil {
		ctx, i = Start(ctx, "")
	}
	i.user.Store(&id)
	return ctx
}

// User returns the authenticated user id.
func User(ctx context.Context) (uuid.UUID, error) {
	if i := infoFrom(ctx); i != nil {
		if id, ok := i.UserID(); ok && id != uuid.Nil {
			return id, nil
		}
	}
	return uuid.Nil, ErrNoUser
}

// RequestID returns the request id, or "" outside a request.
func RequestID(ctx context.Context) string {
	if i := infoFrom(ctx); i != nil {
		return i.RequestID
	}
	return ""
}

// ClientIP returns the caller's IP as resolved by the access logger, or "".
func ClientIP(ctx context.Context) string {
	if i := infoFrom(ctx); i != nil {
		return i.ClientIP
	}
	return ""
}

// WithRequestID is Start without the returned Info.
func WithRequestID(ctx context.Context, id string) context.Context {
	ctx, _ = Start(ctx, id)
	return ctx
}
