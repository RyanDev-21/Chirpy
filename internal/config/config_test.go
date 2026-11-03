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
	t.Setenv("GENMINI_API_KEY", "legacy")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != ":8080" || cfg.IsDev() || len(cfg.CORSOrigins) != 2 || cfg.GeminiAPIKey != "legacy" {
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
