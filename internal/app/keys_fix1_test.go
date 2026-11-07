package app

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"hmikiosk/internal/config"
	"hmikiosk/internal/secrets"
	"hmikiosk/internal/session"
)

func TestManualMaintenanceEndsRunningSessionAndPermanentDisplay(t *testing.T) {
	for name, permanent := range map[string]bool{"Sitzung": false, "Daueranzeige": true} {
		t.Run(name, func(t *testing.T) {
			port := listenVNC(t, "")
			f := newFixture(t)
			f.hw = "aaaa"
			cfg := f.saveConfig(func(c *config.Config) {
				c.HMIs = []config.HMI{{ID: "presse", Name: "Presse 7", Host: "127.0.0.1", Port: port, Icon: "press", Color: "blue"}}
				c.Terminal.HardwareID = "aaaa"
				if permanent {
					c.Kiosk.PermanentHMI = "presse"
				}
			})
			f.start()
			if !permanent {
				if code, _ := f.local("POST", "/api/hmi/presse/open"); code != http.StatusNoContent {
					t.Fatalf("Open %d", code)
				}
			}
			eventually(t, "Client", func() bool { return len(f.runner.started()) == 1 })

			cfg.Terminal.MaintenanceManual = true
			f.s.Apply(cfg)
			eventually(t, "Kacheln mit Hinweis", func() bool {
				s := f.bound.Snapshot()
				return s.State == session.StateTiles && s.Notice == wantManualMaintenanceNotice
			})
			if _, ok := f.s.coord.Occupancy()["presse"]; ok {
				t.Fatal("HMI bleibt belegt")
			}
		})
	}
}

func TestStartRollsBackInterruptedRepair(t *testing.T) {
	f := newFixture(t)
	enc := f.seal("geheim")
	f.saveConfig(func(c *config.Config) { oneHMI(c); c.HMIs[0].PasswordEnc = enc })
	keyPath := filepath.Join(f.data, "secrets.key")
	old, _ := os.ReadFile(keyPath)
	// Stand nach einem Absturz mitten in der Reparatur: neuer Schlüssel, alte Konfiguration, alte Kopie.
	if err := os.WriteFile(keyPath+".old", old, 0o600); err != nil {
		t.Fatal(err)
	}
	fresh, _ := secrets.GenerateKey()
	if err := secrets.WriteKeyFile(keyPath, fresh); err != nil {
		t.Fatal(err)
	}
	s, err := newService(f.options())
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	if pw, err := s.password(s.config().HMIs[0]); err != nil || pw != "geheim" {
		t.Fatalf("Passwort nach Rücknahme: %q %v", pw, err)
	}
	if _, err := os.Stat(keyPath + ".old"); err == nil {
		t.Fatal(".old bleibt")
	}
	if !hasWarning(s.Status().Warnings, "Reparatur") {
		t.Fatalf("Warnungen %v", s.Status().Warnings)
	}
}
