//go:build unix

package plugins

import (
	"os/exec"
	"syscall"
)

// detach starts the plugin in a session of its own, so it has no controlling
// terminal: opening /dev/tty fails instead of reading the user's keys or
// drawing over the screen. Only the plugin process is killed on a cancel or a
// timeout, not a process group.
func detach(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
