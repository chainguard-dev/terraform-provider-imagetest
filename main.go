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
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6/tf6server"
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
		stderrLog.Fatal(err)
	}
	if err := serve(debug); err != nil {
		stderrLog.Fatal(err.Error())
	}
}

func serve(debug bool) error {
	var opts []tf6server.ServeOpt
	if debug {
		opts = append(opts, tf6server.WithManagedDebug())
	}
	err := tf6server.Serve("registry.terraform.io/chainguard-dev/imagetest", func() tfprotov6.ProviderServer {
		return &telemetryServer{ProviderServer: providerserver.NewProtocol6(provider.New(version)())()}
	}, opts...)

	// go-plugin kills the process after two seconds. Resource changes flush before
	// replying; this shorter final drain leaves time for the process to exit.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := o11y.Shutdown(ctx); err != nil {
		stderrLog.Printf("otel shutdown: %v", err)
	}
	return err
}

type telemetryServer struct{ tfprotov6.ProviderServer }

func (s *telemetryServer) ApplyResourceChange(ctx context.Context, req *tfprotov6.ApplyResourceChangeRequest) (*tfprotov6.ApplyResourceChangeResponse, error) {
	defer func() {
		// Terraform may close the plugin immediately after this response, including
		// after a canceled apply. Drain completed spans/logs while it is still waiting.
		flushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if err := o11y.Flush(flushCtx); err != nil {
			stderrLog.Printf("otel flush: %v", err)
		}
	}()
	return s.ProviderServer.ApplyResourceChange(ctx, req)
}

// setupLog sets up the default logging configuration.
func setupLog(ctx context.Context) context.Context {
	slog.SetDefault(slog.New(&log2.TFHandler{}))
	log := clog.New(slog.Default().Handler())
	return clog.WithLogger(ctx, log)
}
