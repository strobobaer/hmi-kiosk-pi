//go:build !windows

package pdhterminal

import "os/exec"

func hideWindow(*exec.Cmd) {}
