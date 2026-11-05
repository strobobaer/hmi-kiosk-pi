// Package platform erkennt Anzeigegröße, TCP-Zustand, Netzwerkschnittstellen
// und Touch-Aktivität des Terminals.
package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultWidth  = 1920
	defaultHeight = 1080
)

// DisplaySize liest die bevorzugte Auflösung des ersten verbundenen DRM-Ausgangs.
// Linux listet den bevorzugten Modus zuerst in "modes".
func DisplaySize(sysRoot string) (w, h int, found bool) {
	statuses, _ := filepath.Glob(filepath.Join(sysRoot, "class", "drm", "card*-*", "status"))
	for _, st := range statuses { // Glob liefert sortiert
		b, err := os.ReadFile(st)
		if err != nil || strings.TrimSpace(string(b)) != "connected" {
			continue
		}
		modes, err := os.ReadFile(filepath.Join(filepath.Dir(st), "modes"))
		if err != nil {
			continue
		}
		first, _, _ := strings.Cut(string(modes), "\n")
		var mw, mh int
		if _, err := fmt.Sscanf(strings.TrimSpace(first), "%dx%d", &mw, &mh); err == nil && mw > 0 && mh > 0 {
			return mw, mh, true
		}
	}
	return 0, 0, false
}

// Resolve wählt Override, dann erkannte Größe, dann 1920x1080.
func Resolve(overrideW, overrideH, w, h int, found bool) (int, int) {
	if overrideW > 0 && overrideH > 0 {
		return overrideW, overrideH
	}
	if found && w > 0 && h > 0 {
		return w, h
	}
	return defaultWidth, defaultHeight
}
