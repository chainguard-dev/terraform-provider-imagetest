package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"sync"
	"testing"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/o11y"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-plugin"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	collog "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	coltrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

// Run the real go-plugin client/server shutdown: an explicit SDK Shutdown call
// alone cannot catch Terraform killing the process before its drain completes.
func TestTelemetryProcess(t *testing.T) {
	if mode := os.Getenv("IMAGETEST_TELEMETRY_TEST"); mode != "" {
		if err := o11y.Setup(setupLog(t.Context()), "test-version"); err != nil {
			t.Fatal(err)
		}
		if mode == "apply" {
			ctx, cancel := context.WithCancel(t.Context())
			result := &tfprotov6.ApplyResourceChangeResponse{Diagnostics: []*tfprotov6.Diagnostic{{Summary: "resource failed"}}}
			applyErr := errors.New("resource error")
			server := telemetryServer{ProviderServer: &testApplyServer{apply: func(ctx context.Context, _ *tfprotov6.ApplyResourceChangeRequest) (*tfprotov6.ApplyResourceChangeResponse, error) {
				emitTelemetry(ctx, "work", 200)
				cancel()
				return result, applyErr
			}}}
			if got, err := server.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{}); got != result || err != applyErr {
				t.Fatalf("apply changed resource response: %v, %v", got, err)
			}
		} else {
			emitTelemetry(t.Context(), "work", 200)
		}
		if err := serve(false); err != nil {
			t.Fatal(err)
		}
		return
	}
	for _, tc := range []struct {
		name, mode, stalled string
		retry, override     bool
	}{
		{name: "shutdown", mode: "shutdown"},
		{name: "shutdown_trace_stalled", mode: "shutdown", stalled: "/v1/traces"},
		{name: "shutdown_logs_stalled", mode: "shutdown", stalled: "/v1/logs"},
		{name: "apply_rpc", mode: "rpc"},
		{name: "apply_trace_stalled", mode: "rpc", stalled: "/v1/traces"},
		{name: "apply_canceled_and_failed", mode: "apply"},
		{name: "apply_retry", mode: "rpc", retry: true},
		{name: "resource_override", mode: "apply", override: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			collector := newTelemetryCollector(t, tc.stalled, tc.retry)
			telemetryEnv(t, collector.server.URL)
			if tc.override {
				t.Setenv("OTEL_SERVICE_NAME", "configured-service")
				t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.version=configured-version")
			}
			self, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(t.Context(), self, "-test.run=^TestTelemetryProcess$")
			// The race detector's one-second exit sleep would consume go-plugin's
			// shutdown allowance; keep race detection enabled without that artificial wait.
			cmd.Env = append(os.Environ(), "IMAGETEST_TELEMETRY_TEST="+tc.mode, "GORACE="+os.Getenv("GORACE")+" atexit_sleep_ms=0")
			var stderr bytes.Buffer
			client := plugin.NewClient(&plugin.ClientConfig{
				ProtocolVersion: 6, MagicCookieKey: "TF_PLUGIN_MAGIC_COOKIE", MagicCookieValue: "d602bf8f470bc67ca7faa0386276bbdd4330efaf76d1a219cb4d6991ca9872b2",
				Plugins: plugin.PluginSet{"provider": &testProviderPlugin{}}, Cmd: cmd, AllowedProtocols: []plugin.Protocol{plugin.ProtocolGRPC}, Logger: hclog.NewNullLogger(), Stderr: &stderr,
			})
			defer client.Kill()
			protocol, err := client.Client()
			if err != nil {
				t.Fatalf("start: %v\n%s", err, &stderr)
			}
			if tc.mode == "rpc" {
				raw, err := protocol.Dispense("provider")
				if err != nil {
					t.Fatal(err)
				}
				conn, ok := raw.(*grpc.ClientConn)
				if !ok {
					t.Fatalf("client: %T", raw)
				}
				// An empty request produces a framework diagnostic. The RPC still must
				// flush completed telemetry before returning that response.
				if err := conn.Invoke(t.Context(), "/tfplugin6.Provider/ApplyResourceChange", &emptypb.Empty{}, &emptypb.Empty{}); err != nil {
					t.Fatal(err)
				}
				collector.check(t, tc.stalled, tc.override)
			}
			if tc.mode == "apply" {
				collector.check(t, tc.stalled, tc.override)
			}
			client.Kill()
			if !cmd.ProcessState.Success() {
				t.Fatalf("provider exit: %v\n%s", cmd.ProcessState, &stderr)
			}
			collector.check(t, tc.stalled, tc.override)
			if tc.stalled != "" {
				phase := "shutdown"
				if tc.mode == "rpc" {
					phase = "flush"
				}
				if !bytes.Contains(stderr.Bytes(), []byte("otel "+phase+":")) {
					t.Errorf("missing %s failure: %s", phase, &stderr)
				}
			}
		})
	}
}

type testProviderPlugin struct{ plugin.NetRPCUnsupportedPlugin }

func (*testProviderPlugin) GRPCServer(*plugin.GRPCBroker, *grpc.Server) error {
	return errors.New("client only")
}

func (*testProviderPlugin) GRPCClient(_ context.Context, _ *plugin.GRPCBroker, conn *grpc.ClientConn) (any, error) {
	return conn, nil
}

type testApplyServer struct {
	tfprotov6.ProviderServer
	apply func(context.Context, *tfprotov6.ApplyResourceChangeRequest) (*tfprotov6.ApplyResourceChangeResponse, error)
}

func (s *testApplyServer) ApplyResourceChange(ctx context.Context, req *tfprotov6.ApplyResourceChangeRequest) (*tfprotov6.ApplyResourceChangeResponse, error) {
	return s.apply(ctx, req)
}

func emitTelemetry(ctx context.Context, prefix string, count int) {
	tracer, logger := otel.Tracer("test"), o11y.LoggerProvider().Logger("test")
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
	attempts map[string]int
}

func newTelemetryCollector(t *testing.T, stalled string, retry bool) *telemetryCollector {
	t.Helper()
	c := &telemetryCollector{attempts: make(map[string]int)}
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
			case <-release:
			case <-r.Context().Done():
			}
			return
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		c.attempts[r.URL.Path]++
		if retry && c.attempts[r.URL.Path] == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
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
