// Package notify meldet das Ergebnis einer Kartenanmeldung am Bildschirm, wenn das
// Browser-Fenster des Terminals nicht vorn liegt (Toast, Überlagerung, Banner, nach vorn).
package notify

import (
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"
)

// Die Arten der Benachrichtigung in ihrer festen Reihenfolge; off schließt alle aus.
const (
	KindToast   = "toast"
	KindOverlay = "overlay"
	KindBanner  = "banner"
	KindFront   = "front"
	kindOff     = "off"
)

// Die Töne bestimmen die Farbe.
const (
	ToneOK      = "ok"
	ToneError   = "error"
	ToneNeutral = "neutral"
)

// MaxField ist die größte Länge von Name und Meldung (in Zeichen).
const MaxField = 120

// Result ist die Rückmeldung der PDH-Seite (POST /api/card/result).
type Result struct {
	OK      bool     `json:"ok"`
	Name    string   `json:"name"`
	Message string   `json:"message"`
	Notify  []string `json:"notify"`
	Logout  bool     `json:"logout"`
}

// Shower zeigt eine Benachrichtigung der gegebenen Arten.
type Shower interface {
	Show(kinds []string, text, tone string)
}

// Kinds wählt die bekannten Arten in fester Reihenfolge. off schließt alles aus, Unbekanntes
// zählt nicht, und ohne Angabe von PDH (alter Stand) gibt es keine Benachrichtigung.
func Kinds(field []string) []string {
	if slices.Contains(field, kindOff) {
		return []string{}
	}
	out := []string{}
	for _, k := range []string{KindToast, KindOverlay, KindBanner, KindFront} {
		if slices.Contains(field, k) {
			out = append(out, k)
		}
	}
	return out
}

// Text ist die Zeile für den Bildschirm und der Ton (ok, neutral, error).
func Text(r Result) (text, tone string) {
	switch {
	case !r.OK:
		if m := strings.TrimSpace(r.Message); m != "" {
			return m, ToneError
		}
		return "Anmeldung fehlgeschlagen", ToneError
	case r.Logout:
		return "Abgemeldet: " + r.Name, ToneNeutral
	default:
		return "Angemeldet: " + r.Name, ToneOK
	}
}

// Clean macht einen Text aus dem Netz anzeigbar: ohne Steuerzeichen und ohne Zeichen, die die
// Schreibrichtung umschalten, am Rand getrimmt und auf MaxField Zeichen gekürzt.
func Clean(s string) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if unicode.IsControl(r) || (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069) || r == 0xFEFF {
			continue
		}
		if n == MaxField {
			break
		}
		b.WriteRune(r)
		n++
	}
	return strings.TrimSpace(b.String())
}

// Der dauerhafte Hinweis des Kiosks, wenn Edge fehlt (kein Browser-Fenster, kein Tray, keine Bedienung): Er bleibt, bis
// Edge startet, und sagt, wie ein Administrator an den PC kommt.
const (
	HintEdgeTitle  = "Edge nicht gefunden"
	HintEdgeDetail = "Strg+Alt+Entf → Benutzer wechseln (Administrator)"
)

// EdgeHint entscheidet, ob der Hinweis steht: nur im Kiosk und nur, solange Edge fehlt. In der Betriebsart Zusatz
// gibt es das Tray-Symbol (rot) und die Selbstprüfung; ein Fenster würde dort die Arbeit stören.
func EdgeHint(kiosk, edgeMissing bool) bool { return kiosk && edgeMissing }

// ToastScriptName ist das feste Skript der Windows-Mitteilung; das Setup legt es in den Programmordner.
const ToastScriptName = "toast.ps1"

// ClockText ist die Uhrzeit in der Überlagerung (Name, Ergebnis, Uhrzeit).
func ClockText(t time.Time) string { return t.Format("15:04:05") }

// ToastScriptPath findet das Skript der Mitteilung. Gesucht wird nur im Programmordner (den der Starter in
// PDH_TERMINAL_BASE mitgibt, sonst Program Files), nie neben einem Update unter ProgramData, das jeder Benutzer
// beschreiben darf. Ohne Fund ist das Ergebnis leer: Es gibt dann keine Mitteilung.
func ToastScriptPath(getenv func(string) string, exe string, exists func(string) bool) string {
	var dirs []string
	if b := getenv("PDH_TERMINAL_BASE"); b != "" {
		dirs = append(dirs, b)
	}
	for _, v := range []string{"ProgramW6432", "ProgramFiles"} {
		if p := getenv(v); p != "" {
			dirs = append(dirs, filepath.Join(p, "PDH-Terminal"))
		}
	}
	// Direkt gestartetes Programm (Entwicklung): sein eigener Ordner, außer er liegt unter ProgramData.
	if exe != "" {
		d := filepath.Dir(exe)
		if pd := getenv("ProgramData"); pd == "" || !strings.HasPrefix(strings.ToLower(d), strings.ToLower(filepath.Clean(pd))+string(filepath.Separator)) {
			dirs = append(dirs, d)
		}
	}
	for _, d := range dirs {
		if p := filepath.Join(d, ToastScriptName); exists(p) {
			return p
		}
	}
	return ""
}
