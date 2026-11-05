// Package telemetry exports metrics with OpenTelemetry (OTLP over HTTP, e.g.
// to Grafana Cloud). It is configured with the standard environment
// variables (OTEL_EXPORTER_OTLP_ENDPOINT, OTEL_EXPORTER_OTLP_HEADERS); when
// they are unset every instrument is a no-op.
//
// Labels are low-cardinality on purpose: route patterns, kinds and
// results, never user ids or message content.
package telemetry

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Enabled reports whether an OTLP endpoint is configured.
func Enabled() bool {
	return os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" || os.Getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT") != ""
}

// Start installs the exporter (one batch per minute) and returns a
// function that flushes and stops it. Without configuration it does
// nothing.
func Start(ctx context.Context, service, version string) (func(context.Context) error, error) {
	if !Enabled() {
		return func(context.Context) error { return nil }, nil
	}
	exp, err := otlpmetrichttp.New(ctx)
	if err != nil {
		return nil, err
	}
	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(semconv.SchemaURL,
		semconv.ServiceName(service), semconv.ServiceVersion(version)))
	if err != nil && !errors.Is(err, resource.ErrSchemaURLConflict) {
		return nil, err
	}
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp, sdkmetric.WithInterval(time.Minute))))
	otel.SetMeterProvider(mp)
	return mp.Shutdown, nil
}

// Instruments are created on the global provider, which forwards to the
// real one once Start has run.
var meter = otel.Meter("chirpy")

func counter(name, desc string) metric.Int64Counter {
	c, _ := meter.Int64Counter(name, metric.WithDescription(desc))
	return c
}

var (
	httpRequests, _ = meter.Int64Counter("chirpy.http.requests", metric.WithDescription("HTTP requests by route and status"))
	httpDuration, _ = meter.Float64Histogram("chirpy.http.duration", metric.WithUnit("s"),
		metric.WithDescription("HTTP request duration"),
		metric.WithExplicitBucketBoundaries(0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10))

	rateLimited  = counter("chirpy.ratelimit.denied", "requests refused by a rate limit")
	messages     = counter("chirpy.chat.messages", "messages sent")
	chatActions  = counter("chirpy.chat.actions", "reactions, edits and deletes")
	accounts     = counter("chirpy.auth.events", "sign-ups, logins, Google sign-ins, revocations")
	pushes       = counter("chirpy.push.sent", "push notifications by result")
	mediaEvents  = counter("chirpy.media.events", "media uploads, downloads and deletions")
	jobs         = counter("chirpy.queue.jobs", "background jobs by topic and result")
	reportsC     = counter("chirpy.reports", "user reports by reason")
	callsC       = counter("chirpy.calls", "voice call events: started, answered, and how calls ended")
	neonBytes    atomic.Int64
	r2Bytes      atomic.Int64
	gaugesActive atomic.Bool
)

// HTTP records one request; route is the matched pattern ("unmatched" for
// 404s), so labels stay bounded.
func HTTP(ctx context.Context, route string, status int, d time.Duration) {
	class := "2xx"
	switch {
	case status >= 500:
		class = "5xx"
	case status >= 400:
		class = "4xx"
	case status >= 300:
		class = "3xx"
	}
	attrs := metric.WithAttributes(attribute.String("route", route), attribute.String("status", class))
	httpRequests.Add(ctx, 1, attrs)
	httpDuration.Record(ctx, d.Seconds(), attrs)
}

func add(ctx context.Context, c metric.Int64Counter, kv ...string) {
	attrs := make([]attribute.KeyValue, 0, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		attrs = append(attrs, attribute.String(kv[i], kv[i+1]))
	}
	c.Add(ctx, 1, metric.WithAttributes(attrs...))
}

// RateLimited counts a request refused by policy.
func RateLimited(ctx context.Context, policy string) { add(ctx, rateLimited, "policy", policy) }

// Message counts a sent message ("private" or "public"), with or without media.
func Message(ctx context.Context, kind string, withMedia bool) {
	media := "no"
	if withMedia {
		media = "yes"
	}
	add(ctx, messages, "type", kind, "media", media)
}

// ChatAction counts "react", "unreact", "edit" or "delete".
func ChatAction(ctx context.Context, action string) { add(ctx, chatActions, "action", action) }

// Auth counts an account event, e.g. ("login", "ok"), ("login", "failed"),
// ("signup", "ok"), ("google", "ok"), ("revoke", "ok").
func Auth(ctx context.Context, event, result string) {
	add(ctx, accounts, "event", event, "result", result)
}

// Push counts a notification by kind and result ("sent", "failed", "dead_token").
func Push(ctx context.Context, kind, result string) { add(ctx, pushes, "kind", kind, "result", result) }

// Media counts "upload", "download" or "delete" (reason: expired, lru).
func Media(ctx context.Context, event, detail string) {
	add(ctx, mediaEvents, "event", event, "detail", detail)
}

// Job counts a queue job by topic and result ("ok", "error").
func Job(ctx context.Context, topic, result string) { add(ctx, jobs, "topic", topic, "result", result) }

// StorageSizes is reported by the hourly cleanup (reading them more often
// would keep the free Neon database awake).
func StorageSizes(neon, r2 int64) {
	neonBytes.Store(neon)
	r2Bytes.Store(r2)
}

// DBStats is a snapshot of the connection pool.
type DBStats struct{ Acquired, Idle, Max int64 }

// ObserveGauges registers the gauges: live WebSocket connections and the
// database pool, read on each export; storage sizes from StorageSizes.
func ObserveGauges(ws func() int, db func() DBStats) error {
	if !gaugesActive.CompareAndSwap(false, true) {
		return nil
	}
	wsG, err := meter.Int64ObservableGauge("chirpy.ws.connections", metric.WithDescription("live WebSocket connections"))
	if err != nil {
		return err
	}
	poolG, err := meter.Int64ObservableGauge("chirpy.db.pool.connections", metric.WithDescription("Postgres pool connections by state"))
	if err != nil {
		return err
	}
	storeG, err := meter.Int64ObservableGauge("chirpy.storage.bytes", metric.WithUnit("By"),
		metric.WithDescription("bytes used: neon database, r2 media"))
	if err != nil {
		return err
	}
	_, err = meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		o.ObserveInt64(wsG, int64(ws()))
		s := db()
		o.ObserveInt64(poolG, s.Acquired, metric.WithAttributes(attribute.String("state", "acquired")))
		o.ObserveInt64(poolG, s.Idle, metric.WithAttributes(attribute.String("state", "idle")))
		o.ObserveInt64(poolG, s.Max, metric.WithAttributes(attribute.String("state", "max")))
		if n := neonBytes.Load(); n > 0 {
			o.ObserveInt64(storeG, n, metric.WithAttributes(attribute.String("store", "neon")))
		}
		o.ObserveInt64(storeG, r2Bytes.Load(), metric.WithAttributes(attribute.String("store", "r2")))
		return nil
	}, wsG, poolG, storeG)
	return err
}

// Report counts a user report by reason.
func Report(ctx context.Context, reason string) { add(ctx, reportsC, "reason", reason) }

// Call records a call event (a bounded set of names).
func Call(ctx context.Context, event string) { add(ctx, callsC, "event", event) }
