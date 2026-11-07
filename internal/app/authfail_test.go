package app

import (
	"net/http"
	"testing"

	"hmikiosk/internal/config"
	"hmikiosk/internal/session"
)

func storedHMI(t *testing.T, f *fixture, id string) config.HMI {
	t.Helper()
	cfg, _, err := config.Open(f.data).Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range cfg.HMIs {
		if h.ID == id {
			return h
		}
	}
	t.Fatalf("HMI %q fehlt", id)
	return config.HMI{}
}

func TestRejectedPasswordIsSavedAndTileShowsIt(t *testing.T) {
	port := listenVNC(t, "geheim")
	f := newFixture(t)
	enc := f.seal("falsch")
	f.saveConfig(func(c *config.Config) {
		oneHMI(c)
		c.HMIs[0].Host, c.HMIs[0].Port, c.HMIs[0].PasswordEnc = "127.0.0.1", port, enc
		c.HMIs[0].Reach = config.ReachOff
	})
	f.start()
	if code, _ := f.local("POST", "/api/hmi/presse/open"); code != http.StatusNoContent {
		t.Fatalf("Open %d", code)
	}
	eventually(t, "Markierung gespeichert", func() bool { return storedHMI(t, f, "presse").AuthFailed })
	eventually(t, "Kachel zeigt es", func() bool {
		tiles := f.s.tiles()
		return len(tiles) == 1 && tiles[0].AuthFailed
	})
	if f.s.config().HMIs[0].AuthFailed != true {
		t.Fatal("laufende Konfiguration ohne Markierung")
	}
	// Weiteres Antippen startet keine Anmeldung und keinen Client.
	if code, _ := f.local("POST", "/api/hmi/presse/open"); code != http.StatusConflict {
		t.Fatalf("zweites Open: %d", code)
	}
	if s := f.bound.Snapshot(); s.State != session.StateTiles || len(f.runner.started()) != 0 {
		t.Fatalf("Snapshot %+v, Clients %d", s, len(f.runner.started()))
	}
}

func TestMarkAuthFailedIgnoresUnknownHMI(t *testing.T) {
	f := newFixture(t)
	f.saveConfig(oneHMI)
	f.start()
	f.s.markAuthFailed("gibtsnicht", "")
	if storedHMI(t, f, "presse").AuthFailed {
		t.Fatal("falsches HMI markiert")
	}
}

// Hat der Admin das Passwort geändert, während die Prüfung lief, gilt das neue
// Passwort nicht als abgelehnt.
func TestMarkAuthFailedSkipsChangedPassword(t *testing.T) {
	f := newFixture(t)
	old, cur := f.seal("alt"), f.seal("neu")
	f.saveConfig(func(c *config.Config) {
		oneHMI(c)
		c.HMIs[0].PasswordEnc = cur
	})
	f.start()
	f.s.markAuthFailed("presse", old)
	if storedHMI(t, f, "presse").AuthFailed || f.s.config().HMIs[0].AuthFailed {
		t.Fatal("geändertes Passwort als abgelehnt markiert")
	}
	f.s.markAuthFailed("presse", cur)
	if !storedHMI(t, f, "presse").AuthFailed || !f.s.config().HMIs[0].AuthFailed {
		t.Fatal("abgelehntes aktuelles Passwort nicht markiert")
	}
}
