//go:build !linux

package card

import (
	"context"
	"errors"

	"hmikiosk/internal/platform"
)

var errNotLinux = errors.New("Kartenleser nur unter Linux")

// ListInputDevices: Ohne Linux gibt es keine Eingabegeräte.
func ListInputDevices(dir string) []InputDevice { return nil }

func readExclusive(ctx context.Context, paths []string, fn func(platform.InputEvent)) error {
	return errNotLinux
}

func openI2C(bus string, addr int) (i2cConn, error) { return nil, errNotLinux }
