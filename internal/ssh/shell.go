package ssh

// shell.go defines some string constants of the various well-known Linux
// shells. These are used by 'ExecIn' to begin a shell session, then pipe one or
// more commands to that process via stdin.

type Shell = string

const (
	ShellSh   Shell = "sh"
	ShellBash Shell = "bash"
	ShellZSH  Shell = "zsh"
	ShellFish Shell = "fish"
)
