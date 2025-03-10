package entrypoint

// ImageRef is replaced at provider build time (ldflag) with the :tag@digest of
// the ./cmd/entrypoint binary.
var ImageRef = "ghcr.io/chainguard-dev/terraform-provider-imagetest/entrypoint:latest"

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

	DriverLocalRegistryEnvVar         = "IMAGETEST_LOCAL_REGISTRY"
	DriverLocalRegistryHostnameEnvVar = "IMAGETEST_LOCAL_REGISTRY_HOSTNAME"
	DriverLocalRegistryPortEnvVar     = "IMAGETEST_LOCAL_REGISTRY_PORT"

	DefaultWorkDir = "/imagetest/work"
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
