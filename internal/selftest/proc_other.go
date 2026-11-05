//go:build !linux

package selftest

import (
	"errors"

	"hmikiosk/internal/client"
)

var errNoProc = errors.New("nur unter Linux verfügbar")

func killGroup(client.Process) error { return errNoProc }

// GroupRSS ist nur unter Linux verfügbar.
func GroupRSS(string, int) (int64, error) { return 0, errNoProc }
