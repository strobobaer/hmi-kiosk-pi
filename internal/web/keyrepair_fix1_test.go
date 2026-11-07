package web

import (
	"bytes"
	"errors"
	"os"
	"testing"

	"hmikiosk/internal/config"
	"hmikiosk/internal/secrets"
)

// Der alte Schlüssel bleibt als secrets.key.old liegen, bis die Konfiguration gespeichert ist.

func (f *keyFixture) oldPath() string { return f.path + ".old" }

func crashingSave(config.Config) error { panic("Strom weg") }

func runCrashing(t *testing.T, f *keyFixture) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("Absturz erwartet")
		}
	}()
	_, _, _ = repairKeys(f.path, f.device, f.cfg, crashingSave)
}

func TestRepairKeepsOldKeyUntilConfigIsSavedThenRemovesIt(t *testing.T) {
	f := newKeyFixture(t)
	old := f.keyBytes()
	sawOld := false
	_, _, err := repairKeys(f.path, f.device, f.cfg, func(config.Config) error {
		b, err := os.ReadFile(f.oldPath())
		sawOld = err == nil && bytes.Equal(b, old)
		return nil
	})
	if err != nil || !sawOld {
		t.Fatalf("err=%v, alter Schlüssel beim Speichern vorhanden: %v", err, sawOld)
	}
	if _, err := os.Stat(f.oldPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(".old nach Erfolg nicht entfernt")
	}
}

func TestRepairSaveFailureRemovesOldKeyCopy(t *testing.T) {
	f := newKeyFixture(t)
	_, _, _ = repairKeys(f.path, f.device, f.cfg, func(config.Config) error { return errors.New("kaputt") })
	if _, err := os.Stat(f.oldPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(".old nach Rücknahme nicht entfernt")
	}
}

func TestRepairOldKeyCopyIsPrivate(t *testing.T) {
	f := newKeyFixture(t)
	var mode os.FileMode
	_, _, _ = repairKeys(f.path, f.device, f.cfg, func(config.Config) error {
		fi, err := os.Stat(f.oldPath())
		if err != nil {
			t.Error(err)
		} else {
			mode = fi.Mode().Perm()
		}
		return nil
	})
	if mode != 0o600 {
		t.Fatalf("Rechte %v", mode)
	}
}

func TestPowerLossWindowIsRolledBackOnNextStart(t *testing.T) {
	f := newKeyFixture(t)
	old := f.keyBytes()
	runCrashing(t, f)
	if bytes.Equal(old, f.keyBytes()) {
		t.Fatal("Testaufbau: neuer Schlüssel erwartet")
	}
	restarted, err := secrets.OpenBox(f.path, true)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(scanKeys(f.cfg, restarted).Names()); n != 3 {
		t.Fatalf("Zustand nach Absturz: %d unlesbar", n)
	}
	restored, err := RecoverInterruptedRepair(f.path, restarted, f.cfg)
	if err != nil || !restored {
		t.Fatalf("restored=%v err=%v", restored, err)
	}
	if !bytes.Equal(old, f.keyBytes()) {
		t.Fatal("alter Schlüssel nicht zurück")
	}
	if _, err := os.Stat(f.oldPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(".old bleibt")
	}
	if pw, err := restarted.Open(f.cfg.HMIs[0].PasswordEnc); err != nil || pw != "pw-a" {
		t.Fatalf("Passwort nach Rücknahme: %q %v", pw, err)
	}
}

func TestRecoverWorksWhenNewKeyFileIsGone(t *testing.T) {
	f := newKeyFixture(t)
	old := f.keyBytes()
	runCrashing(t, f)
	os.Remove(f.path)
	restored, err := RecoverInterruptedRepair(f.path, secrets.Unavailable(), f.cfg)
	if err != nil || !restored || !bytes.Equal(old, f.keyBytes()) {
		t.Fatalf("restored=%v err=%v", restored, err)
	}
}

func TestRecoverOnlyRemovesLeftoverAfterCompletedRepair(t *testing.T) {
	f := newKeyFixture(t)
	oldRaw := f.keyBytes()
	out, _, err := repairKeys(f.path, f.device, f.cfg, func(config.Config) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	newRaw := f.keyBytes()
	if err := os.WriteFile(f.oldPath(), oldRaw, 0o600); err != nil { // Strom weg vor dem Aufräumen
		t.Fatal(err)
	}
	restored, err := RecoverInterruptedRepair(f.path, f.device, out)
	if err != nil || restored {
		t.Fatalf("restored=%v err=%v", restored, err)
	}
	if !bytes.Equal(newRaw, f.keyBytes()) {
		t.Fatal("fertige Reparatur zurückgenommen")
	}
	if _, err := os.Stat(f.oldPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(".old bleibt")
	}
}

func TestRecoverWithoutOldFileDoesNothing(t *testing.T) {
	f := newKeyFixture(t)
	restored, err := RecoverInterruptedRepair(f.path, f.device, f.cfg)
	if err != nil || restored {
		t.Fatalf("restored=%v err=%v", restored, err)
	}
}
