package app

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hmikiosk/internal/config"
)

const wantMaintenanceNotice = "Wartungsmodus: Das Terminal wurde auf ein anderes Gerät übertragen. Einrichtung in der Admin-Seite."

func (f *fixture) peerNonceStatus() int {
	f.t.Helper()
	resp, err := insecureClient().Post("https://"+f.bound.Admin+"/peer/nonce", "application/json", nil)
	if err != nil {
		f.t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestEmptyHardwareIDIsSetAndSavedAtStart(t *testing.T) {
	f := newFixture(t)
	f.hw = "10000000abcdef01"
	f.saveConfig(oneHMI) // Altbestand ohne Kennung
	f.start()
	saved, _, err := config.Open(f.data).Load()
	if err != nil || saved.Terminal.HardwareID != f.hw {
		t.Fatalf("gespeichert %q (%v)", saved.Terminal.HardwareID, err)
	}
	if f.s.config().Terminal.HardwareID != f.hw {
		t.Fatal("nicht im laufenden Dienst")
	}
	if f.s.maintenance() {
		t.Fatal("Wartungsmodus beim ersten Gerät")
	}
}

func TestFirstStartRecordsHardwareID(t *testing.T) {
	f := newFixture(t)
	f.hw = "10000000abcdef01"
	f.start()
	saved, _, err := config.Open(f.data).Load()
	if err != nil || saved.Terminal.HardwareID != f.hw {
		t.Fatalf("gespeichert %q (%v)", saved.Terminal.HardwareID, err)
	}
}

func TestMatchingHardwareIDRunsNormally(t *testing.T) {
	f := newFixture(t)
	f.hw = "aaaa"
	f.saveConfig(func(c *config.Config) { oneHMI(c); c.Terminal.HardwareID = "aaaa" })
	f.start()
	if f.s.maintenance() || f.s.coord.Standby() {
		t.Fatal("Wartungsmodus trotz gleicher Kennung")
	}
	if code := f.peerNonceStatus(); code != http.StatusOK {
		t.Fatalf("Peer-API %d", code)
	}
	if n := f.bound.Snapshot().Notice; n != "" {
		t.Fatalf("Hinweis %q", n)
	}
}

func TestDifferentHardwareIDStartsMaintenance(t *testing.T) {
	f := newFixture(t)
	f.hw = "bbbb"
	f.saveConfig(func(c *config.Config) { oneHMI(c); c.Terminal.HardwareID = "aaaa" })
	f.start()

	if !f.s.maintenance() {
		t.Fatal("kein Wartungsmodus")
	}
	if !f.s.coord.Standby() {
		t.Fatal("Koordinator nicht still")
	}
	if code := f.peerNonceStatus(); code != http.StatusServiceUnavailable {
		t.Fatalf("Peer-API %d", code)
	}
	eventually(t, "Hinweis", func() bool { return f.bound.Snapshot().Notice == wantMaintenanceNotice })
	if code, body := f.local("GET", "/"); code != 200 || !strings.Contains(body, "Presse 7") {
		t.Fatalf("Kachelseite %d", code)
	}
	code, body := f.local("POST", "/api/hmi/presse/open")
	if code != http.StatusConflict || !strings.Contains(body, wantMaintenanceNotice) {
		t.Fatalf("Kachel: %d %s", code, body)
	}
	if n := len(f.runner.started()); n != 0 {
		t.Fatalf("%d Clients gestartet", n)
	}
	// Die gespeicherte Kennung bleibt, bis der Admin entscheidet.
	saved, _, _ := config.Open(f.data).Load()
	if saved.Terminal.HardwareID != "aaaa" {
		t.Fatalf("Kennung überschrieben: %q", saved.Terminal.HardwareID)
	}
}

func TestMaintenanceBlocksPermanentDisplay(t *testing.T) {
	f := newFixture(t)
	f.hw = "bbbb"
	f.saveConfig(func(c *config.Config) {
		oneHMI(c)
		c.Kiosk.PermanentHMI = "presse"
		c.Terminal.HardwareID = "aaaa"
	})
	f.start()
	if st := f.bound.Snapshot().State; st != "tiles" {
		t.Fatalf("Zustand %q", st)
	}
	if n := len(f.runner.started()); n != 0 {
		t.Fatalf("%d Clients gestartet", n)
	}
}

func TestApplyWithNewHardwareIDEndsMaintenance(t *testing.T) {
	f := newFixture(t)
	f.hw = "bbbb"
	cfg := f.saveConfig(func(c *config.Config) { oneHMI(c); c.Terminal.HardwareID = "aaaa" })
	f.start()
	eventually(t, "Hinweis", func() bool { return f.bound.Snapshot().Notice == wantMaintenanceNotice })

	cfg.Terminal.HardwareID = "bbbb"
	f.s.Apply(cfg)
	if f.s.maintenance() || f.s.coord.Standby() {
		t.Fatal("Wartungsmodus dauert an")
	}
	if code := f.peerNonceStatus(); code != http.StatusOK {
		t.Fatalf("Peer-API %d", code)
	}
	eventually(t, "Hinweis weg", func() bool { return f.bound.Snapshot().Notice == "" })
	if hasWarning(f.bound.Status().Warnings, "Wartungsmodus") {
		t.Fatal("Warnung bleibt")
	}
}

func TestUnreadableHardwareIDOnlyWarns(t *testing.T) {
	f := newFixture(t)
	f.saveConfig(func(c *config.Config) { oneHMI(c); c.Terminal.HardwareID = "aaaa" })
	f.start()
	if f.s.maintenance() {
		t.Fatal("Wartungsmodus ohne lesbare Kennung")
	}
	if w := f.bound.Status().Warnings; !hasWarning(w, "Hardware-Kennung") {
		t.Fatalf("Warnungen %v", w)
	}
	saved, _, _ := config.Open(f.data).Load()
	if saved.Terminal.HardwareID != "aaaa" {
		t.Fatalf("Kennung verloren: %q", saved.Terminal.HardwareID)
	}
}

func TestMaintenanceShownInStatus(t *testing.T) {
	f := newFixture(t)
	f.hw = "bbbb"
	f.saveConfig(func(c *config.Config) { c.Terminal.HardwareID = "aaaa" })
	f.start()
	if w := f.bound.Status().Warnings; !hasWarning(w, "Wartungsmodus") {
		t.Fatalf("Warnungen %v", w)
	}
}

func TestHardwareIDNotAdoptedFromFallbackConfig(t *testing.T) {
	f := newFixture(t)
	f.hw = "10000000abcdef01"
	f.saveConfig(oneHMI)
	if err := config.Open(f.data).MarkGood(); err != nil {
		t.Fatal(err)
	}
	// config.json defekt: es gilt der Rückfall, der nicht überschrieben werden darf.
	if err := writeBroken(f.data); err != nil {
		t.Fatal(err)
	}
	f.start()
	if saved, _, err := config.Open(f.data).Load(); err != nil || saved.Terminal.HardwareID != "" {
		t.Fatalf("Rückfall überschrieben: %q %v", saved.Terminal.HardwareID, err)
	}
}

func writeBroken(dir string) error {
	return os.WriteFile(filepath.Join(dir, "config.json"), []byte("{kaputt"), 0o600)
}
