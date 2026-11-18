//go:build !linux

package oled

import "errors"

// openBus: Ohne Linux gibt es keinen I²C-Zugriff.
func openBus(dev string, addr int) (bus, error) {
	return nil, errors.New("Statusdisplay nur unter Linux")
}
