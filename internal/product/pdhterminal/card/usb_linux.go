//go:build linux

package card

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"unsafe"

	"hmikiosk/internal/platform"
)

const (
	eviocgname256 = 0x81004506 // EVIOCGNAME(256)
	eviocgid      = 0x80084502 // EVIOCGID: struct input_id
	eviocgrab     = 0x40044590 // EVIOCGRAB
)

func ioctlVal(f *os.File, req, val uintptr) error {
	sc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var errno syscall.Errno
	if err := sc.Control(func(fd uintptr) {
		_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, fd, req, val)
	}); err != nil {
		return err
	}
	if errno != 0 {
		return errno
	}
	return nil
}

func ioctlPtr(f *os.File, req uintptr, p unsafe.Pointer) error {
	sc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var errno syscall.Errno
	if err := sc.Control(func(fd uintptr) {
		_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(p))
	}); err != nil {
		return err
	}
	if errno != 0 {
		return errno
	}
	return nil
}

// ListInputDevices listet die Eingabegeräte dir/event* mit Name und Kennung, sortiert nach Pfad.
func ListInputDevices(dir string) []InputDevice {
	paths, _ := filepath.Glob(filepath.Join(dir, "event*"))
	sort.Strings(paths)
	var out []InputDevice
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		var name [256]byte
		var id [4]uint16 // bustype, vendor, product, version
		errName := ioctlPtr(f, eviocgname256, unsafe.Pointer(&name[0]))
		errID := ioctlPtr(f, eviocgid, unsafe.Pointer(&id[0]))
		f.Close()
		if errName != nil && errID != nil {
			continue
		}
		n := 0
		for n < len(name) && name[n] != 0 {
			n++
		}
		out = append(out, InputDevice{Path: p, Name: string(name[:n]), ID: fmt.Sprintf("%04x:%04x", id[1], id[2])})
	}
	return out
}

// readExclusive übernimmt path mit EVIOCGRAB und liest erst danach Ereignisse. Scheitert
// das Greifen, wird nichts gelesen: sonst landete die UID zusätzlich im Browser.
func readExclusive(ctx context.Context, path string, fn func(platform.InputEvent)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := ioctlVal(f, eviocgrab, 1); err != nil {
		return fmt.Errorf("Leser kann nicht exklusiv übernommen werden: %w", err)
	}
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			f.Close() // weckt das blockierende Lesen
		case <-stop:
		}
	}()
	platform.ReadInputEvents(f, fn)
	if ctx.Err() != nil {
		return nil
	}
	return errors.New("Leser getrennt")
}
