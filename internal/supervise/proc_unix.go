//go:build !windows

package supervise

import (
	"os"
	"os/exec"
	"syscall"
)

// prepare puts the child in its own process group, so a Ctrl-C in the
// terminal reaches quai-node alone and the child is interrupted once, by stop.
func prepare(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func interrupt(cmd *exec.Cmd) error { return cmd.Process.Signal(os.Interrupt) }

// Prepare readies this process to supervise children; nothing to do here.
func Prepare() {}
