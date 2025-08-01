package ec2

import (
	"os"
	"strings"
)

// debugSet indicates whether a specific debug environment variable is enabled.
//
// If this environment variable is set, the following measures are taken
// elsewhere throughout the driver's lifecycle:
// - For all SSH sessions 'os.Stdout' and 'os.Stderr' will be connected to the
// session's stdout + stderr streams, rather than the default 'bytes.Buffer'.
func debugSet() bool {
	const debugVar = "IMAGETEST_EC2_DEBUG"
	v, ok := os.LookupEnv(debugVar)
	return ok && (v == "1" || strings.EqualFold(v, "true"))
}
