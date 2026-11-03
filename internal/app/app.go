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
	mq "RyanDev-21.com/Chirpy/internal/customMq"
	"RyanDev-21.com/Chirpy/internal/database"
	"RyanDev-21.com/Chirpy/internal/groups"
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
	redis  redis.UniversalClient
	queue  *mq.MainMQ
	hub    *ws.Hub

	users  users.UserService
	auth   authsvc.AuthService
	chat   chat.ChatService
	groups groups.GroupService

	handlers handlers
}

type handlers struct {
	users  *users.UserHandler
	auth   *authsvc.AuthHandler
	chat   *chat.ChatHandler
	groups *groups.GroupHandler
	ai     *ai.Handler // nil when no Gemini key is configured
	admin  *adminHandler
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
	rdb, err := redisx.NewRedisClient(cfg.RedisAddr, cfg.RedisUser, cfg.RedisPass)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("connect redis: %w", err)
	}

	a := &App{
		cfg:    cfg,
		logger: logger,
		db:     db,
		redis:  rdb,
		queue:  mq.NewMainMQ(10),
		hub:    ws.NewHub(),
	}
	queries := database.New(db)

	userRepo := users.NewUserRepo(queries)
	authRepo := authsvc.NewAuthRepo(queries)
	chatRepo := chat.NewChatRepo(queries)
	groupRepo := groups.NewGroupRepo(queries)

	groupCache := groups.NewGroupCache(groupRepo)
	userCache := users.NewUserCache(userRepo)
	configCache := users.NewConfigCache(userRepo)
	chatCache := chat.NewChatCache(redisx.NewRedisCacheImpl(rdb), logger)
	go func() {
		if err := groupCache.Load(); err != nil {
			logger.Error("failed to warm group cache", "error", err)
		}
	}()
	go userCache.Load()

	a.users = users.NewUserService(userRepo, userCache, a.queue, logger, a.hub, configCache)
	a.auth = authsvc.NewAuthService(userRepo, authRepo, cfg.JWTSecret, logger)
	a.chat = chat.NewChatService(chatRepo, a.hub, a.queue, chatCache, groupCache, logger)
	a.groups = groups.NewGroupService(groupRepo, a.queue, groupCache, logger)

	a.handlers = handlers{
		users:  users.NewUserHandler(a.users),
		auth:   authsvc.NewAuthHandler(a.auth),
		chat:   chat.NewChatHandler(a.chat, logger),
		groups: groups.NewGroupHandler(a.groups),
		admin:  &adminHandler{queries: queries},
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

// Run starts the hub, workers and HTTP server, and blocks until ctx is
// cancelled, then shuts the server down gracefully.
func (a *App) Run(ctx context.Context) error {
	go a.hub.Run()
	a.startWorkers()

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
	return srv.Shutdown(shutdownCtx)
}

// Close releases database and redis connections.
func (a *App) Close() {
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
