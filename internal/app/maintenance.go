package app

import (
	"fmt"
	"log"

	"hmikiosk/internal/config"
	"hmikiosk/internal/platform"
)

// maintenanceNotice sperrt die Kacheln, wenn die SD-Karte auf ein anderes Gerät
// übertragen wurde (Hardware-Kennung weicht von der gespeicherten ab).
const maintenanceNotice = "Wartungsmodus: Das Terminal wurde auf ein anderes Gerät übertragen. Einrichtung in der Admin-Seite."

func (s *service) detectHardwareID() string {
	if s.opts.HardwareID != nil {
		return s.opts.HardwareID()
	}
	return platform.HardwareID(procRoot, sysRoot)
}

// claimHardware hält die Kennung des ersten Geräts fest (Neuinstallation oder
// Altbestand ohne Kennung). Ein Rückfallstand bleibt unangetastet: Ihn zu
// überschreiben ginge auf Kosten der verlorenen aktuellen Datei.
func (s *service) claimHardware() {
	if s.hwID == "" {
		log.Print("Keine Hardware-Kennung lesbar, die Erkennung eines geklonten Geräts ist aus")
		return
	}
	if s.loadFailed || s.usedFallback {
		return
	}
	cfg := s.config()
	if cfg.Terminal.HardwareID != "" {
		return
	}
	cfg.Terminal.HardwareID = s.hwID
	if err := s.store.Save(cfg); err != nil {
		s.warn(fmt.Sprintf("Hardware-Kennung nicht gespeichert, sie gilt nur bis zum Neustart: %v", err))
	}
	s.mu.Lock()
	s.cfg.Terminal.HardwareID = s.hwID
	s.mu.Unlock()
}

// maintenance meldet, ob die Karte auf einem anderen Gerät läuft als eingerichtet.
func (s *service) maintenance() bool {
	return config.HardwareChanged(s.config().Terminal.HardwareID, s.hwID)
}

// sessionBlocked sperrt den Start echter Sitzungen im Wartungsmodus und während
// der Dauerprüfung.
func (s *service) sessionBlocked() string {
	if s.maintenance() {
		return maintenanceNotice
	}
	return s.soakBlocked()
}

// syncMaintenance gleicht Koordinator und Hinweis der Kachelseite an den Zustand an.
func (s *service) syncMaintenance() {
	on := s.maintenance()
	s.coord.SetStandby(on)
	if s.rootCtx() == nil { // der Automat läuft noch nicht
		return
	}
	if on {
		s.mgr.SetNotice(maintenanceNotice)
	} else {
		s.mgr.ClearNotice(maintenanceNotice)
	}
}
