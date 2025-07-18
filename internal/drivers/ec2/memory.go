package ec2

// Memory describes the user-configurable physical memory constraints for a
// desired instance.
type Memory struct {
	// The desired instance physical memory capacity
	//
	// This is `fmt.Scan`ed and the unit of memory can case-insensitively be
	// specified like `1000MB` or `1GB`. All units are evaluated as 2^(n) bytes,
	// not bits as lowercase might typically indicate.
	Capacity string
}
