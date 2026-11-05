package update

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// pollInterval ist eine Variable, damit Tests schnell laufen.
var pollInterval = 500 * time.Millisecond

const maxMarkerBytes = 64

// Guard wacht nach einer Aktivierung. Gibt es kein pending, tut er nichts. Sonst
// wartet er bis zu timeout darauf, dass healthyMarker genau die Version aus pending
// enthält: dann wird pending entfernt. Läuft die Frist ab, ruft er rollback auf.
// Bei Abbruch von ctx geschieht kein Rückfall; pending bleibt für den nächsten Start.
func Guard(ctx context.Context, root, healthyMarker string, timeout time.Duration, rollback func(ctx context.Context) error) error {
	raw, err := readSmall(filepath.Join(appPath(root), pendingFile), maxVersionBytes)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	want := ""
	if err == nil {
		if v := string(bytes.TrimSpace(raw)); ValidVersion(v) {
			want = v
		}
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(pollInterval)
	defer tick.Stop()
	for {
		if want != "" && markerIs(healthyMarker, want) {
			return removePending(root)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			// Eine Gesundmeldung, die kurz vor Fristende kam, zählt noch.
			if want != "" && markerIs(healthyMarker, want) {
				return removePending(root)
			}
			return rollback(ctx)
		case <-tick.C:
		}
	}
}

func markerIs(path, version string) bool {
	b, err := readSmall(path, maxMarkerBytes)
	return err == nil && string(bytes.TrimSpace(b)) == version
}
