package o11y

import (
	"context"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"google.golang.org/grpc/metadata"
)

// Attribute keys used on span attributes and clog context values.
const (
	AttrTestID = "test_id"
	AttrName   = "name"
	AttrDriver = "driver"
	AttrTest   = "test"
)

// LoggerProvider returns the configured LoggerProvider, or nil.
// This is the SDK's global, set by Setup.
func LoggerProvider() *sdklog.LoggerProvider { return loggerProvider }

var loggerProvider *sdklog.LoggerProvider

// Setup configures the global OTel TracerProvider and LoggerProvider. This is
// a no-op when no OTLP endpoint is configured.
func Setup(ctx context.Context) error {
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
	))
	otel.SetTextMapPropagator(propagation.TraceContext{})

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

// ExtractTraceContext extracts W3C trace context from incoming gRPC metadata
// on ctx, falling back to the TRACEPARENT environment variable.
func ExtractTraceContext(ctx context.Context) context.Context {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if tp := md.Get("traceparent"); len(tp) > 0 {
			// MapCarrier, not HeaderCarrier: gRPC metadata keys are lowercase,
			// but HeaderCarrier wraps http.Header whose Get() title-cases keys,
			// silently missing "traceparent" → "Traceparent".
			carrier := make(propagation.MapCarrier)
			for k, v := range md {
				if len(v) > 0 {
					carrier[k] = v[0]
				}
			}
			return propagation.TraceContext{}.Extract(ctx, carrier)
		}
	}
	if tp := os.Getenv("TRACEPARENT"); tp != "" {
		return propagation.TraceContext{}.Extract(ctx, propagation.MapCarrier{
			"traceparent": tp,
		})
	}
	return ctx
}
