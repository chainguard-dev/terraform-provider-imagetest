package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os/signal"
	"syscall"
	"time"

	"github.com/chainguard-dev/clog"
	log2 "github.com/chainguard-dev/terraform-provider-imagetest/internal/log"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/o11y"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Run "go generate" to format example terraform files and generate the docs for the registry/website

// If you do not have terraform installed, you can remove the formatting command, but its suggested to
// ensure the documentation is formatted properly.
//go:generate terraform fmt -recursive ./examples/

// Run the docs generation tool, check its repository for more information on how it works and how docs
// can be customized.
//go:generate go run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs

// these will be set by the goreleaser configuration
// to appropriate values for the compiled binary.
var version string = "dev"

// goreleaser can pass other information to the main package, such as the specific commit
// https://goreleaser.com/cookbooks/using-main.version/

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "set to true to run the provider with support for debuggers like delve")
	flag.Parse()

	opts := providerserver.ServeOpts{
		Address: "registry.terraform.io/chainguard-dev/imagetest",
		Debug:   debug,
	}

	ctx := context.Background()
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	ctx = setupLog(ctx)

	err := providerserver.Serve(ctx, provider.New(version), opts)

	if shutdownErr := shutdownOTel(); shutdownErr != nil {
		log.Printf("otel shutdown: %v", shutdownErr)
	}

	if err != nil {
		log.Fatal(err.Error())
	}
}

func shutdownOTel() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var errs []error
	if tp, ok := otel.GetTracerProvider().(*sdktrace.TracerProvider); ok {
		if err := tp.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("tracer provider: %w", err))
		}
	}
	if lp := o11y.LoggerProvider(); lp != nil {
		if err := lp.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("logger provider: %w", err))
		}
	}
	return errors.Join(errs...)
}

// setupLog sets up the default logging configuration.
func setupLog(ctx context.Context) context.Context {
	slog.SetDefault(slog.New(&log2.TFHandler{}))
	log := clog.New(slog.Default().Handler())
	return clog.WithLogger(ctx, log)
}
