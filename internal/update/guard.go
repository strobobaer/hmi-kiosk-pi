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

// readPending liefert die Version aus pending; exists ist false ohne Datei.
// Eine unlesbare oder ungültige Datei ergibt exists=true und version "".
func readPending(root string) (version string, exists bool) {
	raw, err := readSmall(filepath.Join(appPath(root), pendingFile), maxVersionBytes)
	if errors.Is(err, os.ErrNotExist) {
		return "", false
	}
	if err == nil {
		if v := string(bytes.TrimSpace(raw)); ValidVersion(v) {
			return v, true
		}
	}
	return "", true
}

// Guard wacht nach einer Aktivierung. Gibt es kein pending, tut er nichts. Sonst
// wartet er bis zu timeout darauf, dass healthyMarker genau die Version aus pending
// enthält: dann wird pending entfernt. Läuft die Frist ab, wird pending erneut
// gelesen: Ist es weg, ist alles erledigt; nennt es eine andere Version, beginnt
// eine neue Wartezeit für diese; sonst ruft Guard rollback auf.
// Bei Abbruch von ctx geschieht kein Rückfall; pending bleibt für den nächsten Start.
func Guard(ctx context.Context, root, healthyMarker string, timeout time.Duration, rollback func(ctx context.Context) error) error {
	want, ok := readPending(root)
	if !ok {
		return nil
	}
	for {
		healthy, err := waitHealthy(ctx, healthyMarker, want, timeout)
		if err != nil {
			return err
		}
		if healthy {
			return removePending(root)
		}
		now, ok := readPending(root)
		if !ok {
			return nil
		}
		if now != want {
			want = now
			continue
		}
		return rollback(ctx)
	}
}

func waitHealthy(ctx context.Context, marker, want string, timeout time.Duration) (bool, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(pollInterval)
	defer tick.Stop()
	for {
		if want != "" && markerIs(marker, want) {
			return true, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-deadline.C:
			// Eine Gesundmeldung kurz vor Fristende zählt noch.
			return want != "" && markerIs(marker, want), nil
		case <-tick.C:
		}
	}
}

func markerIs(path, version string) bool {
	b, err := readSmall(path, maxMarkerBytes)
	return err == nil && string(bytes.TrimSpace(b)) == version
}
