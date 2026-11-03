package app

import (
	"context"

	"RyanDev-21.com/Chirpy/internal/chat"
	"RyanDev-21.com/Chirpy/internal/friends"
	"RyanDev-21.com/Chirpy/internal/groups"
	"RyanDev-21.com/Chirpy/internal/platform/queue"
	"RyanDev-21.com/Chirpy/internal/users"
)

type subscription struct {
	topic   string
	workers int
	handler queue.Handler
}

// subscriptions lists every queue topic and the handler that persists it.
func (a *App) subscriptions() []subscription {
	return []subscription{
		{users.TopicSaveConfig, 2, queue.Typed(a.users.HandleSaveConfig)},

		{friends.TopicCreate, 4, queue.Typed(a.friends.HandleCreate)},
		{friends.TopicAccept, 4, queue.Typed(a.friends.HandleAccept)},
		{friends.TopicDeny, 2, queue.Typed(a.friends.HandleDeny)},
		{friends.TopicCancel, 2, queue.Typed(a.friends.HandleCancel)},

		{groups.TopicCreateGroup, 2, queue.Typed(a.groups.HandleCreate)},
		{groups.TopicAddMember, 4, queue.Typed(a.groups.HandleAddMember)},
		{groups.TopicRemoveMember, 4, queue.Typed(a.groups.HandleRemoveMember)},

		{chat.TopicSeen, 2, queue.Typed(a.chat.HandleSeen)},
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
