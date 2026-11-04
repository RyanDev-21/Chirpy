package main

import (
	"RyanDev-21.com/Chirpy/internal/platform/telemetry"
	"context"
	"log/slog"
	"os"
	"os/signal"
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

// version is the deployed commit on Render (RENDER_GIT_COMMIT), else "dev".
func version() string {
	if v := os.Getenv("RENDER_GIT_COMMIT"); len(v) >= 7 {
		return v[:7]
	}
	return "dev"
}
