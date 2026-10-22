package ssh

import (
	"fmt"
	"time"
)

// SSHConnectionFailedPrefix starts the message of every SSHConnectionError.
// It is stable so a caller that only sees the driver's output, such as a
// release pipeline reading a guest's log, can tell a connection that
// failed from a test that failed.
const SSHConnectionFailedPrefix = "imagetest: ssh connection failed: "

// SSHConnectionError is returned by Setup when the first SSH connection to
// the host fails. It covers a down host, a wrong address or port, a
// refused key, and a host key that does not match the pin. In every case
// nothing ran on the host, so this is never a test failure. The wrapped
// error says which of them it was.
type SSHConnectionError struct {
	Host string
	Port int32
	Err  error
}

func (e *SSHConnectionError) Error() string {
	return fmt.Sprintf("%s%s port %d: %v", SSHConnectionFailedPrefix, e.Host, e.Port, e.Err)
}

func (e *SSHConnectionError) Unwrap() error { return e.Err }

// LockTimeoutPrefix starts the message of every LockTimeoutError, stable
// for the same reason as SSHConnectionFailedPrefix.
const LockTimeoutPrefix = "imagetest: ssh lock wait timed out: "

// LockTimeoutError is returned by Admit when the host lock was not free
// within Config.LockTimeout. Another run held the host the whole time, so
// nothing of this run reached the host and this is never a test failure.
type LockTimeoutError struct {
	Path    string
	Timeout time.Duration
	Err     error
}

func (e *LockTimeoutError) Error() string {
	return fmt.Sprintf("%s%s was not free within %s", LockTimeoutPrefix, e.Path, e.Timeout)
}

func (e *LockTimeoutError) Unwrap() error { return e.Err }
