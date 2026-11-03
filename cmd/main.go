package main

import (
	"context"
	"crypto/tls"
	"log"
	"net/http"
	"os"
	"time"

	//"database/sql"
	"RyanDev-21.com/Chirpy/cmd/setup"
	aimodels "RyanDev-21.com/Chirpy/internal/aiModels"
	authClient "RyanDev-21.com/Chirpy/internal/auth"
	"RyanDev-21.com/Chirpy/internal/chat"
	chatmodel "RyanDev-21.com/Chirpy/internal/chatModel"
	mq "RyanDev-21.com/Chirpy/internal/customMq"
	"RyanDev-21.com/Chirpy/internal/database"
	"RyanDev-21.com/Chirpy/internal/groups"
	rediscache "RyanDev-21.com/Chirpy/internal/redisCache"

	//"golang.org/x/crypto/acme/autocert"
	"google.golang.org/genai"

	"RyanDev-21.com/Chirpy/internal/users"
	"RyanDev-21.com/Chirpy/pkg/middleware"
	"RyanDev-21.com/Chirpy/pkg/ratelimit"
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
	geminiKey := os.Getenv("GENMINI_API_KEY")
	genAiClientConfig := &genai.ClientConfig{
		APIKey: geminiKey,
	}
	client, err := genai.NewClient(ctx, genAiClientConfig)
	if err != nil {
		log.Printf("failed to create the ai %s", err)
		log.Fatal(err)
	}
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
	mq := mq.NewMainMQ(10)
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
	chatCache := chat.NewChatCache(rediscache, &apicfg.Logger)
	configCache := users.NewConfigCache(userRepo)

	// Create Services
	userService := users.NewUserService(userRepo, userCache, mq, &apicfg.Logger, hub, configCache)
	authService := authClient.NewAuthService(userRepo, authRepo, apicfg.Secret, &apicfg.Logger)
	chatService := chat.NewChatService(chatRepo, hub, mq, chatCache, groupCache, &apicfg.Logger)
	groupService := groups.NewGroupService(groupRepo, mq, groupCache, &apicfg.Logger)
	genAIService := aimodels.NewAiService(client, hub)

	// Create Hanlders
	userHandler := users.NewUserHandler(userService)
	authHandler := authClient.NewAuthHandler(authService)
	chatHandler := chat.NewChatHandler(chatService, &apicfg.Logger)
	groupHandler := groups.NewGroupHandler(groupService)
	genAIHandler := aimodels.NewGenAIHandler(genAIService)
	setup.RunJobsForUsers(mq, userService)
	mq.ListeningForTheChannels(groups.TopicCreateGroup, 10, groupService.StartWorkerForCreateGroup)
	mq.ListeningForTheChannels(groups.TopicAddMember, 10, groupService.StartWorkerForAddMember)
	mq.ListeningForTheChannels(groups.TopicRemoveMember, 10, groupService.StartWorkerForLeaveMember)
	mq.ListeningForTheChannels(chatmodel.PrivateMessageConstant, 50, chatService.StartWorkerForAddPrivateMessage)
	mq.ListeningForTheChannels(chatmodel.PublicMessageConstant, 50, chatService.StartWorkerForAddPublicMessage)
	mq.ListeningForTheChannels(chatmodel.SeenMessageConstant, 10, chatService.StartWorkerForUpdateSeen)

	setup.SetUpUserRoutes(mux, apicfg, *userHandler)
	// Users routes moved

	authed := func(h http.HandlerFunc) http.Handler {
		return middleware.MiddleWareLog(middleware.AuthMiddleWare(h, apicfg.Secret, &apicfg.Logger))
	}
	limited := func(l *ratelimit.RateLimiter, h http.HandlerFunc) http.Handler {
		return middleware.MiddleWareLog(ratelimit.RateLimitMiddleware(l, h, &apicfg.Logger))
	}
	authedLimited := func(l *ratelimit.RateLimiter, h http.HandlerFunc) http.Handler {
		return middleware.MiddleWareLog(middleware.AuthMiddleWare(ratelimit.RateLimitMiddleware(l, h, &apicfg.Logger), apicfg.Secret, &apicfg.Logger))
	}
	loginLimiter := ratelimit.NewRateLimiter(10, time.Minute)
	sendLimiter := ratelimit.NewRateLimiter(120, time.Minute)
	aiLimiter := ratelimit.NewRateLimiter(5, time.Minute)

	// AI suggestions (authenticated, tightly limited: every call costs quota)
	mux.Handle("GET /api/ai/suggestions", authedLimited(aiLimiter, genAIHandler.GenAIDummyHandler))

	// admin: only exists on dev and still needs a logged-in user
	if platform == "dev" {
		mux.Handle("POST /admin/reset", authed(apicfg.UserResetHandle))
	}

	mux.Handle("POST /api/login", limited(loginLimiter, authHandler.Login))
	mux.Handle("POST /api/refresh", limited(loginLimiter, authHandler.RefreshToken))
	mux.Handle("POST /api/revoke", limited(loginLimiter, authHandler.Revoke))

	mux.Handle("GET /api/chats/ws", middleware.MiddleWareLog(middleware.WSAuthMiddleWare(chatHandler.ServeWs, apicfg.Secret, &apicfg.Logger)))
	mux.Handle("POST /api/chats/groups", authed(groupHandler.CreateGroup))
	mux.Handle("POST /api/chats/groups/{group_id}/members", authed(groupHandler.JoinGroup))
	mux.Handle("DELETE /api/chats/groups/{group_id}/members", authed(groupHandler.LeaveGroup))
	mux.Handle("POST /api/chats/message", authedLimited(sendLimiter, chatHandler.SendMessage))
	mux.Handle("GET /api/chat/{otherUser_id}/messages", authed(chatHandler.GetMesagesForPrivateID))
	mux.Handle("POST /api/chat/{otherUser_id}/messages", authed(chatHandler.UpdateSeen))
	mux.Handle("GET /api/chat/groups/{group_id}/messages", authed(chatHandler.GetMessagesForPublicID))

	handlerWithCORS := corsMiddleware(mux)
	cfg := &tls.Config{
		MinVersion:       tls.VersionTLS12,
		CurvePreferences: []tls.CurveID{tls.CurveP521, tls.CurveP256, tls.CurveP384},
		CipherSuites: []uint16{
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
		},
	}
	server := http.Server{
		Addr:         Port,
		Handler:      handlerWithCORS,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		TLSConfig:    cfg,
		TLSNextProto: make(map[string]func(*http.Server, *tls.Conn, http.Handler)),
		//	TLSConfig: m.TLSConfig(),
	}

	log.Printf("The server is running on %q\n", Port)
	log.Fatal(server.ListenAndServeTLS("../server.crt", "../server.key"))
}
