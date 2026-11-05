//go:build linux

package platform

import (
	"context"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

const (
	evKey = 0x01
	evAbs = 0x03
)

// struct input_event: timeval (2 Wörter) + type u16 + code u16 + value i32.
var inputEventSize = 2*(strconv.IntSize/8) + 8

var touchRescanInterval = 5 * time.Second

// WatchTouch liest alle devDir/event* nur lesend (ohne EVIOCGRAB) und ruft
// activity bei EV_KEY und EV_ABS auf. Fehlende Geräte sind kein Fehler.
// activity kann nebenläufig aus mehreren Geräte-Goroutinen und kurz nach der
// Rückkehr noch aufgerufen werden; der Aufrufer muss darauf ausgelegt sein.
func WatchTouch(ctx context.Context, devDir string, activity func()) error {
	var mu sync.Mutex
	open := map[string]*os.File{}
	defer func() {
		mu.Lock()
		for _, f := range open {
			f.Close()
		}
		mu.Unlock()
	}()

	for {
		paths, _ := filepath.Glob(filepath.Join(devDir, "event*"))
		for _, p := range paths {
			mu.Lock()
			_, busy := open[p]
			mu.Unlock()
			if busy {
				continue
			}
			f, err := os.Open(p)
			if err != nil {
				continue
			}
			mu.Lock()
			open[p] = f
			mu.Unlock()
			go func(p string, f *os.File) {
				readEvents(f, activity)
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

func readEvents(r io.Reader, activity func()) {
	buf := make([]byte, inputEventSize)
	for {
		if _, err := io.ReadFull(r, buf); err != nil {
			return
		}
		switch binary.NativeEndian.Uint16(buf[inputEventSize-8:]) {
		case evKey, evAbs:
			activity()
		}
	}
}
