package main

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/chainguard-dev/clog"
)

const (
	fileModeRWX        = 0o700
	fileModeRW         = 0o600
	fileFlagsOverwrite = os.O_TRUNC | os.O_CREATE | os.O_WRONLY
)

func main() {
	// Log with file name + line number
	log.SetFlags(log.Lshortfile)
	slog.SetLogLoggerLevel(slog.LevelDebug)
	log := clog.NewLogger(slog.Default())

	// Init the application context
	//
	// This is really only used for HTTP requests
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Parse command-line inputs
	args := parseArgs()
	log.Info("selected AWS region", "region", args.Region)
	log.Info(
		"output constraints identified",
		"package_name", args.PackageName,
		"package_path", args.PackagePath,
		"file_name", args.FileName,
	)

	// If the output directory doesn't exist, try to make it
	err := os.MkdirAll(args.PackagePath, fileModeRWX)
	if err != nil && !errors.Is(err, os.ErrExist) {
		log.Fatal(
			"failed to craete output package directory",
			"path", args.PackagePath,
			"error", err,
		)
	}

	// Fetch the price list
	pl, err := priceListFetch(ctx, ProductCodeEC2, RegionUSW2)

	// Filter the instance types (products)
	pl = priceListProductFilter(
		pl,
		productFilterIsSharedInstance,
		productFilterIsBoxUsageType,
		productFilterIsLinux,
		productFilterHasNoPreinstalledSoftware,
	)

	// Convert it to our price list
	converted, err := priceListConvert(pl)

	// Generate the final pricelist
	if err = generatePriceList(
		args.PackageName,
		args.PackagePath,
		args.FileName,
		converted,
	); err != nil {
		log.Fatalf("Failed to generate price list: %s.", err)
	}
}

// Drop non-shared tenancy instances (ex: dedicated hosts)
func productFilterIsSharedInstance(p Product) bool {
	const tenancyShared = "Shared"
	return p.Attributes.Tenancy == tenancyShared
}

// Drop reserved instance types
func productFilterIsBoxUsageType(p Product) bool {
	const boxUsage = "-BoxUsage:"
	return strings.Contains(p.Attributes.UsageType, boxUsage)
}

// Drop non-Linux instance types
func productFilterIsLinux(p Product) bool {
	const osLinux = "Linux"
	return p.Attributes.OS == osLinux
}

// Drop instance types with pre-installed software (ex: Kafka). These are
// typically "as-a-service" offerings.
func productFilterHasNoPreinstalledSoftware(p Product) bool {
	const noPreinstalledSoftware = "NA"
	return p.Attributes.PreinstalledSW == noPreinstalledSoftware
}
