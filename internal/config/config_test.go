package config

import (
	"strings"
	"testing"
)

func TestLoadValidates(t *testing.T) {
	t.Setenv("ENV_FILE", "/dev/null")
	t.Setenv("DB_URL", "")
	t.Setenv("SECRET", "short")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "DB_URL") || !strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("expected DB_URL and SECRET errors, got %v", err)
	}
}

func TestLoadDefaults(t *testing.T) {
	t.Setenv("ENV_FILE", "/dev/null")
	t.Setenv("DB_URL", "postgres://x")
	t.Setenv("SECRET", strings.Repeat("s", 32))
	t.Setenv("CORS_ALLOWED_ORIGINS", " https://a , https://b ,")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != ":8080" || cfg.IsDev() || len(cfg.CORSOrigins) != 2 {
		t.Fatalf("unexpected config %+v", cfg)
	}
}

func TestTLSPairRequired(t *testing.T) {
	t.Setenv("ENV_FILE", "/dev/null")
	t.Setenv("DB_URL", "postgres://x")
	t.Setenv("SECRET", strings.Repeat("s", 32))
	t.Setenv("TLS_CERT_FILE", "cert.pem")
	if _, err := Load(); err == nil {
		t.Fatal("expected an error when only the cert is set")
	}
}

func base(t *testing.T) {
	t.Setenv("ENV_FILE", "/dev/null")
	t.Setenv("DB_URL", "postgres://x")
	t.Setenv("SECRET", strings.Repeat("s", 32))
}

func TestPortWinsOverAddr(t *testing.T) {
	base(t)
	t.Setenv("ADDR", ":9999")
	t.Setenv("PORT", "10000")
	cfg, err := Load()
	if err != nil || cfg.Addr != ":10000" {
		t.Fatalf("Addr = %q, %v", cfg.Addr, err)
	}
}

func TestDeploySettings(t *testing.T) {
	base(t)
	t.Setenv("QUEUE_BACKEND", "memory")
	t.Setenv("DB_MAX_CONNS", "5")
	t.Setenv("DB_MAX_IDLE_TIME", "90s")
	t.Setenv("MIGRATE_ON_START", "false")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.QueueBackend != "memory" || cfg.DBMaxConns != 5 || cfg.DBMaxIdleTime.Seconds() != 90 || cfg.MigrateOnStart {
		t.Fatalf("unexpected %+v", cfg)
	}
}

func TestRejectsBadDeploySettings(t *testing.T) {
	for k, v := range map[string]string{"QUEUE_BACKEND": "kafka", "DB_MAX_CONNS": "-1", "DB_MAX_IDLE_TIME": "soon"} {
		t.Run(k, func(t *testing.T) {
			base(t)
			t.Setenv(k, v)
			if _, err := Load(); err == nil {
				t.Fatalf("%s=%s accepted", k, v)
			}
		})
	}
}

func TestWarnsAboutUnverifiedTLS(t *testing.T) {
	base(t)
	t.Setenv("PLATFORM", "prod")
	t.Setenv("RESEND_API_KEY", "re_x")
	t.Setenv("DB_URL", "postgres://u:p@h/db?sslmode=require")
	t.Setenv("REDIS_URL", "redis://h:6379")
	cfg, _ := Load()
	if len(cfg.Warnings()) != 2 {
		t.Fatalf("warnings = %v", cfg.Warnings())
	}
	t.Setenv("DB_URL", "postgres://u:p@h/db?sslmode=verify-full")
	t.Setenv("REDIS_URL", "rediss://h:6379")
	cfg, _ = Load()
	if len(cfg.Warnings()) != 0 {
		t.Fatalf("warnings = %v", cfg.Warnings())
	}
}

func TestWarnsWithoutEmailProvider(t *testing.T) {
	base(t)
	t.Setenv("PLATFORM", "prod")
	t.Setenv("DB_URL", "postgres://u:p@h/db?sslmode=verify-full")
	cfg, _ := Load()
	if w := cfg.Warnings(); len(w) != 1 || !strings.Contains(w[0], "RESEND_API_KEY") {
		t.Fatalf("warnings = %v", w)
	}
	t.Setenv("GOOGLE_CLIENT_IDS", "a.apps.googleusercontent.com, b.apps.googleusercontent.com")
	if cfg, _ = Load(); len(cfg.GoogleClientIDs) != 2 {
		t.Fatalf("GoogleClientIDs = %v", cfg.GoogleClientIDs)
	}
}
