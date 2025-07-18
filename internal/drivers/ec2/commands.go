package ec2

// Commands maps to user-configurable inputs which will be executed on the
// launched EC2 instance.
type Commands struct {
	// If specified, commands will be executed in the context of this user.
	User string

	// If 'Shell' is provided, commands will be run in the shell specified.
	//
	// NOTE: 'Shell', if provided, must be one of: 'sh', 'bash', 'zsh' or 'fish'.
	Shell Shell // TODO: Reconverge after 'ssh' PR is merged.

	// Commands which will be run in sequence. If 'Shell' is provided, these will
	// be run within a single SSH and terminal session. If 'Shell' is NOT
	// provided, these will be executed across individual SSH channels.
	Commands []string

	// Env reflects environment variables which will be exported in the SSH
	// session on the EC2 instance.
	Env map[string]string
}

// TODO: Reconverge after 'ssh' PR is merged.
type Shell = string
