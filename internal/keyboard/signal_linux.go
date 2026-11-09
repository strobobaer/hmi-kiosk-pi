//go:build linux

package keyboard

import (
	"os"
	"syscall"
)

// Signale laut wvkbd(1): SIGUSR2 zeigt, SIGUSR1 verbirgt.
var (
	sigShow os.Signal = syscall.SIGUSR2
	sigHide os.Signal = syscall.SIGUSR1
)
