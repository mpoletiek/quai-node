//go:build !windows

package supervise

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
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

// running reports whether pid is alive and runs program, so a pid reused by
// another process (or a zombie, whose command ps shows in parentheses)
// doesn't count. Without ps, a live pid counts.
func running(pid int, program string) bool {
	if err := syscall.Kill(pid, 0); err != nil && !errors.Is(err, syscall.EPERM) {
		return false
	}
	out, err := exec.Command("ps", "-o", "args=", "-p", strconv.Itoa(pid)).Output()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return false // no such process
	}
	if err != nil {
		return true
	}
	return strings.HasPrefix(strings.TrimSpace(string(out)), program)
}

func kill(pid int) { _ = syscall.Kill(pid, syscall.SIGKILL) }
