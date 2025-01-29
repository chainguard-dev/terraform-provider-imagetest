package entrypoint

// ImageRef is replaced at provider build time (ldflag) with the :tag@digest of
// the ./cmd/entrypoint binary.
var ImageRef = "gcr.io/wolf-chainguard/entrypoint@sha256:d0d087f258b646f8d52edd6aecd9c72a99f38ab75ff1994799a427a30206f89e"

const (
	BinaryPath  = "/ko-app/entrypoint"
	WrapperPath = "/var/run/ko/entrypoint-wrapper.sh"

	// DefaultProcessLogPath contains both stdout and stderr. It is only used
	// when specified at runtime.
	DefaultProcessLogPath = "/tmp/imagetest.log"
	// DefaultStderrLogPath contains only stderr. It is always used to write
	// stderr.
	DefaultStderrLogPath     = "/tmp/imagetest.stderr.log"
	DefaultHealthCheckSocket = "/tmp/imagetest.health.sock"

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
