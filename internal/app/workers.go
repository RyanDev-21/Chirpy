package app

import (
	"context"

	"RyanDev-21.com/Chirpy/internal/chat"
	"RyanDev-21.com/Chirpy/internal/platform/queue"
	"RyanDev-21.com/Chirpy/internal/push"
)

type subscription struct {
	topic   string
	workers int
	handler queue.Handler
}

// subscriptions lists every queue topic and the handler that persists it.
//
// Writes that users depend on (messages, friend requests, group
// membership) go straight to postgres and are not queued. Only writes where
// a short delay, or rarely losing one, is harmless belong here:
//   - seen receipts: frequent, and only the latest one matters
//
// Future side effects (offline notifications, AI suggestions, fan-out
// across instances) should also be queued rather than done in the request.
func (a *App) subscriptions() []subscription {
	return []subscription{
		{chat.TopicSeen, 2, queue.Typed(a.chat.HandleSeen)},
		{push.TopicPush, 2, queue.Typed(a.pushSvc.Handle)},
	}
}

func (a *App) startWorkers(ctx context.Context) error {
	for _, s := range a.subscriptions() {
		if err := a.queue.Subscribe(ctx, s.topic, s.workers, s.handler); err != nil {
			return err
		}
	}
	return nil
}
