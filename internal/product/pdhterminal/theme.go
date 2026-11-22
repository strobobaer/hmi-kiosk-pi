package pdhterminal

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Theme sind die Farben der eigenen Seiten des Terminals (Admin-Seite, Startseite, Offline-Folienschau).
// Sie gleichen dem Hintergrund der Terminalseiten von PDH. Die Grundwerte stammen aus dem CSS von PDH:
// dunkel aus web/templates/terminal.gohtml (die Terminalseite ist dunkel), hell aus dem hellen Erscheinungsbild
// von web/templates/base.gohtml. Schickt PDH eigene Farben (Feld theme, siehe docs/superpowers/specs/
// 2026-11-22-funktionen-sync-contract.md), gelten diese.
type Theme struct {
	Scheme string `json:"scheme"` // "dark" oder "light"
	Bg     string `json:"bg"`
	Bg2    string `json:"bg2"`
	Bg3    string `json:"bg3"`
	Text   string `json:"text"`
	Muted  string `json:"muted"`
	Border string `json:"border"`
}

// DarkTheme ist der Grundwert: die Farben der Terminalseite von PDH.
func DarkTheme() Theme {
	return Theme{Scheme: "dark", Bg: "#0f1117", Bg2: "#1a1d27", Bg3: "#222536", Text: "#f1f3fb", Muted: "#9aa3b8", Border: "#2f3348"}
}

// LightTheme sind die Farben des hellen Erscheinungsbilds von PDH.
func LightTheme() Theme {
	return Theme{Scheme: "light", Bg: "#f3f3f3", Bg2: "#ffffff", Bg3: "#f9f9f9", Text: "#1b1b1b", Muted: "#5f5f5f", Border: "#e2e2e2"}
}

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// ThemeWire ist das Feld theme in den Antworten von PDH; jedes Feld ist freiwillig.
type ThemeWire struct {
	Scheme string `json:"scheme"`
	Bg     string `json:"bg"`
	Bg2    string `json:"bg2"`
	Bg3    string `json:"bg3"`
	Text   string `json:"text"`
	Muted  string `json:"muted"`
	Border string `json:"border"`
}

// ThemeFromWire macht aus den Angaben von PDH ein Theme: Das Schema wählt die Grundwerte (unbekannt: dunkel),
// gültige Farben (#rrggbb) ersetzen einzelne Werte, alles andere wird ignoriert.
func ThemeFromWire(w ThemeWire) Theme {
	t := DarkTheme()
	if strings.EqualFold(w.Scheme, "light") {
		t = LightTheme()
	}
	set := func(dst *string, v string) {
		if hexColor.MatchString(v) {
			*dst = strings.ToLower(v)
		}
	}
	set(&t.Bg, w.Bg)
	set(&t.Bg2, w.Bg2)
	set(&t.Bg3, w.Bg3)
	set(&t.Text, w.Text)
	set(&t.Muted, w.Muted)
	set(&t.Border, w.Border)
	return t
}

// valid: alle Farben sind #rrggbb (Schutz gegen eine beschädigte Datei; das Ergebnis geht in CSS).
func (t Theme) valid() bool {
	for _, c := range []string{t.Bg, t.Bg2, t.Bg3, t.Text, t.Muted, t.Border} {
		if !hexColor.MatchString(c) {
			return false
		}
	}
	return t.Scheme == "dark" || t.Scheme == "light"
}

// CSS liefert die Farbvariablen als Regel für :root. --panel und --line sind die Namen der Admin-Seite.
func (t Theme) CSS() string {
	if !t.valid() {
		t = DarkTheme()
	}
	return fmt.Sprintf(":root{color-scheme:%s;--bg:%s;--bg2:%s;--bg3:%s;--text:%s;--muted:%s;--border:%s;--panel:%s;--line:%s}",
		t.Scheme, t.Bg, t.Bg2, t.Bg3, t.Text, t.Muted, t.Border, t.Bg2, t.Border)
}

func themePath(dataDir string) string { return filepath.Join(dataDir, "pdh", "theme.json") }

// LoadTheme liest das gespeicherte Theme; fehlt es oder ist es unbrauchbar, gilt DarkTheme.
func LoadTheme(dataDir string) Theme {
	b, err := os.ReadFile(themePath(dataDir))
	if err != nil {
		return DarkTheme()
	}
	var t Theme
	if json.Unmarshal(b, &t) != nil || !t.valid() {
		return DarkTheme()
	}
	return t
}

// SaveTheme schreibt das Theme (atomar).
func SaveTheme(dataDir string, t Theme) error {
	if !t.valid() {
		return errors.New("Farben ungültig")
	}
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return writeFileAtomic(themePath(dataDir), b)
}

// writeFileAtomic schreibt über eine Hilfsdatei im selben Ordner (Ordner 0700, Datei 0600).
func writeFileAtomic(path string, b []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(name)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	ok = true
	return nil
}
