package main

import (
	"context"
	"flag"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/chainguard-dev/clog"
	log2 "github.com/chainguard-dev/terraform-provider-imagetest/internal/log"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/o11y"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
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

// Lifecycle errors can occur without a Terraform RPC logging context.
var stderrLog = log.New(os.Stderr, "", log.LstdFlags)

// goreleaser can pass other information to the main package, such as the specific commit
// https://goreleaser.com/cookbooks/using-main.version/

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "set to true to run the provider with support for debuggers like delve")
	flag.Parse()

	ctx := context.Background()
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	ctx = setupLog(ctx)

	if err := o11y.Setup(ctx, version); err != nil {
		stderrLog.Printf("otel setup, continuing without telemetry: %v", err)
	}
	err := providerserver.Serve(ctx, provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/chainguard-dev/imagetest", Debug: debug,
	})

	// go-plugin kills the process after two seconds. Test and harness runs flush before
	// replying; this shorter final drain leaves time for the process to exit.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := o11y.Shutdown(shutdownCtx); err != nil {
		stderrLog.Printf("otel shutdown: %v", err)
	}
	if err != nil {
		stderrLog.Fatal(err)
	}
}

// setupLog sets up the default logging configuration.
func setupLog(ctx context.Context) context.Context {
	slog.SetDefault(slog.New(&log2.TFHandler{}))
	log := clog.New(slog.Default().Handler())
	return clog.WithLogger(ctx, log)
}
