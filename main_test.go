package main

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/o11y"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	collog "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	coltrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/proto"
)

func TestTelemetryBatchShutdown(t *testing.T) {
	for _, configured := range []bool{false, true} {
		t.Run(fmt.Sprintf("configured=%t", configured), func(t *testing.T) {
			var mu sync.Mutex
			var traces []*coltrace.ExportTraceServiceRequest
			var logs []*collog.ExportLogsServiceRequest
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
				mu.Lock()
				defer mu.Unlock()
				switch r.URL.Path {
				case "/v1/traces":
					req := new(coltrace.ExportTraceServiceRequest)
					if err := proto.Unmarshal(data, req); err != nil {
						t.Error(err)
						return
					}
					traces = append(traces, req)
				case "/v1/logs":
					req := new(collog.ExportLogsServiceRequest)
					if err := proto.Unmarshal(data, req); err != nil {
						t.Error(err)
						return
					}
					logs = append(logs, req)
				default:
					t.Errorf("unexpected endpoint: %s", r.URL.Path)
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/x-protobuf")
			}))
			defer server.Close()
			for _, name := range []string{"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_COMPRESSION", "OTEL_EXPORTER_OTLP_LOGS_COMPRESSION", "OTEL_RESOURCE_ATTRIBUTES", "OTEL_SERVICE_NAME"} {
				t.Setenv(name, "")
			}
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", server.URL)
			t.Setenv("OTEL_EXPORTER_OTLP_COMPRESSION", "gzip")
			t.Setenv("OTEL_TRACES_SAMPLER", "always_on")
			// Keep timer and size triggers out of this test: shutdown must flush the short process's queues.
			for _, prefix := range []string{"OTEL_BSP_", "OTEL_BLRP_"} {
				t.Setenv(prefix+"SCHEDULE_DELAY", "3600000")
				t.Setenv(prefix+"MAX_EXPORT_BATCH_SIZE", "512")
				t.Setenv(prefix+"MAX_QUEUE_SIZE", "2048")
			}
			wantService, wantVersion := "imagetest", "test-version"
			if configured {
				wantService, wantVersion = "configured-service", "configured-version"
				t.Setenv("OTEL_SERVICE_NAME", wantService)
				t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.version="+wantVersion)
			}
			previous := otel.GetTracerProvider()
			propagator := otel.GetTextMapPropagator()
			t.Cleanup(func() { otel.SetTracerProvider(previous); otel.SetTextMapPropagator(propagator) })
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if err := o11y.Setup(ctx, "test-version"); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := shutdownOTel(); err != nil {
					t.Error(err)
				}
			})
			tracer := otel.Tracer("batch-test")
			logger := o11y.LoggerProvider().Logger("batch-test")
			for i := range 200 {
				spanCtx, span := tracer.Start(ctx, fmt.Sprintf("work-%d", i))
				var record log.Record
				record.SetBody(attribute.StringValue("diagnostic"))
				logger.Emit(spanCtx, record)
				span.End()
			}
			mu.Lock()
			before := len(traces) + len(logs)
			mu.Unlock()
			if before != 0 {
				t.Fatalf("exported %d requests before batch flush", before)
			}
			cancel()
			if err := shutdownOTel(); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(traces) != 1 || len(logs) != 1 {
				t.Fatalf("requests: traces=%d logs=%d, want one batch each", len(traces), len(logs))
			}
			checkResource := func(attrs []*commonpb.KeyValue) {
				t.Helper()
				values := make(map[string]string, len(attrs))
				for _, a := range attrs {
					values[a.Key] = a.Value.GetStringValue()
				}
				if values["service.name"] != wantService || values["service.version"] != wantVersion {
					t.Errorf("resource identity: %v", values)
				}
				if values["telemetry.sdk.language"] != "go" {
					t.Errorf("missing SDK resource identity: %v", values)
				}
			}
			spanCount, logCount := 0, 0
			for _, rs := range traces[0].ResourceSpans {
				checkResource(rs.Resource.Attributes)
				for _, ss := range rs.ScopeSpans {
					spanCount += len(ss.Spans)
				}
			}
			for _, rs := range logs[0].ResourceLogs {
				checkResource(rs.Resource.Attributes)
				for _, ss := range rs.ScopeLogs {
					for _, record := range ss.LogRecords {
						logCount++
						if len(record.TraceId) != 16 || len(record.SpanId) != 8 {
							t.Error("log lost its span context")
						}
					}
				}
			}
			if spanCount != 200 || logCount != 200 {
				t.Fatalf("delivered spans=%d logs=%d, want 200 each", spanCount, logCount)
			}
		})
	}
}
