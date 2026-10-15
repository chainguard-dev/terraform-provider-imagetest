package o11y

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
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
// It is set once during process startup by Setup.
func LoggerProvider() *sdklog.LoggerProvider { return loggerProvider }

var loggerProvider *sdklog.LoggerProvider

// Setup configures the global OTel TracerProvider and LoggerProvider. This is
// a no-op when no OTLP endpoint is configured. Call it once before serving RPCs.
func Setup(ctx context.Context, version string) error {
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" &&
		os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") == "" {
		return nil
	}

	res, err := resource.New(ctx,
		resource.WithTelemetrySDK(),
		resource.WithAttributes(semconv.ServiceName("imagetest"), semconv.ServiceVersion(version)),
		resource.WithFromEnv(),
	)
	if err != nil {
		return err
	}

	traceExp, err := otlptracehttp.New(ctx)
	if err != nil {
		return err
	}
	logExp, err := otlploghttp.New(ctx)
	if err != nil {
		return errors.Join(err, traceExp.Shutdown(context.WithoutCancel(ctx)))
	}
	otel.SetTracerProvider(sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExp),
		sdktrace.WithResource(res),
	))
	otel.SetTextMapPropagator(propagation.TraceContext{})

	loggerProvider = sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(logExp)),
		sdklog.WithResource(res),
	)

	return nil
}

// Flush exports completed telemetry without stopping the providers.
func Flush(ctx context.Context) error {
	var traces, logs func(context.Context) error
	if tp, ok := otel.GetTracerProvider().(*sdktrace.TracerProvider); ok {
		traces = tp.ForceFlush
	}
	if loggerProvider != nil {
		logs = loggerProvider.ForceFlush
	}
	return exportBoth(ctx, traces, logs)
}

// Shutdown drains and stops both providers.
func Shutdown(ctx context.Context) error {
	var traces, logs func(context.Context) error
	if tp, ok := otel.GetTracerProvider().(*sdktrace.TracerProvider); ok {
		traces = tp.Shutdown
	}
	if loggerProvider != nil {
		logs = loggerProvider.Shutdown
	}
	return exportBoth(ctx, traces, logs)
}

func exportBoth(ctx context.Context, traces, logs func(context.Context) error) error {
	// An unavailable endpoint must not consume the other signal's flush budget.
	var wg sync.WaitGroup
	var traceErr, logErr error
	if traces != nil {
		wg.Go(func() {
			if err := traces(ctx); err != nil {
				traceErr = fmt.Errorf("traces: %w", err)
			}
		})
	}
	if logs != nil {
		wg.Go(func() {
			if err := logs(ctx); err != nil {
				logErr = fmt.Errorf("logs: %w", err)
			}
		})
	}
	wg.Wait()
	return errors.Join(traceErr, logErr)
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
