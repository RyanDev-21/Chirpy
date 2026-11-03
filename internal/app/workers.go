package app

import (
	"RyanDev-21.com/Chirpy/internal/chat"
	"RyanDev-21.com/Chirpy/internal/groups"
	"RyanDev-21.com/Chirpy/internal/users"
)

// startWorkers registers one consumer pool per queue topic.
func (a *App) startWorkers() {
	q := a.queue
	q.ListeningForTheChannels(users.SendRequest, 10, a.users.StartWorkerForAddFri)
	q.ListeningForTheChannels(users.ConfirmFriendReq, 10, a.users.StartWorkerForConfirmFri)
	q.ListeningForTheChannels(users.DeleteFriReq, 10, a.users.StartWorkerForDeleteReq)
	q.ListeningForTheChannels(users.CancelReq, 10, a.users.StartWorkerForCancelReq)
	q.ListeningForTheChannels(users.TopicUpdateUserCache, 5, a.users.StartWorkerForUpdateUserCache)
	q.ListeningForTheChannels(users.TopicSaveConfig, 5, a.users.StartWorkerForSaveConfig)

	q.ListeningForTheChannels(groups.TopicCreateGroup, 10, a.groups.StartWorkerForCreateGroup)
	q.ListeningForTheChannels(groups.TopicAddMember, 10, a.groups.StartWorkerForAddMember)
	q.ListeningForTheChannels(groups.TopicRemoveMember, 10, a.groups.StartWorkerForLeaveMember)

	q.ListeningForTheChannels(chat.TopicPrivateMessage, 50, a.chat.StartWorkerForAddPrivateMessage)
	q.ListeningForTheChannels(chat.TopicPublicMessage, 50, a.chat.StartWorkerForAddPublicMessage)
	q.ListeningForTheChannels(chat.TopicSeen, 10, a.chat.StartWorkerForUpdateSeen)
}
