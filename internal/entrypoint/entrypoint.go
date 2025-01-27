package entrypoint

var ImageRef = "ghcr.io/chainguard-dev/terraform-provider-imagetest:latest"

const (
	BinaryPath            = "/ko-app/entrypoint"
	WrapperPath           = "/var/run/ko/entrypoint-wrapper.sh"
	DefaultProcessLogPath = "/tmp/imagetest.log"

	// Return code if entrypoint fails.
	InternalErrorCode = 1000

	// Healthcheck return code if wrapped command fails and we're paused.
	ProcessPausedErrorCode = 927
)

var DefaultEntrypoint = []string{
	BinaryPath,
	"--process-log-path", DefaultProcessLogPath,
	WrapperPath,
}

var DefaultHealthCheckCommand = []string{
	BinaryPath,
	"healthcheck",
}
