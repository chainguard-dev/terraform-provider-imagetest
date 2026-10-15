package sshhost

import _ "embed"

// cmdStdOpts contains standard shell configuration for setup command sessions.
//
//go:embed provision/ubuntu/stdopts.sh
var cmdStdOpts string
