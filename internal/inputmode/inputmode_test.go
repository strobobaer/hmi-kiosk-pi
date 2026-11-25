package inputmode

import (
	"os"
	"path/filepath"
	"testing"
)

func setup(t *testing.T, input, setting string) {
	t.Helper()
	dir := t.TempDir()
	if input != "" {
		if err := os.WriteFile(filepath.Join(dir, "input"), []byte(input), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if setting != "" {
		if err := os.WriteFile(filepath.Join(dir, "input-setting"), []byte(setting), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := sessionDir
	sessionDir = dir
	t.Cleanup(func() { sessionDir = old })
}

func TestEffectiveExplicitModesIgnoreSession(t *testing.T) {
	setup(t, "mouse\n", "")
	if got := Effective("touch"); got != "touch" {
		t.Errorf("touch: %q", got)
	}
	if got := Effective(""); got != "touch" {
		t.Errorf("leer: %q", got)
	}
	setup(t, "touch\n", "")
	if got := Effective("mouse"); got != "mouse" {
		t.Errorf("mouse: %q", got)
	}
}

func TestEffectiveAutoFollowsSession(t *testing.T) {
	setup(t, "mouse\n", "")
	if got := Effective("auto"); got != "mouse" {
		t.Errorf("auto mit Maus: %q", got)
	}
	setup(t, "touch\n", "")
	if got := Effective("auto"); got != "touch" {
		t.Errorf("auto ohne Maus: %q", got)
	}
}

func TestEffectiveAutoWithoutOrBrokenFileIsTouch(t *testing.T) {
	setup(t, "", "")
	if got := Effective("auto"); got != "touch" {
		t.Errorf("ohne Datei: %q", got)
	}
	setup(t, "stift\n", "")
	if got := Effective("auto"); got != "touch" {
		t.Errorf("unbekannter Wert: %q", got)
	}
}

func TestRunning(t *testing.T) {
	setup(t, "mouse\n", "auto\n")
	mode, setting, ok := Running()
	if !ok || mode != "mouse" || setting != "auto" {
		t.Fatalf("Running: %q %q %v", mode, setting, ok)
	}
	setup(t, "", "")
	if _, _, ok := Running(); ok {
		t.Fatal("ohne Datei darf es keine laufende Sitzung geben")
	}
	// Ältere Sitzung ohne Vermerk der Einstellung: Wert bekannt, Einstellung unbekannt.
	setup(t, "touch\n", "")
	mode, setting, ok = Running()
	if !ok || mode != "touch" || setting != "" {
		t.Fatalf("Running ohne Vermerk: %q %q %v", mode, setting, ok)
	}
}
