//go:build linux

package platform

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

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
	readInputEvents(r, func(ev rawEvent) {
		if ev.Type == evKey || ev.Type == evAbs {
			activity()
		}
	})
}
