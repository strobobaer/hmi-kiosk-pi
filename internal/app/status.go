package app

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"hmikiosk/internal/config"
	"hmikiosk/internal/platform"
	"hmikiosk/internal/release"
	"hmikiosk/internal/web"
)

// warn merkt sich einen Hinweis für die Admin-Seite und schreibt ihn ins Protokoll.
func (s *service) warn(msg string) {
	log.Print(msg)
	s.mu.Lock()
	defer s.mu.Unlock()
	if !slices.Contains(s.warnings, msg) {
		s.warnings = append(s.warnings, msg)
	}
}

// Status liefert die Anzeigewerte der Admin-Seite.
func (s *service) Status() web.SystemStatus {
	s.mu.RLock()
	cfgWarn, keyWarn, boxWarn := s.cfgWarn, s.keyWarn, s.boxWarn
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
	add(keyWarn)
	if !s.box.Available() {
		add(boxWarn)
	}
	st.Warnings = append(st.Warnings, warnings...)
	add(s.journal.Warning())
	switch {
	case cfg.Terminal.MaintenanceManual:
		add(manualMaintenanceNotice)
	case config.HardwareChanged(cfg.Terminal.HardwareID, s.hwID):
		add(maintenanceNotice)
	case s.hwID == "":
		add("Die Hardware-Kennung des Geräts ist nicht lesbar: Ein auf ein anderes Gerät übertragenes Terminal würde nicht erkannt.")
	}
	if !st.OverlayActive {
		add("Der Schreibschutz (Overlay auf /) ist nicht aktiv.")
	}

	w, h, found := platform.DisplaySize(sysRoot)
	rw, rh := platform.Resolve(cfg.Display.OverrideW, cfg.Display.OverrideH, w, h, found)
	st.Display = fmt.Sprintf("%d×%d", rw, rh)
	override := cfg.Display.OverrideW > 0 && cfg.Display.OverrideH > 0
	switch {
	case override:
		st.Display += " (fest eingestellt)"
	case !found:
		st.Display += " (Standardwert)"
		add(fmt.Sprintf("Kein Monitor erkannt, es gilt %d×%d.", rw, rh))
	}

	if free, total, err := diskUsage(s.opts.DataDir); err == nil {
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
