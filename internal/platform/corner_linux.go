//go:build linux

package platform

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const cornerTick = 250 * time.Millisecond

// eviocgabs ist _IOR('E', 0x40+abs, struct input_absinfo) mit 6 × int32.
func eviocgabs(abs uintptr) uintptr { return 0x80184540 + abs }

// axisRange fragt den Wertebereich einer ABS-Achse ab; Fehler, wenn das Gerät sie nicht hat.
func axisRange(f *os.File, abs uintptr) (AxisRange, error) {
	sc, err := f.SyscallConn()
	if err != nil {
		return AxisRange{}, err
	}
	var info [6]int32 // value, minimum, maximum, fuzz, flat, resolution
	var errno syscall.Errno
	if err := sc.Control(func(fd uintptr) {
		_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, fd, eviocgabs(abs), uintptr(unsafe.Pointer(&info[0])))
	}); err != nil {
		return AxisRange{}, err
	}
	if errno != 0 {
		return AxisRange{}, errno
	}
	if info[2] <= info[1] {
		return AxisRange{}, syscall.EINVAL
	}
	return AxisRange{Min: info[1], Max: info[2]}, nil
}

// deviceAxes liefert die Bereiche; Mehrfingerachsen haben Vorrang vor ABS_X/ABS_Y.
// Geräte ohne beide Achsen (Tastaturen) liefern ok=false.
func deviceAxes(f *os.File) (x, y AxisRange, ok bool) {
	for _, p := range [][2]uintptr{{absMTPositionX, absMTPositionY}, {absX, absY}} {
		xr, errX := axisRange(f, p[0])
		yr, errY := axisRange(f, p[1])
		if errX == nil && errY == nil {
			return xr, yr, true
		}
	}
	return AxisRange{}, AxisRange{}, false
}

// WatchCorner liest alle devDir/event* mit Positionsachsen nur lesend (ohne EVIOCGRAB)
// und ruft hit, wenn der erste Finger cfg.Hold lang im Feld oben rechts bleibt.
// Neue Geräte werden alle 5 s gesucht. hit kann nebenläufig aus mehreren
// Geräte-Goroutinen und kurz nach der Rückkehr noch aufgerufen werden.
func WatchCorner(ctx context.Context, devDir string, cfg CornerCfg, hit func()) error {
	var mu sync.Mutex
	open := map[string]*os.File{}
	defer func() {
		mu.Lock()
		for _, f := range open {
			f.Close()
		}
		mu.Unlock()
	}()

	skip := map[string]bool{} // Geräte ohne Positionsachsen nicht ständig neu öffnen
	for {
		paths, _ := filepath.Glob(filepath.Join(devDir, "event*"))
		for _, p := range paths {
			mu.Lock()
			_, busy := open[p]
			mu.Unlock()
			if busy || skip[p] {
				continue
			}
			f, err := os.Open(p)
			if err != nil {
				continue
			}
			x, y, ok := deviceAxes(f)
			if !ok {
				f.Close()
				skip[p] = true
				continue
			}
			mu.Lock()
			open[p] = f
			mu.Unlock()
			go func(p string, f *os.File) {
				watchCornerDevice(ctx, f, newCornerDetector(cfg, x, y), hit)
				f.Close()
				mu.Lock()
				delete(open, p)
				mu.Unlock()
			}(p, f)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(touchRescanInterval):
		}
	}
}

func watchCornerDevice(ctx context.Context, r *os.File, det *cornerDetector, hit func()) {
	var mu sync.Mutex
	feed := func(ev rawEvent) {
		mu.Lock()
		fired := det.feed(ev, time.Now())
		mu.Unlock()
		if fired {
			hit()
		}
	}
	done := make(chan struct{})
	defer close(done)
	// Ein ruhender Finger erzeugt keine Ereignisse: Takt als künstliches EV_SYN.
	go func() {
		t := time.NewTicker(cornerTick)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				feed(rawEvent{Type: evSyn})
			}
		}
	}()
	readInputEvents(r, feed)
}
