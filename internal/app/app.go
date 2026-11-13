// Package app wires the server together: config, infrastructure clients,
// services, background workers and the HTTP router.
package app

import (
	"sync"

	"RyanDev-21.com/Chirpy/internal/admin"
	"RyanDev-21.com/Chirpy/internal/platform/ratelimit"
	"RyanDev-21.com/Chirpy/internal/platform/telemetry"
	"RyanDev-21.com/Chirpy/internal/reports"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"log/slog"
	"net/http"
	"time"

	authsvc "RyanDev-21.com/Chirpy/internal/auth"
	"RyanDev-21.com/Chirpy/internal/calls"
	"RyanDev-21.com/Chirpy/internal/chat"
	"RyanDev-21.com/Chirpy/internal/config"
	"RyanDev-21.com/Chirpy/internal/database"
	"RyanDev-21.com/Chirpy/internal/discover"
	"RyanDev-21.com/Chirpy/internal/friends"
	"RyanDev-21.com/Chirpy/internal/groups"
	"RyanDev-21.com/Chirpy/internal/media"
	"RyanDev-21.com/Chirpy/internal/platform/mail"
	"RyanDev-21.com/Chirpy/internal/platform/migrate"
	"RyanDev-21.com/Chirpy/internal/platform/queue"
	"RyanDev-21.com/Chirpy/internal/platform/redisx"
	"RyanDev-21.com/Chirpy/internal/platform/revoke"
	"RyanDev-21.com/Chirpy/internal/presence"
	"RyanDev-21.com/Chirpy/internal/push"
	"RyanDev-21.com/Chirpy/internal/search"
	"RyanDev-21.com/Chirpy/internal/users"
	"RyanDev-21.com/Chirpy/internal/ws"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
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
	// revoked ends access tokens early (password change, account deletion)
	revoked *revoke.Store
	tickets *ws.Tickets
	// memLimits: in-process rate limiters by policy (RATE_LIMIT_BACKEND=memory)
	limMu     sync.Mutex
	memLimits map[string]ratelimit.Limiter
	pushSvc   *push.Service
	admin     *admin.Service
	media     *media.Service
	hub       *ws.Hub
	calls     *calls.Manager

	users   users.UserService
	friends friends.Service
	auth    authsvc.AuthService
	chat    chat.ChatService
	groups  groups.GroupService

	handlers handlers
}

type handlers struct {
	users    *users.UserHandler
	friends  *friends.Handler
	auth     *authsvc.AuthHandler
	account  *authsvc.AccountHandler
	chat     *chat.ChatHandler
	groups   *groups.GroupHandler
	admin    *adminHandler
	discover *discover.Handler
	push     *push.Handler
	admin2   *admin.Handler
	reports  *reports.Handler
	media    *media.Handler
	presence *presence.Handler
	calls    *calls.Handler
	search   *search.Handler
}

// New connects to Postgres and Redis and builds all services.
// Option overrides a dependency; used by tests.
type Option func(*options)

type options struct {
	mailer mail.Sender
	google authsvc.GoogleVerifier
	push   push.Sender
	media  media.Storage
}

// WithMediaStorage replaces R2 (tests).
func WithMediaStorage(s media.Storage) Option { return func(o *options) { o.media = s } }

// WithPushSender replaces the FCM client (tests).
func WithPushSender(s push.Sender) Option { return func(o *options) { o.push = s } }

// WithMailer replaces the email sender.
func WithMailer(m mail.Sender) Option { return func(o *options) { o.mailer = m } }

// WithGoogleVerifier replaces Google ID token verification.
func WithGoogleVerifier(v authsvc.GoogleVerifier) Option {
	return func(o *options) { o.google = v }
}

