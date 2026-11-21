package main

import (
	"RyanDev-21.com/Chirpy/internal/platform/telemetry"
	"context"
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"RyanDev-21.com/Chirpy/internal/app"
	"RyanDev-21.com/Chirpy/internal/config"
	"RyanDev-21.com/Chirpy/internal/platform/reqctx"
)

func main() {
	cfg, err := config.Load()
	logger := reqctx.NewLogger(os.Stdout, cfg.LogFormat, cfg.LogLevel)
	slog.SetDefault(logger)
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	for _, w := range cfg.Warnings() {
		logger.Warn("insecure configuration", "detail", w)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// metrics to OTEL_EXPORTER_OTLP_ENDPOINT (e.g. Grafana Cloud); off if unset
	stopMetrics, err := telemetry.Start(ctx, "chirpy-api", version())
	if err != nil {
		logger.Error("metrics exporter failed to start; continuing without metrics", "error", err)
		stopMetrics = func(context.Context) error { return nil }
	} else if telemetry.Enabled() {
		logger.Info("exporting metrics", "endpoint", os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	}
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = stopMetrics(sctx) // flush the last minute
	}()

	a, err := app.New(ctx, cfg, logger)
	if err != nil {
		logger.Error("startup failed", "error", err)
		os.Exit(1)
	}
	defer a.Close()

	if err := a.Run(ctx); err != nil {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

// buildVersion is set at build time: go build -ldflags "-X main.buildVersion=<commit>"
// (the Dockerfile's VERSION build argument).
var buildVersion string

// version is the deployed commit: from the build, from Render
// (RENDER_GIT_COMMIT), from Go's own build info when built from a git
// checkout, else "dev".
func version() string {
	for _, v := range []string{buildVersion, os.Getenv("RENDER_GIT_COMMIT"), vcsRevision()} {
		if len(v) >= 7 {
			return v[:7]
		}
	}
	return "dev"
}

func vcsRevision() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" {
				return s.Value
			}
		}
	}
	return ""
}
