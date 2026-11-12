//go:build linux

package card

import (
	"fmt"
	"os"
	"path/filepath"
)

const i2cSlave = 0x0703 // I2C_SLAVE

// openI2C öffnet /dev/i2c-N (bus: Nummer oder Pfad) und wählt die Slave-Adresse (Standard 0x24).
func openI2C(bus string, addr int) (i2cConn, error) {
	if bus == "" {
		bus = "1"
	}
	path := bus
	if !filepath.IsAbs(bus) {
		path = "/dev/i2c-" + bus
	}
	if addr == 0 {
		addr = 0x24
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	if err := ioctlVal(f, i2cSlave, uintptr(addr)); err != nil {
		f.Close()
		return nil, fmt.Errorf("I²C-Adresse 0x%02x: %w", addr, err)
	}
	return f, nil
}
