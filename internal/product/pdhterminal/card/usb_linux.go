//go:build linux

package card

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"
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

// ListInputDevices listet die Eingabegeräte dir/event* mit Name und Kennung, numerisch sortiert nach Pfad.
func ListInputDevices(dir string) []InputDevice {
	paths, _ := filepath.Glob(filepath.Join(dir, "event*"))
	sort.Slice(paths, func(i, j int) bool { return eventPathLess(paths[i], paths[j]) })
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

// readExclusive übernimmt alle paths mit EVIOCGRAB und liest erst danach Ereignisse (je Knoten
// ein Lesevorgang, fn kann gleichzeitig laufen). Scheitert das Öffnen oder Greifen eines Knotens,
// werden alle wieder freigegeben und nichts gelesen: sonst landete die UID zusätzlich im Browser.
func readExclusive(ctx context.Context, paths []string, fn func(platform.InputEvent)) error {
	var files []*os.File
	closeAll := func() {
		for _, f := range files {
			f.Close()
		}
	}
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			closeAll()
			return fmt.Errorf("Leser kann nicht exklusiv übernommen werden: %w", err)
		}
		files = append(files, f)
		if err := ioctlVal(f, eviocgrab, 1); err != nil {
			closeAll()
			return fmt.Errorf("Leser kann nicht exklusiv übernommen werden: %w", err)
		}
	}
	if len(files) == 0 {
		return errors.New("Leser kann nicht exklusiv übernommen werden: kein Gerät")
	}
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			closeAll() // weckt das blockierende Lesen
		case <-stop:
		}
	}()
	// Endet ein Knoten (getrennt), endet der ganze Leser: die übrigen werden geschlossen.
	var once sync.Once
	var wg sync.WaitGroup
	for _, f := range files {
		wg.Add(1)
		go func(f *os.File) {
			defer wg.Done()
			platform.ReadInputEvents(f, fn)
			once.Do(closeAll)
		}(f)
	}
	wg.Wait()
	closeAll()
	if ctx.Err() != nil {
		return nil
	}
	return errors.New("Leser getrennt")
}

// readKeys übernimmt alle Knoten exklusiv (sonst landet die UID als Tastatureingabe im Browser)
// und liest erst danach.
func (u *usbReader) readKeys(ctx context.Context, fn func(code uint16, at time.Time)) error {
	paths := make([]string, len(u.devs))
	for i, d := range u.devs {
		paths[i] = d.Path
	}
	return readExclusive(ctx, paths, func(ev platform.InputEvent) {
		if ev.Type == evKey && ev.Value == 1 {
			fn(ev.Code, time.Now())
		}
	})
}

// usbMayBeAbsent: Unter Windows startet der Leser auch ohne angestecktes Gerät und findet es später
// von selbst (Auswahl per Kennung oder Name, nicht per Handle); unter Linux scheitert das Öffnen.
const usbMayBeAbsent = false

func learnPlatform(ctx context.Context, d time.Duration) (InputDevice, string, error) {
	return InputDevice{}, "", errNotSupported
}
