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

func TestEffectiveFollowsRunningSession(t *testing.T) {
	setup(t, "mouse\n", "auto\n")
	for _, saved := range []string{"", "touch", "mouse", "auto"} {
		if got := Effective(saved); got != "mouse" {
			t.Errorf("gespeichert %q, Sitzung mouse: %q", saved, got)
		}
	}
	setup(t, "touch\n", "touch\n")
	for _, saved := range []string{"", "touch", "mouse", "auto"} {
		if got := Effective(saved); got != "touch" {
			t.Errorf("gespeichert %q, Sitzung touch: %q", saved, got)
		}
	}
}

func TestSuppressAutoKeyboard(t *testing.T) {
	setup(t, "mouse\n", "mouse\n")
	if SuppressAutoKeyboard("mouse", "") || SuppressAutoKeyboard("mouse", "auto") {
		t.Error("Maus und automatisch: darf nicht unterdrücken")
	}
	if !SuppressAutoKeyboard("mouse", "off") {
		t.Error("Maus und aus: muss unterdrücken")
	}
	setup(t, "touch\n", "touch\n")
	if SuppressAutoKeyboard("touch", "off") {
		t.Error("Touch und aus: darf nicht unterdrücken")
	}
	// Gespeichert mouse, Sitzung läuft noch mit touch: bis zum Neustart wie Touch.
	if SuppressAutoKeyboard("mouse", "off") {
		t.Error("Sitzung touch: darf nicht unterdrücken")
	}
}

func TestEffectiveWithoutSessionUsesSavedMouse(t *testing.T) {
	setup(t, "", "")
	if Effective("mouse") != "mouse" || Effective("touch") != "touch" || Effective("") != "touch" || Effective("auto") != "touch" {
		t.Error("ohne Sitzung gilt Maus nur, wenn so gespeichert")
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
