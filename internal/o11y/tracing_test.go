package o11y

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/sdk/resource"
	collog "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	coltrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/proto"
)

func setupTelemetry(t *testing.T) {
	t.Helper()
	previous, propagator, logger := otel.GetTracerProvider(), otel.GetTextMapPropagator(), loggerProvider
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 10*time.Second)
		defer cancel()
		if err := Shutdown(ctx); err != nil {
			t.Error(err)
		}
		otel.SetTracerProvider(previous)
		otel.SetTextMapPropagator(propagator)
		loggerProvider = logger
	})
	if err := Setup(t.Context(), "test-version"); err != nil {
		t.Fatal(err)
	}
}

func TestSetupInvalidResource(t *testing.T) {
	telemetryEnv(t, "http://127.0.0.1:4318")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "malformed")
	previous, logger := otel.GetTracerProvider(), loggerProvider
	if err := Setup(t.Context(), "test-version"); !errors.Is(err, resource.ErrPartialResource) {
		t.Fatalf("setup error: %v", err)
	}
	if otel.GetTracerProvider() != previous || loggerProvider != logger {
		t.Fatal("failed setup installed a partial provider")
	}
	if err := Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestFlush(t *testing.T) {
	for _, override := range []bool{false, true} {
		t.Run(fmt.Sprintf("override=%t", override), func(t *testing.T) {
			c := newTelemetryCollector(t, "")
			telemetryEnv(t, c.server.URL)
			if override {
				t.Setenv("OTEL_SERVICE_NAME", "configured-service")
				t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.version=configured-version")
			}
			setupTelemetry(t)
			ctx, cancel := context.WithCancel(t.Context())
			emitTelemetry(ctx, "work", 200)
			c.mu.Lock()
			before := len(c.traces) + len(c.logs)
			c.mu.Unlock()
			if before != 0 {
				t.Fatalf("exported %d requests before flush", before)
			}
			cancel()
			if err := Flush(context.WithoutCancel(ctx)); err != nil {
				t.Fatal(err)
			}
			c.check(t, "", override)
		})
	}
}

func TestShutdown(t *testing.T) {
	for _, stalled := range []string{"", "/v1/traces", "/v1/logs"} {
		t.Run("stalled="+stalled, func(t *testing.T) {
			c := newTelemetryCollector(t, stalled)
			telemetryEnv(t, c.server.URL)
			setupTelemetry(t)
			emitTelemetry(t.Context(), "work", 200)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- Shutdown(ctx) }()
			if stalled != "" {
				// Observe both independent exports before canceling. Serial draining cannot
				// reach the healthy endpoint while the other request is blocked.
				waitCtx, stop := context.WithTimeout(t.Context(), 10*time.Second)
				defer stop()
				for range 2 {
					select {
					case <-c.received:
					case <-waitCtx.Done():
						t.Fatal("exports did not start independently")
					}
				}
				cancel()
			}
			err := <-result
			if stalled == "" && err != nil {
				t.Fatal(err)
			}
			if stalled != "" && !errors.Is(err, context.Canceled) {
				t.Fatalf("shutdown error: %v", err)
			}
			c.check(t, stalled, false)
		})
	}
}

func TestExportBothDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		start := time.Now()
		err := exportBoth(ctx, func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }, func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() })
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) != time.Second {
			t.Fatalf("deadline: elapsed=%v err=%v", time.Since(start), err)
		}
	})
}

func emitTelemetry(ctx context.Context, prefix string, count int) {
	tracer, logger := otel.Tracer("test"), LoggerProvider().Logger("test")
	for i := range count {
		name := fmt.Sprintf("%s-%d", prefix, i)
		ctx, span := tracer.Start(ctx, name)
		var record log.Record
		record.SetBody(attribute.StringValue(name))
		logger.Emit(ctx, record)
		span.End()
	}
}

func telemetryEnv(t *testing.T, endpoint string) {
	t.Helper()
	for _, name := range []string{"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_COMPRESSION", "OTEL_EXPORTER_OTLP_LOGS_COMPRESSION", "OTEL_RESOURCE_ATTRIBUTES", "OTEL_SERVICE_NAME"} {
		t.Setenv(name, "")
	}
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", endpoint)
	t.Setenv("OTEL_EXPORTER_OTLP_COMPRESSION", "gzip")
	t.Setenv("OTEL_TRACES_SAMPLER", "always_on")
	// Only operation completion or shutdown can export this sub-batch fixture.
	for _, prefix := range []string{"OTEL_BSP_", "OTEL_BLRP_"} {
		t.Setenv(prefix+"SCHEDULE_DELAY", "3600000")
		t.Setenv(prefix+"MAX_EXPORT_BATCH_SIZE", "512")
		t.Setenv(prefix+"MAX_QUEUE_SIZE", "2048")
	}
}

