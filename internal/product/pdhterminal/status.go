package pdhterminal

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"hmikiosk/internal/platform"
	"hmikiosk/internal/release"
	"hmikiosk/internal/web"
)

const execLimit = 10 * time.Second

var bootDirs = []string{"/boot/firmware", "/boot"}

func runCommand(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, execLimit)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	return string(out), err
}

func readBootConfig() (string, error) {
	var firstErr error
	for _, dir := range bootDirs {
		b, err := os.ReadFile(filepath.Join(dir, "config.txt"))
		if err == nil {
			return string(b), nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return "", firstErr
}

// status liefert die Anzeigewerte der Admin-Seite.
func (s *service) status() web.SystemStatus {
	s.mu.RLock()
	cfgWarn, boxWarn := s.cfgWarn, s.boxWarn
	warnings := slices.Clone(s.warnings)
	cfg := s.cfg
	s.mu.RUnlock()

	st := web.SystemStatus{Version: release.Version, OverlayActive: overlayActive(procRoot)}
	add := func(w string) {
		if w != "" {
			st.Warnings = append(st.Warnings, w)
		}
	}
	add(cfgWarn)
	if !s.box.Available() {
		add(boxWarn)
	}
	st.Warnings = append(st.Warnings, warnings...)
	add(s.journal.Warning())
	if !st.OverlayActive {
		add("Der Schreibschutz (Overlay auf /) ist nicht aktiv.")
	}
	if have := platform.InstalledSystemLevel(rootFS, localAdminOrigin); have < release.PDHTerminal.SystemLevel {
		add(fmt.Sprintf("Die Systemteile dieses Geräts (Stand %d) sind älter, als diese Programmversion braucht (Stand %d). "+
			"Sie funktionieren erst nach einem neuen SD-Abbild oder erneutem install.sh aus dem Bundle dieser Version "+
			"(vorher die Konfiguration sichern).", have, release.PDHTerminal.SystemLevel))
	}
	w, h, found := platform.DisplaySize(sysRoot)
	rw, rh := platform.Resolve(cfg.Display.OverrideW, cfg.Display.OverrideH, w, h, found)
	st.Display = fmt.Sprintf("%d×%d", rw, rh)
	switch {
	case cfg.Display.OverrideW > 0 && cfg.Display.OverrideH > 0:
		st.Display += " (fest eingestellt)"
	case !found:
		st.Display += " (Standardwert)"
		add(fmt.Sprintf("Kein Monitor erkannt, es gilt %d×%d.", rw, rh))
	}
	if free, total, err := diskUsage(s.o.DataDir); err == nil {
		st.DataFreeBytes, st.DataTotalBytes = free, total
	} else {
		add(fmt.Sprintf("Datenpartition nicht verfügbar: %v", err))
	}
	return st
}

// overlayActive wertet den letzten Eintrag für / aus: Er verdeckt die früheren.
func overlayActive(root string) bool {
	data, err := os.ReadFile(filepath.Join(root, "mounts"))
	if err != nil {
		return false
	}
	active := false
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[1] == "/" {
			active = fields[2] == "overlay"
		}
	}
	return active
}
