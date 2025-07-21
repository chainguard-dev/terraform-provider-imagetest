package mock

import "golang.org/x/crypto/ssh"

// marshalExitStatus marshals the standard 'exit-status' message body to
// indicate to the caller the exit code of the executed process.
func marshalExitStatus(exitCode uint32) []byte {
	return ssh.Marshal(struct {
		Status uint32
	}{exitCode})
}
