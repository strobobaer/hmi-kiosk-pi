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

// manualMaintenanceNotice gilt, wenn der Admin den Wartungsmodus selbst gestartet hat.
const manualMaintenanceNotice = "Wartungsmodus: Von Hand gestartet. Einrichtung in der Admin-Seite."

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

// maintenance meldet, ob die Karte auf einem anderen Gerät läuft als eingerichtet
// oder der Admin den Wartungsmodus von Hand gestartet hat.
func (s *service) maintenance() bool {
	return config.InMaintenance(s.config(), s.hwID)
}

// maintenanceMessage ist der Hinweis zum gerade geltenden Grund; leer außerhalb des Wartungsmodus.
func (s *service) maintenanceMessage() string {
	switch cfg := s.config(); {
	case cfg.Terminal.MaintenanceManual:
		return manualMaintenanceNotice
	case config.HardwareChanged(cfg.Terminal.HardwareID, s.hwID):
		return maintenanceNotice
	}
	return ""
}

// sessionBlocked sperrt den Start echter Sitzungen im Wartungsmodus und während
// der Dauerprüfung.
func (s *service) sessionBlocked() string {
	if msg := s.maintenanceMessage(); msg != "" {
		return msg
	}
	return s.soakBlocked()
}

// syncMaintenance gleicht Koordinator und Hinweis der Kachelseite an den Zustand an.
func (s *service) syncMaintenance() {
	msg := s.maintenanceMessage()
	started := s.rootCtx() != nil // sonst läuft der Automat noch nicht
	// Eine laufende Sitzung oder Daueranzeige endet vor der Stille, sonst könnte ein
	// anderes Terminal das HMI nehmen, während hier noch ein Client darauf läuft.
	if started && msg != "" {
		s.mgr.EndForMaintenance(msg)
	}
	s.coord.SetStandby(msg != "")
	if !started {
		return
	}
	for _, n := range []string{maintenanceNotice, manualMaintenanceNotice} {
		if n != msg {
			s.mgr.ClearNotice(n)
		}
	}
}
