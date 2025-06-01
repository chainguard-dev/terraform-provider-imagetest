package main

import (
	"flag"
	"fmt"
	"log"
)

type Args struct {
	PackagePath string
	PackageName string
	FileName    string
}

func ProcessArgs() Args {
	// Parse
	args := parseArgs()

	// Validate
	if err := validateArgs(args); err != nil {
		log.Fatalf("Failed to validate inputs: %s.", err)
	}

	return args
}

func parseArgs() (args Args) {
	// Package path
	//
	// --package-path, -p
	const packagePathDesc = "The path to the Go package for which we're " +
		"emitting the generated file."
	flag.StringVar(
		&args.PackagePath,
		"package-path",
		packagePathDesc,
		"--package-path internal/mypkg",
	)
	flag.StringVar(
		&args.PackagePath,
		"p",
		packagePathDesc,
		"-p internal/mypkg",
	)

	// Package file name
	//
	// --file-name, -fn
	const fileNameDesc = "The name of the file to emit."
	flag.StringVar(
		&args.FileName,
		"file-name",
		"",
		"--file-name my_file.go",
	)
	flag.StringVar(
		&args.FileName,
		"fn",
		"",
		"-fn my_file.go",
	)

	// Package name
	//
	// --package-name, -n
	const packageNameDesc = "The name of the Go package for the generated file."
	flag.StringVar(
		&args.PackageName,
		"package-name",
		packageNameDesc,
		"--package-name mypkg")
	flag.StringVar(
		&args.PackageName,
		"n",
		packageNameDesc,
		"-n mypkg")

	flag.Parse()

	return
}

func validateArgs(args Args) error {
	// --package-path is required
	if args.PackagePath == "" {
		return fmt.Errorf("flag '--package-path' is required and was not provided")
	}

	// --package-name is required
	if args.PackageName == "" {
		return fmt.Errorf("flag '--package-name' is required and was not provided")
	}

	// --file-name is required
	if args.FileName == "" {
		return fmt.Errorf("Flag '--file-name' is required and was not provided")
	}

	return nil
}
