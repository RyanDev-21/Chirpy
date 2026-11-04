// Package config loads server settings from the environment.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	Addr     string
	Platform string // "dev" enables admin routes

	DatabaseURL string
	// DBMaxConns caps the Postgres pool. DBMaxIdleTime closes idle
	// connections so serverless Postgres (Neon) can scale to zero.
	DBMaxConns    int32
	DBMaxIdleTime time.Duration
	// MigrateOnStart applies embedded migrations at startup (needed on
	// hosts without a shell, e.g. Render's free tier).
	MigrateOnStart bool

	// RedisURL (redis:// or rediss:// for TLS, e.g. Upstash) wins over
	// the individual address fields.
	RedisURL           string
	RedisAddr          string
	RedisUser          string
	RedisPass          string
	RedisDB            int
	RedisPoolSize      int
	QueueRedisPoolSize int

	// QueueBackend is "redis" (durable Redis Streams) or "memory"
	// (in-process; jobs are lost on restart). Use memory on Redis plans
	// billed per command: the stream readers poll even when idle.
	QueueBackend string

	JWTSecret string
	// Token lifetimes; zero means the defaults (15 minutes, 30 days).
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration

	// ResendAPIKey enables email (verification and reset codes). Without it
	// emails are printed to the log on PLATFORM=dev and refused elsewhere.
	ResendAPIKey string
	MailFrom     string
	// GoogleClientIDs are the OAuth client IDs Google ID tokens may be
	// issued to. For Android sign-in this is the *Web* client ID the app
	// passes as serverClientId. Empty disables Google sign-in.
	GoogleClientIDs []string
	// FirebaseServiceAccount is the service account key JSON for push
	// notifications (FCM). Empty disables push.
	FirebaseServiceAccount string
	// Cloudflare R2 for photos and videos; all four are needed, otherwise
	// media is off.
	R2AccountID, R2Bucket, R2AccessKeyID, R2SecretAccessKey string

	TLSCertFile string // empty: serve plain HTTP (e.g. behind a TLS proxy)
	TLSKeyFile  string

	CORSOrigins []string

	// TrustedProxies is the number of reverse proxies in front of the
	// server (Render: 1, Caddy on a VPS: 1, none: 0). It selects which
	// X-Forwarded-For entry is the real client; see httpx.ClientIP.
	TrustedProxies int

	ShutdownTimeout time.Duration

	LogLevel  string // debug | info | warn | error
	LogFormat string // json | text
}

func (c Config) IsDev() bool { return c.Platform == "dev" }

// Warnings lists settings that work but are unsafe outside development.
func (c Config) Warnings() []string {
	if c.IsDev() {
		return nil
	}
	var w []string
	if u, err := url.Parse(c.DatabaseURL); err == nil && u.Query().Get("sslmode") != "verify-full" {
		w = append(w, "DB_URL does not use sslmode=verify-full: the database certificate is not verified")
	}
	if c.ResendAPIKey == "" {
		w = append(w, "RESEND_API_KEY is not set: sign-up and password reset cannot send codes")
	}
	if c.RedisURL != "" && !strings.HasPrefix(c.RedisURL, "rediss://") {
		w = append(w, "REDIS_URL is not rediss://: Redis traffic is not encrypted")
	}
	return w
}

