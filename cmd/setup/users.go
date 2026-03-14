package setup

import (
	"net/http"
	"time"

	mq "RyanDev-21.com/Chirpy/internal/customMq"
	"RyanDev-21.com/Chirpy/internal/users"
	"RyanDev-21.com/Chirpy/pkg/middleware"
	"RyanDev-21.com/Chirpy/pkg/ratelimit"
)

var (
	searchUserLimiter    = ratelimit.NewRateLimiter(20, time.Minute)
	addFriendLimiter     = ratelimit.NewRateLimiter(10, time.Minute)
	confirmFriendLimiter = ratelimit.NewRateLimiter(30, time.Minute)
	deleteFriendLimiter  = ratelimit.NewRateLimiter(30, time.Minute)
)

func RunJobsForUsers(mq *mq.MainMQ, userService users.UserService) {
	go mq.ListeningForTheChannels(users.SendRequest, 100, userService.StartWorkerForAddFri)
	go mq.ListeningForTheChannels(users.ConfirmFriendReq, 100, userService.StartWorkerForConfirmFri)
	go mq.ListeningForTheChannels(users.DeleteFriReq, 100, userService.StartWorkerForDeleteReq)
	go mq.ListeningForTheChannels(users.CancelReq, 100, userService.StartWorkerForCancelReq)

	go mq.ListeningForTheChannels("UpdateUserCache", 100, userService.StartWorkerForUpdateUserCache)
	go mq.ListeningForTheChannels("SavePosition", 100, userService.StartWorkerForSaveConfig)
}

func SetUpUserRoutes(mux *http.ServeMux, apiConf *APIConfig, userHandler users.UserHandler) {
	// create Users
	mux.Handle("POST /api/users", apiConf.MiddlewareMetricsInc(middleware.MiddleWareLog(userHandler.Register)))

	// update Password
	mux.Handle("POST /api/users/password", middleware.MiddleWareLog(middleware.AuthMiddleWare(userHandler.UpdatePassword, apiConf.Secret, &apiConf.Logger)))

	// search users
	mux.Handle("GET /api/users/search",
		middleware.MiddleWareLog(
			middleware.AuthMiddleWare(
				ratelimit.RateLimitMiddleware(searchUserLimiter, userHandler.SearchUser, &apiConf.Logger),
				apiConf.Secret, &apiConf.Logger)))

	// send fri req
	mux.Handle("POST /api/friends/requests",
		middleware.MiddleWareLog(
			middleware.AuthMiddleWare(
				ratelimit.RateLimitMiddleware(addFriendLimiter, userHandler.AddFriend, &apiConf.Logger),
				apiConf.Secret, &apiConf.Logger)))

	// get fri req list
	mux.Handle("GET /api/friends/requests", middleware.MiddleWareLog(middleware.AuthMiddleWare(userHandler.GetPendingList, apiConf.Secret, &apiConf.Logger)))

	// confirm/cancel fri req
	mux.Handle("PUT /api/friends/requests/{request_id}/",
		middleware.MiddleWareLog(
			middleware.AuthMiddleWare(
				ratelimit.RateLimitMiddleware(confirmFriendLimiter, userHandler.UpdateReq, &apiConf.Logger),
				apiConf.Secret, &apiConf.Logger)))

	// cancel send req
	mux.Handle("DELETE /api/friends/requests/{request_id}/",
		middleware.MiddleWareLog(
			middleware.AuthMiddleWare(
				ratelimit.RateLimitMiddleware(deleteFriendLimiter, userHandler.DeleteFriReq, &apiConf.Logger),
				apiConf.Secret, &apiConf.Logger)))

	// get friend list
	mux.Handle("GET /api/friends", middleware.MiddleWareLog(middleware.AuthMiddleWare(
		userHandler.GetFriendList, apiConf.Secret, &apiConf.Logger)))

	// save users configs
	mux.Handle("POST /api/users/configs", middleware.MiddleWareLog(middleware.AuthMiddleWare(
		userHandler.SaveConfig, apiConf.Secret, &apiConf.Logger)))

	// get user configs
	mux.Handle("GET /api/users/configs", middleware.MiddleWareLog(middleware.AuthMiddleWare(
		userHandler.GetConfig, apiConf.Secret, &apiConf.Logger)))
}
