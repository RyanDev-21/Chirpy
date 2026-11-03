// Package app wires the server together: config, infrastructure clients,
// services, background workers and the HTTP router.
package app

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"RyanDev-21.com/Chirpy/internal/ai"
	authsvc "RyanDev-21.com/Chirpy/internal/auth"
	"RyanDev-21.com/Chirpy/internal/chat"
	"RyanDev-21.com/Chirpy/internal/config"
	"RyanDev-21.com/Chirpy/internal/database"
	"RyanDev-21.com/Chirpy/internal/friends"
	"RyanDev-21.com/Chirpy/internal/groups"
	"RyanDev-21.com/Chirpy/internal/platform/queue"
	"RyanDev-21.com/Chirpy/internal/platform/redisx"
	"RyanDev-21.com/Chirpy/internal/users"
	"RyanDev-21.com/Chirpy/internal/ws"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"google.golang.org/genai"
)

// App holds every long-lived dependency of the server.
type App struct {
	cfg    config.Config
	logger *slog.Logger
	db     *pgxpool.Pool
	redis  *redis.Client
	// queueRedis is a separate pool for the job queue: its blocking reads
	// must never take connections away from request handling
	queueRedis *redis.Client
	queue      queue.Broker
	hub        *ws.Hub

	users   users.UserService
	friends friends.Service
	auth    authsvc.AuthService
	chat    chat.ChatService
	groups  groups.GroupService

	handlers handlers
}

type handlers struct {
	users   *users.UserHandler
	friends *friends.Handler
	auth    *authsvc.AuthHandler
	chat    *chat.ChatHandler
	groups  *groups.GroupHandler
	ai      *ai.Handler // nil when no Gemini key is configured
	admin   *adminHandler
}

// New connects to Postgres and Redis and builds all services.
func New(ctx context.Context, cfg config.Config, logger *slog.Logger) (*App, error) {
	db, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	if err := db.Ping(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	redisOpts := redisx.Options{Addr: cfg.RedisAddr, Username: cfg.RedisUser, Password: cfg.RedisPass, DB: cfg.RedisDB}
	rdb, err := redisx.NewClient(ctx, redisOpts)
	if err != nil {
		db.Close()
		return nil, err
	}
	queueOpts := redisOpts
	queueOpts.PoolSize = 32
	qrdb, err := redisx.NewClient(ctx, queueOpts)
	if err != nil {
		_ = rdb.Close()
		db.Close()
		return nil, err
	}

	a := &App{
		cfg:        cfg,
		logger:     logger,
		db:         db,
		redis:      rdb,
		queueRedis: qrdb,
		queue:      queue.NewRedis(qrdb, queue.RedisOptions{}, logger),
		hub:        ws.NewHub(),
	}
	queries := database.New(db)

	a.users = users.NewUserService(users.NewUserRepo(queries), users.NewStore(rdb), a.queue, logger)
	a.friends = friends.NewService(friends.NewRepo(queries), friends.NewStore(rdb), a.users, a.hub, logger)
	a.groups = groups.NewGroupService(groups.NewGroupRepo(db), groups.NewStore(rdb), logger)
	a.chat = chat.NewChatService(chat.NewChatRepo(queries), chat.NewCache(rdb), a.groups, a.friends, a.hub, a.queue, logger)
	a.auth = authsvc.NewAuthService(users.NewUserRepo(queries), authsvc.NewAuthRepo(queries), cfg.JWTSecret, logger)

	a.handlers = handlers{
		users:   users.NewUserHandler(a.users),
		friends: friends.NewHandler(a.friends),
		auth:    authsvc.NewAuthHandler(a.auth),
		chat:    chat.NewChatHandler(a.chat, a.hub, ws.Upgrader(cfg.CORSOrigins), logger),
		groups:  groups.NewGroupHandler(a.groups),
		admin:   &adminHandler{queries: queries},
	}
	if cfg.GeminiAPIKey != "" {
		client, err := genai.NewClient(ctx, &genai.ClientConfig{APIKey: cfg.GeminiAPIKey})
		if err != nil {
			a.Close()
			return nil, fmt.Errorf("create gemini client: %w", err)
		}
		a.handlers.ai = ai.NewGenAIHandler(ai.NewAiService(client))
	} else {
		logger.Warn("GEMINI_API_KEY not set, AI routes disabled")
	}
	return a, nil
}

// Start runs the websocket hub and queue workers until ctx is cancelled.
func (a *App) Start(ctx context.Context) error {
	go a.hub.Run()
	return a.startWorkers(ctx)
}

// Run starts the hub, workers and HTTP server, and blocks until ctx is
// cancelled, then shuts the server down gracefully.
func (a *App) Run(ctx context.Context) error {
	if err := a.Start(ctx); err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              a.cfg.Addr,
		Handler:           a.Router(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		TLSConfig:         tlsConfig(),
	}

	errCh := make(chan error, 1)
	go func() {
		a.logger.Info("server listening", "addr", a.cfg.Addr, "tls", a.cfg.TLSCertFile != "")
		var err error
		if a.cfg.TLSCertFile != "" {
			err = srv.ListenAndServeTLS(a.cfg.TLSCertFile, a.cfg.TLSKeyFile)
		} else {
			err = srv.ListenAndServe()
		}
		if !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	a.logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), a.cfg.ShutdownTimeout)
	defer cancel()
	err := srv.Shutdown(shutdownCtx)
	a.queue.Wait() // let in-flight jobs finish and ack
	return err
}

// Close releases database and redis connections.
func (a *App) Close() {
	if a.queueRedis != nil {
		_ = a.queueRedis.Close()
	}
	if a.redis != nil {
		_ = a.redis.Close()
	}
	if a.db != nil {
		a.db.Close()
	}
}

func tlsConfig() *tls.Config {
	return &tls.Config{
		MinVersion:       tls.VersionTLS12,
		CurvePreferences: []tls.CurveID{tls.X25519, tls.CurveP256, tls.CurveP384},
		CipherSuites: []uint16{
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
		},
	}
}
