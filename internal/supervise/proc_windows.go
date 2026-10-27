//go:build windows

package supervise

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// prepare starts the child in a new process group, so a CTRL_BREAK can be
// sent to it alone. Go programs (go-quai) read CTRL_BREAK as os.Interrupt.
func prepare(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
}

func interrupt(cmd *exec.Cmd) error {
	return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(cmd.Process.Pid))
}

// Prepare gives a service, which has no console, one for its children to
// share: console control events only reach processes attached to the
// sender's console. In a terminal the console exists and this does nothing.
func Prepare() {
	_, _, _ = allocConsole.Call()
}

var allocConsole = windows.NewLazySystemDLL("kernel32.dll").NewProc("AllocConsole")

// running is false: the Windows service manager waits for quai-node, which
// waits for its children, before it starts the service again.
func running(pid int, program string) bool { return false }

func kill(pid int) {}
