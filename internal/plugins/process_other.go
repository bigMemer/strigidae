//go:build !unix

package plugins

import "os/exec"

// detach has nothing to do where there are no sessions to start.
func detach(*exec.Cmd) {}
