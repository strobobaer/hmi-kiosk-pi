//go:build !linux && !windows

package card

import (
	"context"
	"errors"
	"time"

	"hmikiosk/internal/platform"
)

var errNotLinux = errors.New("Kartenleser nur unter Linux")

// ListInputDevices: Ohne Linux gibt es keine Eingabegeräte.
func ListInputDevices(dir string) []InputDevice { return nil }

func readExclusive(ctx context.Context, paths []string, fn func(platform.InputEvent)) error {
	return errNotLinux
}

func openI2C(bus string, addr int) (i2cConn, error) { return nil, errNotLinux }

func openSPI(dev string) (spiBus, error) { return nil, errNotLinux }

func openUART(dev string) (serialPort, error) { return nil, errNotLinux }

func (u *usbReader) readKeys(ctx context.Context, fn func(code uint16, at time.Time)) error {
	return errNotLinux
}

// usbMayBeAbsent: Unter Windows startet der Leser auch ohne angestecktes Gerät und findet es später
// von selbst (Auswahl per Kennung oder Name, nicht per Handle); unter Linux scheitert das Öffnen.
const usbMayBeAbsent = false

func learnPlatform(ctx context.Context, d time.Duration) (InputDevice, string, error) {
	return InputDevice{}, "", errNotSupported
}
