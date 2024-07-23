package harness

import "context"

type Harness interface {
	Create(context.Context) error
	Destroy(context.Context) error
	Run(context.Context, Command) error
}

type Command struct {
	Args       string
	WorkingDir string
}

func DefaultEntrypoint() []string {
	return []string{"/bin/sh", "-c"}
}

func DefaultCmd() []string {
	return []string{"tail -f /dev/null"}
}
