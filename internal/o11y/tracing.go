package o11y

import (
	"context"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Attribute keys used on span attributes and clog context values.
const (
	AttrTestID = "test_id"
	AttrName   = "name"
	AttrDriver = "driver"
	AttrTest   = "test"
)

// loggerProvider holds the global LoggerProvider for use by the OTel slog
// handler. Nil when OTLP is not configured.
var loggerProvider *sdklog.LoggerProvider

// LoggerProvider returns the configured LoggerProvider, or nil.
func LoggerProvider() *sdklog.LoggerProvider { return loggerProvider }

// Setup configures the global OTel TracerProvider and LoggerProvider. This is
// a no-op when no OTLP endpoint is configured.
func Setup(ctx context.Context) error {
	// Check both the generic and trace-specific env vars.
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" &&
		os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") == "" {
		return nil
	}

	res, err := resource.New(ctx, resource.WithFromEnv())
	if err != nil {
		return err
	}

	traceExp, err := otlptracehttp.New(ctx)
	if err != nil {
		return err
	}
	otel.SetTracerProvider(sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(traceExp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	))

	logExp, err := otlploghttp.New(ctx)
	if err != nil {
		return err
	}
	loggerProvider = sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewSimpleProcessor(logExp)),
		sdklog.WithResource(res),
	)

	return nil
}
