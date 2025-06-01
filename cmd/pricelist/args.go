package main

import "flag"

type Args struct {
	PackagePath string
	PackageName string
	FileName    string
	Region      string
}

func parseArgs() Args {
	var args Args

	// --package-path, -pp
	flag.StringVar(&args.PackagePath, "package-path", "", "")
	flag.StringVar(&args.PackagePath, "pp", "", "")

	// --package-name, -pn
	flag.StringVar(&args.PackageName, "package-name", "", "")
	flag.StringVar(&args.PackageName, "pn", "", "")

	// --file-name, -fn
	flag.StringVar(&args.FileName, "file-name", "", "")
	flag.StringVar(&args.FileName, "fn", "", "")

	// --region, -r
	flag.StringVar(&args.Region, "region", "us-west-2", "")
	flag.StringVar(&args.Region, "r", "us-west-2", "")

	flag.Parse()

	return args
}
