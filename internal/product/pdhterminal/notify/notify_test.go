package notify

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestKinds(t *testing.T) {
	cases := []struct {
		in, want []string
	}{
		{nil, []string{}},
		{[]string{}, []string{}},
		{[]string{"banner", "overlay"}, []string{"overlay", "banner"}},
		{[]string{"off", "toast"}, []string{}},
		{[]string{"x", "front"}, []string{"front"}},
		{[]string{"front", "toast", "toast", "banner", "overlay"}, []string{"toast", "overlay", "banner", "front"}},
	}
	for _, c := range cases {
		if got := Kinds(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Kinds(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestText(t *testing.T) {
	cases := []struct {
		r          Result
		text, tone string
	}{
		{Result{OK: true, Name: "M. Muster"}, "Angemeldet: M. Muster", "ok"},
		{Result{OK: true, Name: "M. Muster", Logout: true}, "Abgemeldet: M. Muster", "neutral"},
		{Result{Message: "Karte unbekannt"}, "Karte unbekannt", "error"},
		{Result{}, "Anmeldung fehlgeschlagen", "error"},
	}
	for _, c := range cases {
		text, tone := Text(c.r)
		if text != c.text || tone != c.tone {
			t.Errorf("Text(%+v) = %q, %q", c.r, text, tone)
		}
	}
}

func TestClean(t *testing.T) {
	if got := Clean("  M.\x00 Mus\ter‮\n "); got != "M. Muser" {
		t.Errorf("Clean = %q", got)
	}
	if got := Clean(strings.Repeat("ä", 500)); len([]rune(got)) != MaxField {
		t.Errorf("Länge %d", len([]rune(got)))
	}
}

func TestClockText(t *testing.T) {
	if got := ClockText(time.Date(2026, 1, 2, 9, 5, 7, 0, time.UTC)); got != "09:05:07" {
		t.Errorf("ClockText = %q", got)
	}
}

func TestToastScriptPath(t *testing.T) {
	sep := string(filepath.Separator)
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	only := func(paths ...string) func(string) bool {
		return func(p string) bool {
			for _, q := range paths {
				if p == q {
					return true
				}
			}
			return false
		}
	}
	base := filepath.Join("X", "Base", "toast.ps1")
	pf := filepath.Join("X", "PF", "PDH-Terminal", "toast.ps1")
	cases := []struct {
		name   string
		env    map[string]string
		exe    string
		exists func(string) bool
		want   string
	}{
		{"Programmordner vom Starter", map[string]string{"PDH_TERMINAL_BASE": filepath.Join("X", "Base"), "ProgramFiles": filepath.Join("X", "PF")}, "", only(base, pf), base},
		{"Program Files", map[string]string{"ProgramFiles": filepath.Join("X", "PF")}, "", only(pf), pf},
		{"fehlt", map[string]string{"ProgramFiles": filepath.Join("X", "PF")}, "", only(), ""},
		{"Update unter ProgramData zählt nie", map[string]string{"ProgramData": "Y" + sep + "PD"}, "Y" + sep + "PD" + sep + "PDH-Terminal" + sep + "app" + sep + "1.2.3" + sep + "pdh-terminal.exe",
			only("Y" + sep + "PD" + sep + "PDH-Terminal" + sep + "app" + sep + "1.2.3" + sep + "toast.ps1"), ""},
		{"eigener Ordner (Entwicklung)", map[string]string{}, filepath.Join("D", "pdh-terminal.exe"), only(filepath.Join("D", "toast.ps1")), filepath.Join("D", "toast.ps1")},
	}
	for _, c := range cases {
		if got := ToastScriptPath(env(c.env), c.exe, c.exists); got != c.want {
			t.Errorf("%s: %q, erwartet %q", c.name, got, c.want)
		}
	}
}
