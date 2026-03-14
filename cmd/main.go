package main

import (
	"context"
	"log"
	"net/http"
	"os"

	//"database/sql"
	"RyanDev-21.com/Chirpy/cmd/setup"
	authClient "RyanDev-21.com/Chirpy/internal/auth"
	"RyanDev-21.com/Chirpy/internal/chat"
	chatmodel "RyanDev-21.com/Chirpy/internal/chatModel"
	mq "RyanDev-21.com/Chirpy/internal/customMq"
	"RyanDev-21.com/Chirpy/internal/database"
	"RyanDev-21.com/Chirpy/internal/groups"
	rediscache "RyanDev-21.com/Chirpy/internal/redisCache"

	"RyanDev-21.com/Chirpy/internal/users"
	"RyanDev-21.com/Chirpy/pkg/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

const (
	Port = ":8080"
)

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func main() {
	err := godotenv.Load("../.env")
	if err != nil {
		log.Fatal("failed to load the env")
	}
	ctx := context.Background()
	dURL := os.Getenv("DB_URL")
	platform := os.Getenv("PLATFORM")
	Secret := os.Getenv("SECRET")
	redisAddr := os.Getenv("REDIS_ADDR")
	redisUsr := os.Getenv("REDIS_USR")
	redisPass := os.Getenv("REDIS_PASS")
	db, err := pgxpool.New(ctx, dURL)
	if err != nil {
		log.Fatal("Failed connection to the db ")
	}
	queries := database.New(db)
	defer db.Close()
	// initiate Logger first

	apicfg := setup.InitApiConfig(queries, platform, Secret)
	mux := http.NewServeMux()
	// init customMq queue
	mq := mq.NewMainMQ(&map[string]chan *mq.Channel{}, 10)
	// init hub
	hub := chatmodel.NewHub()
	go hub.Run()

	// init redis cache
	cacheClient, err := rediscache.NewRedisClient(redisAddr, redisUsr, redisPass)
	if err != nil {
		log.Fatalf("failed to get the redis client \n #%s#", err)
	}

	// Create Repositories
	userRepo := users.NewUserRepo(apicfg.Queries)
	authRepo := authClient.NewAuthRepo(apicfg.Queries)
	chatRepo := chat.NewChatRepo(apicfg.Queries)
	groupRepo := groups.NewGroupRepo(apicfg.Queries)
	// set up group cache
	groupCache := groups.NewGroupCache(groupRepo)
	go groupCache.Load()
	// set up user cache
	userCache := users.NewUserCache(userRepo)
	go userCache.Load()

	rediscache := rediscache.NewRedisCacheImpl(cacheClient)
	chatCache := chat.NewChatCache(rediscache, chatRepo, &apicfg.Logger)
	go chatCache.LoadMessagesForStartUp()
	configCache := users.NewConfigCache(userRepo)

	// Create Services
	userService := users.NewUserService(userRepo, userCache, mq, &apicfg.Logger, hub, configCache)
	authService := authClient.NewAuthService(userRepo, authRepo, apicfg.Secret, &apicfg.Logger)
	chatService := chat.NewChatService(chatRepo, hub, mq, chatCache, groupCache, &apicfg.Logger)
	groupService := groups.NewGroupService(groupRepo, hub, mq, groupCache, &apicfg.Logger)

	// Create Hanlders
	userHandler := users.NewUserHandler(userService)
	authHandler := authClient.NewAuthHandler(authService)
	chatHandler := chat.NewChatHandler(chatService, &apicfg.Logger)
	groupHandler := groups.NewGroupHandler(groupService)

	// right now the topic name for job are hand coded should change that later
	go mq.Run()
	// set up jobs consumer for userService
	setup.RunJobsForUsers(mq, userService)
	// this has to move somewhere
	// need to fix the magic numbers and hardcoded job
	go mq.ListeningForTheChannels("createGroup", 100, groupService.StartWorkerForCreateGroup)
	//	go mq.ListeningForTheChannels("addCreator", 100, groupService.StartWorkerForCreateGroupLeader)
	go mq.ListeningForTheChannels("addMemberList", 100, groupService.StartWorkerForAddMember)
	go mq.ListeningForTheChannels("addMember", 100, groupService.StartWorkerForAddMemberList)
	go mq.ListeningForTheChannels("removeGroupMember", 100, groupService.StartWorkerForLeaveMember)
	// jobs for users moved
	go mq.ListeningForTheChannels(chatmodel.PrivateMessageConstant, 1000, chatService.StartWorkerForAddPrivateMessage)
	go mq.ListeningForTheChannels(chatmodel.PublicMessageConstant, 1000, chatService.StartWorkerForAddPublicMessage)
	go mq.ListeningForTheChannels("JobForSeen", 100, chatService.StartWorkerForUpdateSeen)

	setup.SetUpUserRoutes(mux, apicfg, *userHandler)
	// Users routes moved

	// POST route
	mux.HandleFunc("POST /admin/metrics/reset", apicfg.ResetHandle)
	mux.HandleFunc("POST /admin/reset", apicfg.UserResetHandle)
	mux.Handle("POST /api/login", middleware.MiddleWareLog(authHandler.Login))
	mux.Handle("POST /api/refresh", middleware.MiddleWareLog(authHandler.RefreshToken))
	mux.HandleFunc("POST /api/revoke", authHandler.Revoke)

	// //NOTE FOR webhooks
	// /*"Client must request with json of '{
	// 					  "event": "user.upgraded",
	// 					  "data": {
	// 						"user_id": "3311741c-680c-4546-99f3-fc9efac2036c"
	// 					  }
	// }'"*/
	// mux.HandleFunc("POST /api/polka/webhooks", apicfg.WebHookHandle)

	// Maybe endpoint for chat
	mux.Handle("GET /api/chats/ws", middleware.MiddleWareLog(middleware.AuthMiddleWare(chatHandler.ServeWs, apicfg.Secret, &apicfg.Logger)))

	// create a group
	mux.Handle("POST /api/chats/groups", middleware.MiddleWareLog(middleware.AuthMiddleWare(groupHandler.CreateGroup, apicfg.Secret, &apicfg.Logger)))

	// join group
	mux.Handle("POST /api/chats/groups/{group_id}/members", middleware.MiddleWareLog(middleware.AuthMiddleWare(groupHandler.JoinGroup, apicfg.Secret, &apicfg.Logger)))

	// leave group
	mux.Handle("DELETE /api/chats/groups/{group_id}/members", middleware.MiddleWareLog(middleware.AuthMiddleWare(groupHandler.LeaveGroup, apicfg.Secret, &apicfg.Logger)))

	// TODO::still have to write this one
	// kick or add the user from or to the group
	// mux.Handle("PATCH /api/chats/groups/{group_id}/members", middleware.MiddleWareLog(middleware.AuthMiddleWare(groupHandler.CreateGroup, apicfg.Secret, Logger)))
	//
	// TODO:and this one
	// change group setting or anything
	// mux.HandleFunc("PATCH /api/chats/groups/{group_id}", groupHandler.CreateGroup)

	// NOTE:: i should really consider making the server as my own config and graceful shutdown
	//
	// endpoint for send message
	// maybe need to consider about making the chatID
	// this is really wrong but i don't wanna fix it anymore
	// cuz what is the point of asking the chatid(toID) in body
	// should be like those get endpoint
	// this end point should be like this
	// for private, POST /api/chats/{otherUser_id}/messages
	// for public, POST /api/chats/groups/{group_id}/messages
	// for private ,PATCH /api/chats/{otherUser_id}/messages (the service should be called based on the type like "edit","seen" //cuz this route is for both )
	// for group ,PATH /api/chats/groups/{group_id}/messages (this should use for maybe edit )
	mux.Handle("POST /api/chats/message", middleware.MiddleWareLog(middleware.AuthMiddleWare(chatHandler.SendMessage, apicfg.Secret, &apicfg.Logger)))
	handlerWithCORS := corsMiddleware(mux)
	server := http.Server{
		Addr:    Port,
		Handler: handlerWithCORS,
	}

	mux.Handle("GET /api/chat/{otherUser_id}/messages", middleware.MiddleWareLog(middleware.AuthMiddleWare(chatHandler.GetMesagesForPrivateID, apicfg.Secret, &apicfg.Logger)))
	mux.Handle("POST /api/chat/{otherUser_id}/messages", middleware.MiddleWareLog(middleware.AuthMiddleWare(chatHandler.UpdateSeen, apicfg.Secret, &apicfg.Logger)))
	mux.Handle("GET /api/chat/groups/{group_id}/messages", middleware.MiddleWareLog(middleware.AuthMiddleWare(chatHandler.GetMessagesForPublicID, apicfg.Secret, &apicfg.Logger)))
	log.Printf("The server is running on %q\n", Port)
	log.Fatal(server.ListenAndServe())
}
