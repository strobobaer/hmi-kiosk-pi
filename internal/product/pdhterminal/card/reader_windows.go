//go:build windows

package card

import "errors"

var errNotLinux = errors.New("PN532 nur am Raspberry Pi")

func openI2C(bus string, addr int) (i2cConn, error) { return nil, errNotLinux }

func openSPI(dev string) (spiBus, error) { return nil, errNotLinux }

func openUART(dev string) (serialPort, error) { return nil, errNotLinux }
