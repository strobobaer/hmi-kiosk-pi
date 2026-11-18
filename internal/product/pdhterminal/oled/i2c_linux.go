//go:build linux

package oled

import (
	"fmt"
	"os"
	"syscall"
)

const i2cSlave = 0x0703 // I2C_SLAVE

// openBus öffnet dev (z. B. /dev/i2c-1) und wählt die Slave-Adresse.
func openBus(dev string, addr int) (bus, error) {
	f, err := os.OpenFile(dev, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	sc, err := f.SyscallConn()
	if err != nil {
		f.Close()
		return nil, err
	}
	var errno syscall.Errno
	if err := sc.Control(func(fd uintptr) {
		_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, fd, i2cSlave, uintptr(addr))
	}); err != nil {
		f.Close()
		return nil, err
	}
	if errno != 0 {
		f.Close()
		return nil, fmt.Errorf("I²C-Adresse 0x%02x: %w", addr, errno)
	}
	return f, nil
}
