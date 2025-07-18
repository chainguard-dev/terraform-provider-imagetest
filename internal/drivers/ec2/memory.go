package ec2

import (
	"context"
	"strconv"
	"strings"

	"github.com/chainguard-dev/clog"
)

// Memory describes the user-configurable physical memory constraints for a
// desired instance.
type Memory struct {
	// The desired instance physical memory capacity
	//
	// This is 'fmt.Scan'ed and the unit of memory can case-insensitively be
	// specified like '1000MB' or '1GB'. All units are evaluated as 2^(n) bytes,
	// not bits as lowercase might typically indicate.
	Capacity string
}

// We allow string input of memory capacity in any of the following formats:
//
// - 2      (defaults to gigabytes)
// - 2000MB (parsed as megabytes)
// - 2GB    (parsed as gigabytes)
//
// The `uint32` returned is the input converted to `mib` which is what AWS
// expects when sizing physical memory.
func parseMemoryCapacity(ctx context.Context, input string) uint32 {
	log := clog.FromContext(ctx).With("input", input)

	// Lower-case the string for standard comparison
	input = strings.ToLower(input)
	// Trim whitespace to sanitize user input
	input = strings.TrimSpace(input)

	// Look for a capacity unit suffix
	const (
		unitGB  = "gb"
		unitMB  = "mb"
		unitKB  = "kb"
		unitGIB = "gib"
		unitMIB = "mib"
		unitKIB = "kib"
	)
	log.Debug("evaluating memory input suffixes")
	var unit string
	var coefficient float32
	switch {
	default:
		// No unit present, default to GB
		unit = unitGB
		coefficient = 953.67431640625

	case strings.HasSuffix(input, unitGB):
		unit = unitGB
		coefficient = 953.67431640625

	case strings.HasSuffix(input, "mb"):
		unit = unitMB
		coefficient = 0.95367431640625

	case strings.HasSuffix(input, "kb"):
		unit = unitKB
		coefficient = 0.00095367431640625

	case strings.HasSuffix(input, "gib"):
		unit = unitGIB
		coefficient = 1024

	case strings.HasSuffix(input, "mib"):
		unit = unitMIB
		coefficient = 1

	case strings.HasSuffix(input, "kib"):
		unit = unitKIB
		coefficient = 0.00097656
	}
	log = log.With("unit", unit, "coefficient", coefficient)
	log.Debug("suffix and unit successfully identified")

	// Parse the number to a float64
	i := strings.Index(input, unit)
	var n float64
	var err error
	if i > -1 {
		// Input has a unit at the end, parse everything before the unit
		input = input[:i]
		// Trim whitespace.. again.. to sanitize user input
		input = strings.TrimSpace(input)
		// Parse
		n, err = strconv.ParseFloat(input, 32)
	} else {
		// Input does NOT have a unit at the end, parse the entire input
		n, err = strconv.ParseFloat(input, 32)
	}
	if err != nil {
		log.Error("failed to parse input as a float64", "error", err)
		return 0
	}
	log.Debug("parsed float successfully", "n", n)

	// Apply the coefficient to arrive at MiB
	result := float32(n) * coefficient

	// Truncate to uint32 and return
	return max(1, uint32(result))
}
