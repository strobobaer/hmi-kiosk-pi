//go:build windows

package winstart

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Zwischen Prüfung und Start darf die Exe weder beschrieben noch ersetzt, gelöscht oder umbenannt werden.
func TestLockCandidateBlocksTamperingUntilClosed(t *testing.T) {
	f := newFixture(t)
	c := f.install(t, "0.0.2")
	l, err := LockCandidate(c, f.pub)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.Exe, []byte("Trojaner"), 0o755); err == nil {
		t.Error("Schreiben trotz Sperre")
	}
	if err := os.Remove(c.Exe); err == nil {
		t.Error("Löschen trotz Sperre")
	}
	if err := os.Rename(c.Exe, c.Exe+".alt"); err == nil {
		t.Error("Umbenennen trotz Sperre")
	}
	if err := os.Rename(filepath.Dir(c.Exe), filepath.Dir(c.Exe)+"x"); err == nil {
		t.Error("Umbenennen des Ordners trotz Sperre")
	}
	// Lesen bleibt möglich (der Prozessstart liest die Datei).
	if b, err := os.ReadFile(c.Exe); err != nil || string(b) != "exe-0.0.2" {
		t.Errorf("Lesen: %q %v", b, err)
	}
	l.Close()
	if err := os.WriteFile(c.Exe, []byte("frei"), 0o755); err != nil {
		t.Errorf("nach Close: %v", err)
	}
}

// Ein Verweis als Exe oder als Versionsordner wird abgelehnt (er ließe sich nach der Prüfung umbiegen).
func TestLockCandidateRejectsLinks(t *testing.T) {
	f := newFixture(t)
	c := f.install(t, "0.0.2")
	real := c.Exe + ".real"
	if err := os.Rename(c.Exe, real); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, c.Exe); err != nil {
		t.Skipf("Verweis nicht anlegbar (Rechte): %v", err)
	}
	if err := VerifyCandidate(c, f.pub); err == nil {
		t.Error("Verweis als Exe angenommen")
	}
}

// Ein Verbindungspunkt (Junction, ohne besondere Rechte anlegbar) als Versionsordner wird abgelehnt.
func TestLockCandidateRejectsJunctionFolder(t *testing.T) {
	f := newFixture(t)
	c := f.install(t, "0.0.2")
	dir := filepath.Dir(c.Exe)
	real := dir + "-real"
	if err := os.Rename(dir, real); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", dir, real).CombinedOutput(); err != nil {
		t.Skipf("Junction nicht anlegbar: %v %s", err, out)
	}
	if err := VerifyCandidate(c, f.pub); err == nil {
		t.Error("Junction als Versionsordner angenommen")
	}
}
