//go:build e2e

// Gegenstelle für den Ende-zu-Ende-Test mit PDH (test/pdh-e2e/run.sh): echter Handler der
// Fernbefehle auf dem echten HTTPS-Listener (newAdminServer, CertSource, selbst signiertes
// Zertifikat), nur die Sitzungsverwaltung ist ein Skript. Läuft bis SIGTERM.
//
//	HMIK_E2E=1 go test -tags e2e -run TestPDHE2EServe -timeout 0 ./internal/app/
package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"hmikiosk/internal/config"
	"hmikiosk/internal/release"
	"hmikiosk/internal/session"
	"hmikiosk/internal/web"
)

// Was PDH im Fall „geöffnet“ schicken muss (siehe kiosk_e2e_test.go in PDH).
const (
	e2eTerminal = "Prüfstand Halle 3"
	e2ePassword = "Gehëim-pw 8!"
	e2eUser     = "Jörg Müßig-Äpfelbaum"
)

// e2eSession: Antworten je Host (10.0.0.x), sonst wie die Sitzungsverwaltung.
type e2eSession struct {
	mu   sync.Mutex
	snap session.Snapshot
}

func (f *e2eSession) Snapshot() session.Snapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.snap
}

func (f *e2eSession) OpenRemote(_ context.Context, r session.RemoteRequest) (session.RemoteResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fmt.Printf("E2E_OPEN host=%s port=%d viewOnly=%v label=%q (%d Zeichen) user=%q pwOK=%v\n",
		r.Host, r.Port, r.ViewOnly, r.Label, len([]rune(r.Label)), r.User, r.Password == e2ePassword)
	switch r.Host {
	case "10.0.0.1": // öffnen, danach belegt bis close
		if f.snap.State != session.StateTiles {
			return session.RemoteResult{Outcome: session.RemoteBusy, HMI: f.snap.RemoteLabel, Since: f.snap.Since}, nil
		}
		if r.Password != e2ePassword || r.User != e2eUser || !r.ViewOnly || r.Port != 5900 {
			return session.RemoteResult{Outcome: session.RemoteInvalid, Message: "e2e: Passwort, Benutzer, Modus oder Port kam verändert an"}, nil
		}
		f.snap = session.Snapshot{State: session.StateConnected, HMI: session.RemoteAdHocID, HMIName: r.Label + " – von PDH: " + r.User,
			ViewOnly: true, Since: time.Now().Truncate(time.Second), Remote: true, RemoteLabel: r.Label}
		return session.RemoteResult{Outcome: session.RemoteOpened}, nil
	case "10.0.0.2":
		return session.RemoteResult{Outcome: session.RemoteBusy, HMI: "Presse 1", Since: time.Date(2026, 11, 9, 13, 5, 0, 0, time.UTC)}, nil
	case "10.0.0.3":
		return session.RemoteResult{Outcome: session.RemoteBusy, HMI: "Presse 1 (an Halle 4)"}, nil
	case "10.0.0.4": // Kachel steht auf „nur ansehen“: öffnet trotz control nur zum Ansehen
		if r.ViewOnly {
			return session.RemoteResult{Outcome: session.RemoteInvalid, Message: "e2e: control erwartet"}, nil
		}
		fmt.Println("E2E_VIEWONLY_TILE control → nur ansehen (Kachel)")
		return session.RemoteResult{Outcome: session.RemoteOpened}, nil
	case "10.0.0.5":
		return session.RemoteResult{Outcome: session.RemoteNotReady, Message: "HMI 10.0.0.5:5900 nicht erreichbar: <i>Zeitüberschreitung</i>"}, nil
	case "10.0.0.6":
		return session.RemoteResult{Outcome: session.RemoteInvalid, Message: "VNC-Anmeldung am HMI fehlgeschlagen: Das von PDH übergebene Passwort wurde abgelehnt."}, nil
	case "10.0.0.7":
		return session.RemoteResult{Outcome: session.RemoteNotReady, Message: "Wartungsmodus: " + strings.Repeat("sehr langer Grund ", 20)}, nil
	case "10.0.0.8":
		return session.RemoteResult{}, errors.New("interner Fehler")
	}
	return session.RemoteResult{Outcome: session.RemoteNotReady, Message: "e2e: unbekannter Fall " + r.Host}, nil
}

func (f *e2eSession) CloseRemote() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.snap.Remote {
		return false
	}
	f.snap = session.Snapshot{State: session.StateTiles}
	return true
}

func TestPDHE2EServe(t *testing.T) {
	if os.Getenv("HMIK_E2E") != "1" {
		t.Skip("nur mit HMIK_E2E=1 (test/pdh-e2e/run.sh)")
	}
	dir := t.TempDir()
	cert, err := web.LoadOrCreateCert(dir)
	if err != nil {
		t.Fatal(err)
	}
	src := web.NewCertSource(dir, cert)
	key, hash, err := web.NewRemoteKey()
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Terminal.Name = e2eTerminal
	cfg.Admin.RemoteKeyHash = hash
	if err := config.Validate(cfg); err != nil {
		t.Fatal(err)
	}
	api := web.NewRemoteAPI(web.RemoteDeps{
		Config:  func() config.Config { return cfg },
		Session: &e2eSession{snap: session.Snapshot{State: session.StateTiles}},
		Version: release.Version,
	})
	mux := http.NewServeMux()
	mux.Handle("/api/remote/", api)
	addr := os.Getenv("HMIK_E2E_ADDR")
	if addr == "" {
		addr = ":8443"
	}
	l, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	srv := newAdminServer(mux, src.GetCertificate)
	go func() { _ = srv.ServeTLS(l, "", "") }()
	// Fingerabdruck wie auf der Admin-Seite (mit Doppelpunkten).
	fmt.Printf("E2E_KEY=%s\nE2E_FINGERPRINT=%s\nE2E_READY %s\n", key, src.PublicKeyFingerprint(), l.Addr())
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	_ = srv.Close()
	if fp := src.PublicKeyFingerprint(); fp == "" {
		t.Error("kein Fingerabdruck")
	}
}
