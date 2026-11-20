// Package notify meldet das Ergebnis einer Kartenanmeldung am Bildschirm, wenn das
// Browser-Fenster des Terminals nicht vorn liegt (Toast, Überlagerung, Banner, nach vorn).
package notify

import (
	"slices"
	"strings"
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