func New(ctx context.Context, cfg config.Config, logger *slog.Logger, opts ...Option) (*App, error) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	if cfg.MigrateOnStart {
		if err := migrate.Up(ctx, cfg.DatabaseURL, logger); err != nil {
			return nil, err
		}
	}
	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse DB_URL: %w", err)
	}
	if cfg.DBMaxConns > 0 {
		poolCfg.MaxConns = cfg.DBMaxConns
	}
	if cfg.DBMaxIdleTime > 0 {
		poolCfg.MaxConnIdleTime = cfg.DBMaxIdleTime
	}
	poolCfg.MinConns = 0 // let serverless Postgres scale to zero when idle
	db, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	if err := db.Ping(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	redisOpts := redisx.Options{URL: cfg.RedisURL, Addr: cfg.RedisAddr, Username: cfg.RedisUser, Password: cfg.RedisPass, DB: cfg.RedisDB, PoolSize: cfg.RedisPoolSize}
	// Redis only holds caches and short-lived records here, so it being down
	// at boot (e.g. during a deploy) mustn't keep Chirpy down. The durable
	// Redis queue does need it, so that backend still requires Redis.
	redisOpts.AllowDown = cfg.QueueBackend != "redis"
	rdb, err := redisx.NewClient(ctx, redisOpts)
	if errors.Is(err, redisx.ErrUnreachable) {
		logger.Warn("starting without Redis: caches fall back to the database until it's back", "error", err)
	} else if err != nil {
		db.Close()
		return nil, err
	}
	var (
		qrdb   *redis.Client
		broker queue.Broker
	)
	switch cfg.QueueBackend {
	case "memory":
		broker = queue.NewMemory(8, logger)
		logger.Warn("QUEUE_BACKEND=memory: queued jobs are lost on restart")
	default:
		queueOpts := redisOpts
		queueOpts.PoolSize = cfg.QueueRedisPoolSize
		if qrdb, err = redisx.NewClient(ctx, queueOpts); err != nil {
			_ = rdb.Close()
			db.Close()
			return nil, err
		}
		broker = queue.NewRedis(qrdb, queue.RedisOptions{}, logger)
	}

	a := &App{
		cfg:        cfg,
		logger:     logger,
		db:         db,
		redis:      rdb,
		queueRedis: qrdb,
		queue:      broker,
		hub:        ws.NewHub(logger),
	}
	queries := database.New(db)
	accessTTL := cfg.AccessTokenTTL
	if accessTTL <= 0 {
		accessTTL = authsvc.DefaultAccessTTL
	}
	a.revoked = revoke.New(rdb, accessTTL)
	a.tickets = ws.NewTickets(cfg.JWTSecret)
	// every "sign out everywhere" (password change or reset, takeover of an
	// unverified account) also ends the access tokens already issued
	userRepo := &sessionEnder{UserRepo: users.NewUserRepo(queries), revoked: a.revoked, hub: a.hub}

	a.users = users.NewUserService(userRepo, users.NewStore(rdb), logger)
	a.friends = friends.NewService(friends.NewRepo(queries), friends.NewStore(rdb), a.users, a.hub, logger)
	a.groups = groups.NewGroupService(groups.NewGroupRepo(db), groups.NewStore(rdb), a.hub, logger)
	chatRepo := chat.NewChatRepo(queries)
	a.chat = chat.NewChatService(chatRepo, chat.NewCache(rdb), a.groups, a.friends, a.hub, a.queue, logger)
	a.hub.SetPolicy(a.chat)
	a.groups.SetFriends(a.friends)
	presenceSvc := presence.NewService(queries, a.friends, a.hub, logger)
	a.hub.OnPresence(presenceSvc.Changed)
	a.calls = calls.NewManager(a.hub, a.friends, queries, logger)
	a.calls.SetDailyLimit(time.Duration(cfg.CallMinutesPerDay) * time.Minute)
	a.hub.SetCalls(a.calls)
	a.friends.OnBreak(a.calls.EndBetween)
	a.groups.OnDelete(a.chat.ForgetGroup)
	a.users.OnRename(a.friends.ForgetNames)

	// push notifications: only with a Firebase key (or a test sender)
	pushSender := o.push
	if pushSender == nil && cfg.FirebaseServiceAccount != "" {
		// a bad key turns push off; it must not keep the server from starting
		if fcm, err := push.NewFCM(ctx, []byte(cfg.FirebaseServiceAccount)); err != nil {
			logger.Error("FIREBASE_SERVICE_ACCOUNT is not a valid service account key: push notifications disabled", "error", err)
		} else {
			pushSender = fcm
			go func() { // proves the key works; the result is in the logs
				cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
				defer cancel()
				if err := fcm.Check(cctx); err != nil {
					logger.Error("FCM credentials check failed: push notifications will not arrive", "error", err)
				} else {
					logger.Info("FCM credentials OK", "project", fcm.Project())
				}
			}()
		}
	}
	a.pushSvc = push.NewService(queries, pushSender, a.hub, pushNames{users: a.users, groups: a.groups}, logger)
	if pushSender != nil {
		d := push.NewDispatcher(a.queue, logger)
		a.chat.SetNotifier(d)
		a.friends.SetNotifier(d)
		a.groups.SetNotifier(d)
		a.calls.SetNotifier(d)
	} else {
		logger.Warn("FIREBASE_SERVICE_ACCOUNT not set: push notifications disabled")
	}
	userRepo.devices = a.pushSvc

	// photos and videos: only with R2 configured (or a test store)
	store := o.media
	if store == nil && cfg.R2AccountID != "" && cfg.R2Bucket != "" && cfg.R2AccessKeyID != "" && cfg.R2SecretAccessKey != "" {
		store = media.NewR2(cfg.R2AccountID, cfg.R2Bucket, cfg.R2AccessKeyID, cfg.R2SecretAccessKey)
	}
	a.media = media.NewService(queries, store, a.groups, logger)
	a.groups.OnDelete(a.media.ExpireGroup)
	if store != nil {
		a.chat.SetMedia(a.media)
	} else {
		logger.Warn("R2 not configured (R2_ACCOUNT_ID, R2_BUCKET, R2_ACCESS_KEY_ID, R2_SECRET_ACCESS_KEY): photos and videos disabled")
	}
	a.auth = authsvc.NewAuthService(users.NewUserRepo(queries), authsvc.NewAuthRepo(queries), cfg.JWTSecret,
		cfg.AccessTokenTTL, cfg.RefreshTokenTTL, logger)
	sender := o.mailer
	switch {
	case sender != nil:
	case cfg.ResendAPIKey != "":
		sender = mail.NewResend(cfg.ResendAPIKey, cfg.MailFrom)
	case cfg.IsDev():
		sender = mail.Log{Logger: logger}
	default:
		sender = mail.Disabled{}
	}
	google := o.google
	if google == nil {
		google = authsvc.NewGoogleVerifier(cfg.GoogleClientIDs)
		if google == nil {
			logger.Warn("GOOGLE_CLIENT_IDS not set: Google sign-in answers 503")
		}
	}
	friendStore, groupStore, userStore := friends.NewStore(rdb), groups.NewStore(rdb), users.NewStore(rdb)
	accounts := authsvc.NewAccountService(a.users, userRepo, authsvc.NewAccountRepo(db), sender,
		a.auth, google, &cacheCleaner{friends: friendStore, groups: groupStore, users: userStore, hub: a.hub, revoked: a.revoked, logger: logger},
		cfg.JWTSecret, logger)

	a.admin = admin.NewService(db, userRepo, func(ctx context.Context, g uuid.UUID) {
		if err := groupStore.Invalidate(ctx, g.String()); err != nil {
			logger.WarnContext(ctx, "failed to invalidate group cache", "error", err)
		}
	}, logger)
	a.admin.OnBanChange(a.chat.BansChanged)

	a.handlers = handlers{
		users:    users.NewUserHandler(a.users),
		friends:  friends.NewHandler(a.friends),
		auth:     authsvc.NewAuthHandler(a.auth),
		account:  authsvc.NewAccountHandler(accounts),
		chat:     chat.NewChatHandler(a.chat, a.hub, ws.Upgrader(cfg.CORSOrigins), logger),
		groups:   groups.NewGroupHandler(a.groups),
		admin:    &adminHandler{queries: queries},
		discover: discover.NewHandler(discover.NewService(queries)),
		push:     push.NewHandler(a.pushSvc),
		admin2:   admin.NewHandler(a.admin),
		reports:  reports.NewHandler(reports.NewService(db, logger)),
		media:    media.NewHandler(a.media),
		presence: presence.NewHandler(presenceSvc),
		calls:    calls.NewHandler(calls.NewTURN(cfg.TURNKeyID, cfg.TURNToken), queries, logger),
		search:   search.NewHandler(search.NewService(queries, chatRepo)),
	}
	return a, nil
}

// Start runs the websocket hub and queue workers until ctx is cancelled.
func (a *App) Start(ctx context.Context) error {
	a.calls.Recover(ctx)
	go a.hub.Run()
	go a.runJanitor(ctx)
	if err := telemetry.ObserveGauges(a.hub.Count, func() telemetry.DBStats {
		s := a.db.Stat()
		return telemetry.DBStats{Acquired: int64(s.AcquiredConns()), Idle: int64(s.IdleConns()), Max: int64(s.MaxConns())}
	}); err != nil {
		a.logger.Warn("failed to register gauges", "error", err)
	}
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
	// the memory queue: run what's still waiting (pushes, read markers)
	if d, ok := a.queue.(interface{ Drain(context.Context) int }); ok {
		if left := d.Drain(shutdownCtx); left > 0 {
			a.logger.Warn("jobs dropped at shutdown", "jobs", left)
		}
	}
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
