package ssh

import "fmt"

// HostUnreachablePrefix starts the message of every HostUnreachableError.
// It is stable so a caller that only sees the driver's output, such as a
// release pipeline reading a guest's log, can tell a host it could not
// reach from a test that failed.
const HostUnreachablePrefix = "imagetest: host unreachable: "

// HostUnreachableError is returned by Setup when the first SSH connection
// to the host fails: the host is down, the address or port is wrong, the
// key is refused, or the host key does not match the pin. Nothing ran on
// the host, so this is never a test failure.
type HostUnreachableError struct {
	Host string
	Port int32
	Err  error
}

func (e *HostUnreachableError) Error() string {
	return fmt.Sprintf("%s%s port %d: %v", HostUnreachablePrefix, e.Host, e.Port, e.Err)
}

func (e *HostUnreachableError) Unwrap() error { return e.Err }
