package o11y

import (
	"context"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
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

// SetupTracing configures the global otel TracerProvider. When
// OTEL_EXPORTER_OTLP_TRACES_ENDPOINT is set, spans are exported via OTLP/HTTP.
func SetupTracing(ctx context.Context) error {
	if os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") == "" {
		return nil
	}

	exporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return err
	}

	res, err := resource.New(ctx, resource.WithFromEnv())
	if err != nil {
		return err
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(provider)

	return nil
}
