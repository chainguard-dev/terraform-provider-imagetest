// instgen utilizes the Vantage (https://vantage)
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/chainguard-dev/terraform-provider-imagetest/cmd/instgen/filter"
)

func main() {
	log.SetFlags(log.Lshortfile)

	// Init the application context
	var ctx, cancel = signal.NotifyContext(
		context.Background(),
		os.Kill, os.Interrupt,
	)
	defer cancel()

	// Parse inputs
	var args = ProcessArgs()

	// Init the AWS config
	//
	// This initialization will retrieve the configuration it can by default from
	// the environment
	//
	// If local configuration exists (ex: from `aws configure ...`) this will be
	// included but environment values will precede
	var cfg, err = config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("Failed to load default AWS config: %s.", err)
	}

	// Init the EC2 client
	var client = ec2.NewFromConfig(cfg)

	// Get a writable stream to the output file
	var path = filepath.Join(args.PackagePath, args.FileName)
	const flags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	var f *os.File
	if f, err = os.OpenFile(path, flags, 0o644); err != nil {
		log.Fatalf("Failed to open output file: %s.", err)
	}
	defer f.Close()

	// Get an iterator over all EC2 instance types
	it := InstanceTypes(ctx, client, &ec2.DescribeInstanceTypesInput{
		MaxResults: aws.Int32(100), // 100 is the maximum allowed
	})

	// Filter the EC2 instance types in-flight
	//
	// Filters are logically ANDed
	filtered := filter.AndSeq(it, instanceTypeFilters...)

	// Emit instance types
	emitInstances(f, filtered, args.PackageName)
}
