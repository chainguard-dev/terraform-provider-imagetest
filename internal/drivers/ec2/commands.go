package ec2

import _ "embed"

// cmdStdOpts contains standard shell configuration for all SSH sessions.
//
//go:embed provision/ubuntu/stdopts.sh
var cmdStdOpts string
