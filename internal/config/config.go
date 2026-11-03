// Package config loads server settings from the environment.
package config

import (
	"errors"
	"fmt"
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

	RedisAddr string
	RedisUser string
	RedisPass string
	RedisDB   int

	JWTSecret       string
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration

	GeminiAPIKey string

	TLSCertFile string // empty: serve plain HTTP (e.g. behind a TLS proxy)
	TLSKeyFile  string

	CORSOrigins []string

	// TrustProxyHeaders makes the server take the client IP from
	// X-Forwarded-For / X-Real-IP. Only enable it behind a proxy you control
	// that overwrites those headers; otherwise clients can spoof their IP
	// and bypass per-IP rate limits.
	TrustProxyHeaders bool

	ShutdownTimeout time.Duration
}

func (c Config) IsDev() bool { return c.Platform == "dev" }

// Load reads an optional .env file (ENV_FILE, ./.env or ../.env) and then
// the environment. Real environment variables take precedence.
func Load() (Config, error) {
	loadEnvFile()

	c := Config{
		Addr:            getenv("ADDR", ":8080"),
		Platform:        getenv("PLATFORM", "prod"),
		DatabaseURL:     os.Getenv("DB_URL"),
		RedisAddr:       getenv("REDIS_ADDR", "localhost:6379"),
		RedisUser:       os.Getenv("REDIS_USR"),
		RedisPass:       os.Getenv("REDIS_PASS"),
		JWTSecret:       os.Getenv("SECRET"),
		GeminiAPIKey:    firstNonEmpty(os.Getenv("GEMINI_API_KEY"), os.Getenv("GENMINI_API_KEY")),
		TLSCertFile:     os.Getenv("TLS_CERT_FILE"),
		TLSKeyFile:      os.Getenv("TLS_KEY_FILE"),
		CORSOrigins:     splitList(getenv("CORS_ALLOWED_ORIGINS", "")),
		AccessTokenTTL:  time.Hour,
		RefreshTokenTTL: 30 * 24 * time.Hour,
		ShutdownTimeout: 15 * time.Second,
	}
	var err error
	if c.TrustProxyHeaders, err = strconv.ParseBool(getenv("TRUST_PROXY_HEADERS", "false")); err != nil {
		return c, fmt.Errorf("TRUST_PROXY_HEADERS: %w", err)
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

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
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
