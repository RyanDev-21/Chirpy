package telemetry

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// The instruments are created before any provider is installed (package
// init); they must still report once one is, with bounded labels.
func TestInstrumentsReport(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	ctx := context.Background()

	HTTP(ctx, "/api/users/me", 200, 3*time.Millisecond)
	HTTP(ctx, "/api/users/me", 503, time.Second)
	Auth(ctx, "login", "failed")
	Message(ctx, "private", true)
	StorageSizes(123, 456)
	if err := ObserveGauges(func() int { return 7 }, func() DBStats { return DBStats{Acquired: 1, Idle: 2, Max: 10} }); err != nil {
		t.Fatal(err)
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			got[m.Name] = true
			if m.Name == "chirpy.http.requests" {
				sum := m.Data.(metricdata.Sum[int64])
				for _, dp := range sum.DataPoints {
					if v, _ := dp.Attributes.Value("route"); v.AsString() != "/api/users/me" {
						t.Errorf("route label %v", v)
					}
				}
				if len(sum.DataPoints) != 2 { // 2xx and 5xx
					t.Errorf("want 2 status classes, got %d", len(sum.DataPoints))
				}
			}
		}
	}
	for _, name := range []string{"chirpy.http.requests", "chirpy.http.duration", "chirpy.auth.events", "chirpy.chat.messages",
		"chirpy.ws.connections", "chirpy.db.pool.connections", "chirpy.storage.bytes"} {
		if !got[name] {
			t.Errorf("metric %s not reported (have %v)", name, got)
		}
	}
}
