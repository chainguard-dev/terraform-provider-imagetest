package o11y

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	collog "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	coltrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

// Include OTLP encoding, HTTP, and the final flush. Measuring enqueue alone
// would hide both network cost and records dropped by a full queue.
func BenchmarkTelemetry(b *testing.B) {
	for _, workload := range []struct {
		name                string
		suites, tests, logs int
	}{
		{"short", 1, 20, 5},
		{"parallel_log_heavy", 8, 25, 10},
	} {
		for _, batch := range []bool{false, true} {
			b.Run(fmt.Sprintf("%s/batch=%t", workload.name, batch), func(b *testing.B) {
				var requests, wireBytes, spans, records atomic.Int64
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					data, err := io.ReadAll(r.Body)
					if err != nil {
						b.Error(err)
						return
					}
					requests.Add(1)
					wireBytes.Add(int64(len(data)))
					if r.Header.Get("Content-Encoding") == "gzip" {
						reader, err := gzip.NewReader(bytes.NewReader(data))
						if err != nil {
							b.Error(err)
							return
						}
						defer reader.Close()
						data, err = io.ReadAll(reader)
						if err != nil {
							b.Error(err)
							return
						}
					}
					switch r.URL.Path {
					case "/v1/traces":
						var req coltrace.ExportTraceServiceRequest
						if err := proto.Unmarshal(data, &req); err != nil {
							b.Error(err)
							return
						}
						for _, rs := range req.ResourceSpans {
							for _, ss := range rs.ScopeSpans {
								spans.Add(int64(len(ss.Spans)))
							}
						}
					case "/v1/logs":
						var req collog.ExportLogsServiceRequest
						if err := proto.Unmarshal(data, &req); err != nil {
							b.Error(err)
							return
						}
						for _, rs := range req.ResourceLogs {
							for _, ss := range rs.ScopeLogs {
								records.Add(int64(len(ss.LogRecords)))
							}
						}
					}
					w.Header().Set("Content-Type", "application/x-protobuf")
				}))
				defer server.Close()
				for _, key := range []string{"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_COMPRESSION", "OTEL_EXPORTER_OTLP_LOGS_COMPRESSION", "OTEL_RESOURCE_ATTRIBUTES", "OTEL_SERVICE_NAME", "OTEL_BSP_MAX_QUEUE_SIZE", "OTEL_BLRP_MAX_QUEUE_SIZE", "OTEL_BSP_MAX_EXPORT_BATCH_SIZE", "OTEL_BLRP_MAX_EXPORT_BATCH_SIZE", "OTEL_BSP_SCHEDULE_DELAY", "OTEL_BLRP_SCHEDULE_DELAY"} {
					b.Setenv(key, "")
				}
				b.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", server.URL)
				b.Setenv("OTEL_EXPORTER_OTLP_COMPRESSION", "gzip")
				b.Setenv("OTEL_TRACES_SAMPLER", "always_on")
				prevTrace, prevLog, prevProp := otel.GetTracerProvider(), loggerProvider, otel.GetTextMapPropagator()
				defer func() {
					otel.SetTracerProvider(prevTrace)
					loggerProvider = prevLog
					otel.SetTextMapPropagator(prevProp)
				}()
				if err := Setup(b.Context(), "benchmark"); err != nil {
					b.Fatal(err)
				}
				if !batch {
					// Match production identity/exporter options while restoring the former
					// synchronous processors as the baseline.
					res, err := resource.New(b.Context(), resource.WithTelemetrySDK(), resource.WithAttributes(semconv.ServiceName("imagetest"), semconv.ServiceVersion("benchmark")))
					if err != nil {
						b.Fatal(err)
					}
					if err := Shutdown(b.Context()); err != nil {
						b.Fatal(err)
					}
					te, err := otlptracehttp.New(b.Context())
					if err != nil {
						b.Fatal(err)
					}
					le, err := otlploghttp.New(b.Context())
					if err != nil {
						b.Fatal(err)
					}
					otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSyncer(te), sdktrace.WithResource(res)))
					loggerProvider = sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(le)), sdklog.WithResource(res))
				}
				defer func() {
					ctx, cancel := context.WithTimeout(context.WithoutCancel(b.Context()), 10*time.Second)
					defer cancel()
					if err := Shutdown(ctx); err != nil {
						b.Error(err)
					}
				}()
				tracer, logger := otel.Tracer("imagetest"), loggerProvider.Logger("imagetest")
				for b.Loop() {
					var wg sync.WaitGroup
					for suite := range workload.suites {
						wg.Go(func() {
							for test := range workload.tests {
								ctx, span := tracer.Start(b.Context(), "imagetest.test")
								span.SetAttributes(attribute.Int("suite", suite), attribute.Int("test", test))
								for range workload.logs {
									var record log.Record
									record.SetBody(attribute.StringValue("running test command: checking the container's installed dependencies"))
									logger.Emit(ctx, record)
								}
								span.End()
							}
							ctx, cancel := context.WithTimeout(b.Context(), 10*time.Second)
							defer cancel()
							if err := Flush(ctx); err != nil {
								b.Error(err)
							}
						})
					}
					wg.Wait()
				}
				want := int64(b.N * workload.suites * workload.tests)
				if spans.Load() != want || records.Load() != want*int64(workload.logs) {
					b.Fatalf("delivered spans=%d logs=%d, want %d/%d", spans.Load(), records.Load(), want, want*int64(workload.logs))
				}
				b.ReportMetric(float64(requests.Load())/float64(b.N), "requests/op")
				b.ReportMetric(float64(wireBytes.Load())/float64(b.N), "wire-bytes/op")
			})
		}
	}
}
