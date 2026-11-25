// Package inputmode bestimmt die wirksame Bedienart (Touch oder Maus) des Terminals.
//
// Die Erkennung einer Maus (Einstellung „auto“) macht allein die Sitzung des Kiosk-Benutzers
// (hmi-kiosk-session prepare, über udev); sie legt das Ergebnis im Laufzeitordner der Sitzung ab. Dieses Paket
// liest es nur: eine Quelle der Wahrheit, nirgends eine zweite Erkennung.
package inputmode

import (
	"os"
	"path/filepath"
	"strings"

	"hmikiosk/internal/config"
)

// SessionDir ist der Laufzeitordner der Sitzung (gehört dem Kiosk-Benutzer, lesbar für alle).
const SessionDir = "/run/hmi-kiosk-session"

// Dateien darin: input enthält die wirksame Bedienart (touch oder mouse) der laufenden Sitzung,
// input-setting die Einstellung, mit der die Sitzung gestartet wurde (touch, mouse, auto).
const (
	inputFile   = "input"
	settingFile = "input-setting"
)

// sessionDir ist für Tests ersetzbar.
var sessionDir = SessionDir

func read(name string) string {
	b, err := os.ReadFile(filepath.Join(sessionDir, name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Running liefert, was die laufende Sitzung beim Start festgelegt hat: die wirksame Bedienart und die Einstellung,
// aus der sie entstand (leer bei einer Sitzung ohne diesen Vermerk). ok ist false, wenn es keine gültige Angabe gibt
// (Start außerhalb des Abbilds, Windows).
func Running() (mode, setting string, ok bool) {
	switch m := read(inputFile); m {
	case config.InputTouch, config.InputMouse:
		mode = m
	default:
		return "", "", false
	}
	switch s := read(settingFile); s {
	case config.InputTouch, config.InputMouse, config.InputAuto:
		setting = s
	}
	return mode, setting, true
}

// Effective ist die wirksame Bedienart (touch oder mouse): die der laufenden Sitzung (Zeiger und Tastatur gehören
// zusammen; eine gespeicherte Änderung zeigt die Admin-Seite nur als „gilt nach dem Neustart“). Ohne Angabe der
// Sitzung (Start außerhalb des Abbilds) gilt die gespeicherte Einstellung: Maus wie gespeichert, sonst Touch.
func Effective(setting string) string {
	if m, _, ok := Running(); ok {
		return m
	}
	if setting == config.InputMouse {
		return config.InputMouse
	}
	return config.InputTouch
}

// SuppressAutoKeyboard sagt, ob die Bildschirmtastatur beim Feld-Fokus unterbleibt: nur bei wirksamer Maus und
// eingestelltem „aus“ (angeschlossene Tastatur). Sonst öffnet sie sich wie bisher.
func SuppressAutoKeyboard(setting, oskWithMouse string) bool {
	return oskWithMouse == config.OSKOff && Effective(setting) == config.InputMouse
}

// SetDir ersetzt den Ordner der Sitzung (nur für Tests anderer Pakete) und liefert die Rückkehr.
func SetDir(dir string) (restore func()) {
	old := sessionDir
	sessionDir = dir
	return func() { sessionDir = old }
}
