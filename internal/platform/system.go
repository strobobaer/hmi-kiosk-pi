package platform

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

const (
	// SystemLevelFile kennzeichnet den Stand der Systemteile (Abbild oder install.sh).
	SystemLevelFile = "etc/hmi-kiosk/system-level"
	// ChromiumPolicyFile ist die Navigationssperre des Kiosk-Chromium.
	ChromiumPolicyFile = "etc/chromium/policies/managed/hmi-kiosk.json"

	maxSystemLevel = 9999
	maxPolicyBytes = 64 << 10
)

// InstalledSystemLevel liest den Stand der Systemteile unter root (Produktion "/").
// Systeme vor der Kennzeichnung (bis 0.1.6) haben keine Datei: Erlaubt ihre
// Chromium-Richtlinie schon die Admin-Seite am Terminal, sind es Systemteile von 0.1.6
// (Stand 1), sonst älter (Stand 0). Eine unlesbare Datei gilt als Stand 0.
func InstalledSystemLevel(root string) int {
	b, err := os.ReadFile(filepath.Join(root, SystemLevelFile))
	if errors.Is(err, fs.ErrNotExist) {
		if _, perr := os.Stat(filepath.Join(root, ChromiumPolicyFile)); perr == nil &&
			ChromiumAllows(root, "http://127.0.0.1:8081") {
			return 1
		}
		return 0
	}
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || n < 0 || n > maxSystemLevel {
		return 0
	}
	return n
}

// ChromiumAllows meldet, ob die Chromium-Richtlinie unter root den Ursprung
// (scheme://host:port) öffnen lässt. Ohne Richtlinie sperrt Chromium nichts. Eine
// unlesbare Richtlinie gilt als Sperre: Lieber kein Zahnrad als eines auf eine Fehlerseite.
func ChromiumAllows(root, origin string) bool {
	f, err := os.Open(filepath.Join(root, ChromiumPolicyFile))
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	if err != nil {
		return false
	}
	defer f.Close()
	var p struct {
		URLBlocklist []string
		URLAllowlist []string
	}
	dec := json.NewDecoder(io.LimitReader(f, maxPolicyBytes))
	if err := dec.Decode(&p); err != nil {
		return false
	}
	if !slices.Contains(p.URLBlocklist, "*") {
		return true
	}
	_, hostport, _ := strings.Cut(origin, "://")
	for _, a := range p.URLAllowlist {
		a = strings.TrimSuffix(a, "/")
		if a == origin || a == hostport {
			return true
		}
	}
	return false
}
