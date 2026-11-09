//go:build !linux

package keyboard

import "os"

// Ohne Linux gibt es kein wvkbd: Jeder Aufruf meldet ErrUnavailable.
var (
	sigShow os.Signal
	sigHide os.Signal
)