// Load reads an optional .env file (ENV_FILE, ./.env or ../.env) and then
// the environment. Real environment variables take precedence.
func Load() (Config, error) {
	loadEnvFile()

	c := Config{
		Addr:                   listenAddr(),
		Platform:               getenv("PLATFORM", "prod"),
		DatabaseURL:            os.Getenv("DB_URL"),
		RedisURL:               os.Getenv("REDIS_URL"),
		RedisAddr:              getenv("REDIS_ADDR", "localhost:6379"),
		QueueBackend:           getenv("QUEUE_BACKEND", "redis"),
		RedisUser:              os.Getenv("REDIS_USR"),
		RedisPass:              os.Getenv("REDIS_PASS"),
		JWTSecret:              os.Getenv("SECRET"),
		ResendAPIKey:           os.Getenv("RESEND_API_KEY"),
		MailFrom:               getenv("MAIL_FROM", "Chirpy <no-reply@mail.ponypigeon.com>"),
		GoogleClientIDs:        splitList(os.Getenv("GOOGLE_CLIENT_IDS")),
		FirebaseServiceAccount: os.Getenv("FIREBASE_SERVICE_ACCOUNT"),
		R2AccountID:            os.Getenv("R2_ACCOUNT_ID"),
		R2Bucket:               os.Getenv("R2_BUCKET"),
		R2AccessKeyID:          os.Getenv("R2_ACCESS_KEY_ID"),
		R2SecretAccessKey:      os.Getenv("R2_SECRET_ACCESS_KEY"),
		TLSCertFile:            os.Getenv("TLS_CERT_FILE"),
		TLSKeyFile:             os.Getenv("TLS_KEY_FILE"),
		CORSOrigins:            splitList(getenv("CORS_ALLOWED_ORIGINS", "")),
		ShutdownTimeout:        15 * time.Second,
		LogLevel:               getenv("LOG_LEVEL", "info"),
		LogFormat:              getenv("LOG_FORMAT", "json"),
	}
	var err error
	if c.RedisPoolSize, err = atoi("REDIS_POOL_SIZE", 0); err != nil {
		return c, err
	}
	if c.QueueRedisPoolSize, err = atoi("QUEUE_REDIS_POOL_SIZE", 16); err != nil {
		return c, err
	}
	maxConns, err := atoi("DB_MAX_CONNS", 10)
	if err != nil {
		return c, err
	}
	c.DBMaxConns = int32(maxConns)
	if c.DBMaxIdleTime, err = time.ParseDuration(getenv("DB_MAX_IDLE_TIME", "2m")); err != nil {
		return c, fmt.Errorf("DB_MAX_IDLE_TIME: %w", err)
	}
	if c.MigrateOnStart, err = strconv.ParseBool(getenv("MIGRATE_ON_START", "true")); err != nil {
		return c, fmt.Errorf("MIGRATE_ON_START: %w", err)
	}
	if c.TrustedProxies, err = atoi("TRUSTED_PROXIES", 0); err != nil {
		return c, err
	}
	if c.RedisDB, err = strconv.Atoi(getenv("REDIS_DB", "0")); err != nil {
		return c, fmt.Errorf("REDIS_DB: %w", err)
	}
	return c, c.validate()
}

func (c Config) validate() error {
	var errs []error
	if c.DatabaseURL == "" {
		errs = append(errs, errors.New("DB_URL is required"))
	}
	if len(c.JWTSecret) < 32 {
		errs = append(errs, errors.New("SECRET must be at least 32 characters"))
	}
	if c.QueueBackend != "redis" && c.QueueBackend != "memory" {
		errs = append(errs, errors.New(`QUEUE_BACKEND must be "redis" or "memory"`))
	}
	if (c.TLSCertFile == "") != (c.TLSKeyFile == "") {
		errs = append(errs, errors.New("TLS_CERT_FILE and TLS_KEY_FILE must be set together"))
	}
	return errors.Join(errs...)
}

func loadEnvFile() {
	if f := os.Getenv("ENV_FILE"); f != "" {
		_ = godotenv.Load(f)
		return
	}
	for _, f := range []string{".env", "../.env"} {
		if _, err := os.Stat(f); err == nil {
			_ = godotenv.Load(f)
			return
		}
	}
}

// listenAddr honours PORT (set by Render, Fly, Heroku-style hosts), then
// ADDR, then :8080.
func listenAddr() string {
	if p := os.Getenv("PORT"); p != "" {
		return ":" + p
	}
	return getenv("ADDR", ":8080")
}

func atoi(k string, def int) (int, error) {
	v := os.Getenv(k)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", k)
	}
	return n, nil
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
