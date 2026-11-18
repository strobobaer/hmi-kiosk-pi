//go:build linux

package card

import (
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
	"unsafe"
)

const (
	tcFlush   = 0x540B     // TCFLSH
	tciOFlush = 2          // TCIOFLUSH
	cBaud     = 0x100F     // CBAUD | CBAUDEX
	crtscts   = 0x80000000 // CRTSCTS
)

// uartPort liest blockierend mit Zeitgrenze (VMIN=0, VTIME=1); ein leerer Read ist der Zeitablauf.
type uartPort struct{ f *os.File }

func (u *uartPort) Read(b []byte) (int, error) {
	n, err := u.f.Read(b)
	if n == 0 && errors.Is(err, io.EOF) {
		return 0, nil
	}
	return n, err
}
func (u *uartPort) Write(b []byte) (int, error) { return u.f.Write(b) }
func (u *uartPort) Close() error                { return u.f.Close() }

// openUART öffnet dev roh mit 115200 8N1, VMIN=0, VTIME=1 (100 ms).
func openUART(dev string) (serialPort, error) {
	// Blockierend öffnen und erst dann in os.File packen (kein Poller), sonst greift VTIME nicht.
	fd, err := syscall.Open(dev, syscall.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), dev)
	var tio syscall.Termios
	if err := ioctlPtr(f, syscall.TCGETS, unsafe.Pointer(&tio)); err != nil {
		f.Close()
		return nil, fmt.Errorf("UART %s: %w", dev, err)
	}
	// cfmakeraw von Hand
	tio.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP | syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON | syscall.IXOFF | syscall.IXANY
	tio.Oflag &^= syscall.OPOST
	tio.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	tio.Cflag &^= syscall.CSIZE | syscall.PARENB | syscall.CSTOPB | cBaud | crtscts
	tio.Cflag |= syscall.CS8 | syscall.CLOCAL | syscall.CREAD | syscall.B115200
	tio.Ispeed, tio.Ospeed = syscall.B115200, syscall.B115200
	tio.Cc[syscall.VMIN] = 0
	tio.Cc[syscall.VTIME] = 1
	if err := ioctlPtr(f, syscall.TCSETS, unsafe.Pointer(&tio)); err != nil {
		f.Close()
		return nil, fmt.Errorf("UART %s: %w", dev, err)
	}
	if err := ioctlVal(f, tcFlush, tciOFlush); err != nil {
		f.Close()
		return nil, fmt.Errorf("UART %s: %w", dev, err)
	}
	return &uartPort{f: f}, nil
}
