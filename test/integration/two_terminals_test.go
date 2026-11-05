//go:build integration

// Package integration führt echte Dienste (app.Run) mit echtem RFB-Test-Server,
// echter Terminal-API über TLS und gefälschtem VNC-Client zusammen.
//
// Beide Terminals teilen sich hier /proc/net/tcp. Das Terminal sieht also auch
// die Verbindung des anderen; die Tests prüfen darum Client-Starts und -Stopps
// und nie, dass ein Terminal keine Verbindung sieht, solange ein anderes eine hat.
package integration

import (
	"strings"
	"testing"
	"time"

	"hmikiosk/internal/config"
)

func TestTwoTerminalsTakeoverAndLock(t *testing.T) {
	t.Parallel()
	t.Run("Übernahme mit Rückfrage und Vorwarnung", func(t *testing.T) {
		t.Parallel()
		vnc := startVNC(t)
		a, b := pair(t, vnc, press(vnc), config.RoleNormal, config.RoleNormal, testKey, testKey)
		a.connect("presse")

		b.post("/api/hmi/presse/open")
		if s := b.awaitState("confirming"); s.ConfirmName != "A" {
			t.Fatalf("Rückfrage nennt %q", s.ConfirmName)
		}
		if b.runner.started.Load() != 0 {
			t.Fatal("Client vor der Antwort gestartet")
		}
		start := time.Now()
		b.post("/api/confirm")
		if s := a.awaitState("warned"); !strings.Contains(s.Notice, "B") {
			t.Fatalf("Vorwarnung %q", s.Notice)
		}
		a.awaitNotice("tiles", "Übernommen von B")
		b.awaitState("connected")
		if d := time.Since(start); d < 4*time.Second {
			t.Fatalf("Vorwarnung nach %v vorbei", d)
		}
		if a.runner.running() != 0 || b.runner.running() != 1 {
			t.Fatalf("Clients laufen: A %d, B %d", a.runner.running(), b.runner.running())
		}
	})
	t.Run("Sperre", func(t *testing.T) {
		t.Parallel()
		vnc := startVNC(t)
		h := press(vnc)
		lock := config.ModeLock
		h.Mode = &lock
		a, b := pair(t, vnc, h, config.RoleNormal, config.RoleNormal, testKey, testKey)
		a.connect("presse")

		b.post("/api/hmi/presse/open")
		b.awaitNotice("tiles", "Belegt durch A")
		if b.runner.started.Load() != 0 {
			t.Fatal("Client trotz Sperre gestartet")
		}
		if s := a.snapshot(); s.State != "connected" || a.runner.running() != 1 {
			t.Fatalf("A gestört: %+v", s)
		}
		await(t, "Belegt-Hinweis auf der Kachel", func() bool {
			return strings.Contains(b.tilesPage(), "belegt durch A")
		})
		if strings.Contains(a.tilesPage(), "belegt durch") {
			t.Fatal("eigene Sitzung zeigt die Kachel als belegt")
		}
	})
}

func TestPrimaryPreemptsNormal(t *testing.T) {
	t.Parallel()
	vnc := startVNC(t)
	a, b := pair(t, vnc, press(vnc), config.RoleNormal, config.RolePrimary, testKey, testKey)
	a.connect("presse")

	// Primär fragt nicht zurück, die Vorwarnung bleibt.
	b.post("/api/hmi/presse/open")
	a.awaitState("warned")
	if s := b.snapshot(); s.State == "confirming" {
		t.Fatalf("Rückfrage beim primären Terminal: %+v", s)
	}
	a.awaitNotice("tiles", "Übernommen von B")
	b.awaitState("connected")

	// Umgekehrt verdrängt ein normales Terminal das primäre nie.
	a.post("/api/hmi/presse/open")
	a.awaitNotice("tiles", "Belegt durch B")
	if a.runner.running() != 0 || b.runner.running() != 1 {
		t.Fatalf("Clients laufen: A %d, B %d", a.runner.running(), b.runner.running())
	}
}

func TestPeerKilledHMIFreeAfter6s(t *testing.T) {
	t.Parallel()
	vnc := startVNC(t)
	a, b := pair(t, vnc, press(vnc), config.RoleNormal, config.RoleNormal, testKey, testKey)
	a.connect("presse")
	await(t, "Belegung aus dem Herzschlag", func() bool {
		return strings.Contains(b.tilesPage(), "belegt durch A")
	})

	start := time.Now()
	a.kill()
	// Der letzte Herzschlag ist höchstens 2 s alt: Die Frist von 6 s läuft also frühestens nach 4 s ab.
	await(t, "freie Kachel", func() bool {
		return !strings.Contains(b.tilesPage(), "belegt durch")
	})
	d := time.Since(start)
	if d < 3500*time.Millisecond || d > 8*time.Second {
		t.Fatalf("frei nach %v", d)
	}

	b.post("/api/hmi/presse/open")
	s := b.awaitState("connected")
	if len(s.Unreachable) != 1 || s.Unreachable[0] != "A" {
		t.Fatalf("Hinweis auf das tote Terminal fehlt: %+v", s)
	}
}

func TestWrongSharedKeyAllowsWithNotice(t *testing.T) {
	t.Parallel()
	vnc := startVNC(t)
	a, b := pair(t, vnc, press(vnc), config.RoleNormal, config.RoleNormal, testKey, "ein-ganz-anderer-schluessel-hier")
	a.connect("presse")

	b.post("/api/hmi/presse/open")
	s := b.awaitState("connected")
	if len(s.BadKey) != 1 || s.BadKey[0] != "A" {
		t.Fatalf("Hinweis fehlt: %+v", s)
	}
	if len(s.Unreachable) != 0 {
		t.Fatalf("falscher Schlüssel als nicht erreichbar gemeldet: %+v", s)
	}
	if a.runner.running() != 1 || b.runner.running() != 1 {
		t.Fatalf("Clients laufen: A %d, B %d", a.runner.running(), b.runner.running())
	}
}

func TestPermanentDisplayReconnectsAfterServerRestart(t *testing.T) {
	t.Parallel()
	vnc := startVNC(t)
	a := startTerminal(t, terminalSpec{name: "A", role: config.RoleNormal, hmi: press(vnc), permanent: true, vnc: vnc})
	a.awaitState("connected")
	if a.runner.started.Load() != 1 {
		t.Fatalf("%d Clients gestartet", a.runner.started.Load())
	}

	vnc.stop()
	s := a.awaitState("reconnecting")
	if !strings.Contains(s.Notice, "Neuer Versuch") {
		t.Fatalf("Hinweis %q", s.Notice)
	}
	vnc.restart()
	await(t, "neue Verbindung", func() bool {
		return a.snapshot().State == "connected" && a.runner.started.Load() >= 2
	})
	if a.runner.running() != 1 {
		t.Fatalf("%d Clients laufen", a.runner.running())
	}
}
