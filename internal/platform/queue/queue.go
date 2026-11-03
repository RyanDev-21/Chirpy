// Package queue defines the background job contract shared by services
// (which publish) and workers (which handle).
package queue

import (
	"context"
	"encoding/json"
	"errors"
)

// Publisher enqueues a job payload on a topic.
type Publisher interface {
	Publish(ctx context.Context, topic string, payload any) error
}

// Handler processes one raw job payload. Returning an error retries the
// job; wrap it with Permanent to drop it instead.
type Handler func(ctx context.Context, payload json.RawMessage) error

type permanentError struct{ err error }

func (p permanentError) Error() string { return "permanent: " + p.err.Error() }
func (p permanentError) Unwrap() error { return p.err }

// Permanent marks err as not worth retrying (bad payload, constraint
// violation, ...).
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return permanentError{err: err}
}

func IsPermanent(err error) bool {
	var p permanentError
	return errors.As(err, &p)
}

// Typed adapts a handler for a concrete payload type into a Handler.
// Payloads that fail to decode are dropped as permanent errors.
func Typed[T any](fn func(ctx context.Context, job T) error) Handler {
	return func(ctx context.Context, raw json.RawMessage) error {
		var job T
		if err := json.Unmarshal(raw, &job); err != nil {
			return Permanent(err)
		}
		return fn(ctx, job)
	}
}