type telemetryCollector struct {
	server   *httptest.Server
	mu       sync.Mutex
	traces   []*coltrace.ExportTraceServiceRequest
	logs     []*collog.ExportLogsServiceRequest
	received chan struct{}
}

func newTelemetryCollector(t *testing.T, stalled string) *telemetryCollector {
	t.Helper()
	c := &telemetryCollector{received: make(chan struct{}, 2)}
	release := make(chan struct{})
	c.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Encoding") != "gzip" {
			t.Errorf("compression: %q", r.Header.Get("Content-Encoding"))
		}
		reader, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Error(err)
			http.Error(w, "invalid gzip", http.StatusBadRequest)
			return
		}
		defer reader.Close()
		data, err := io.ReadAll(reader)
		if err != nil {
			t.Error(err)
			return
		}
		if r.URL.Path == stalled {
			select {
			case c.received <- struct{}{}:
			default:
			}
			select {
			case <-release:
			case <-r.Context().Done():
			}
			return
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		switch r.URL.Path {
		case "/v1/traces":
			req := new(coltrace.ExportTraceServiceRequest)
			if err := proto.Unmarshal(data, req); err != nil {
				t.Error(err)
				return
			}
			c.traces = append(c.traces, req)
		case "/v1/logs":
			req := new(collog.ExportLogsServiceRequest)
			if err := proto.Unmarshal(data, req); err != nil {
				t.Error(err)
				return
			}
			c.logs = append(c.logs, req)
		default:
			t.Errorf("unexpected endpoint: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		select {
		case c.received <- struct{}{}:
		default:
		}
	}))
	t.Cleanup(func() { close(release); c.server.Close() })
	return c
}

func (c *telemetryCollector) check(t *testing.T, stalled string, override bool) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	wantTrace, wantLog := 1, 1
	if stalled == "/v1/traces" {
		wantTrace = 0
	}
	if stalled == "/v1/logs" {
		wantLog = 0
	}
	if len(c.traces) != wantTrace || len(c.logs) != wantLog {
		t.Fatalf("batches: traces=%d logs=%d, want %d/%d", len(c.traces), len(c.logs), wantTrace, wantLog)
	}
	checkResource := func(attrs []*commonpb.KeyValue) {
		t.Helper()
		values := make(map[string]string, len(attrs))
		for _, a := range attrs {
			values[a.Key] = a.Value.GetStringValue()
		}
		service, version := "imagetest", "test-version"
		if override {
			service, version = "configured-service", "configured-version"
		}
		if values["service.name"] != service || values["service.version"] != version || values["telemetry.sdk.language"] != "go" {
			t.Errorf("resource identity: %v", values)
		}
	}
	traces, logs := make(map[string]string), make(map[string]string)
	for _, req := range c.traces {
		for _, rs := range req.ResourceSpans {
			checkResource(rs.Resource.Attributes)
			for _, ss := range rs.ScopeSpans {
				for _, span := range ss.Spans {
					traces[span.Name] = fmt.Sprintf("%x/%x", span.TraceId, span.SpanId)
				}
			}
		}
	}
	for _, req := range c.logs {
		for _, rs := range req.ResourceLogs {
			checkResource(rs.Resource.Attributes)
			for _, ss := range rs.ScopeLogs {
				for _, record := range ss.LogRecords {
					logs[record.Body.GetStringValue()] = fmt.Sprintf("%x/%x", record.TraceId, record.SpanId)
				}
			}
		}
	}
	if len(traces) != 200*wantTrace || len(logs) != 200*wantLog {
		t.Fatalf("unique spans=%d logs=%d, want %d/%d", len(traces), len(logs), 200*wantTrace, 200*wantLog)
	}
	if wantTrace != 0 && wantLog != 0 {
		for name, id := range traces {
			if len(id) != 49 || logs[name] != id {
				t.Errorf("log correlation for %q: got %s, want %s", name, logs[name], id)
			}
		}
	}
}
