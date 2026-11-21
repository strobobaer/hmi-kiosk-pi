package pdhterminal

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestKioskAdminSection(t *testing.T) {
	var logoffs int
	e := newAdminEnvWith(t, newFakePDH(t).client(), func(p *adminProduct) {
		p.kiosk, p.logoff = true, func() error { logoffs++; return nil }
	})
	r := e.get("/admin/kiosk")
	body := r.Body.String()
	if r.Code != http.StatusOK || !strings.Contains(body, "Kiosk-Benutzer abmelden") ||
		!strings.Contains(body, "Strg+Alt+Entf") || !strings.Contains(body, "Benutzer wechseln") {
		t.Fatalf("Kiosk-Abschnitt: %d\n%s", r.Code, body)
	}
	if !strings.Contains(e.get("/admin/pdh").Body.String(), `href="/admin/kiosk"`) {
		t.Error("Navigation ohne Kiosk")
	}
	// Ohne Bestätigung keine Abmeldung.
	if r := e.post("/admin/kiosk/logoff", url.Values{}); r.Code != http.StatusBadRequest || logoffs != 0 {
		t.Fatalf("ohne Bestätigung: %d, Abmeldungen %d", r.Code, logoffs)
	}
	if r := e.post("/admin/kiosk/logoff", url.Values{"confirm": {"1"}}); r.Code != http.StatusOK || logoffs != 1 {
		t.Fatalf("mit Bestätigung: %d, Abmeldungen %d", r.Code, logoffs)
	}
}

func TestKioskLogoffFailureShown(t *testing.T) {
	e := newAdminEnvWith(t, newFakePDH(t).client(), func(p *adminProduct) {
		p.kiosk, p.logoff = true, func() error { return errors.New("verweigert") }
	})
	r := e.post("/admin/kiosk/logoff", url.Values{"confirm": {"1"}})
	if r.Code != http.StatusInternalServerError || !strings.Contains(r.Body.String(), "verweigert") {
		t.Fatalf("Fehler: %d\n%s", r.Code, r.Body.String())
	}
}

func TestKioskSectionAbsentOutsideKiosk(t *testing.T) {
	e := newAdminEnv(t, newFakePDH(t).client())
	if r := e.get("/admin/kiosk"); r.Code != http.StatusNotFound {
		t.Fatalf("Kiosk-Seite ohne Kiosk: %d", r.Code)
	}
	if body := e.get("/admin/pdh").Body.String(); strings.Contains(body, "/admin/kiosk") {
		t.Error("Navigation zeigt Kiosk ohne Kiosk")
	}
	if r := e.post("/admin/kiosk/logoff", url.Values{"confirm": {"1"}}); r.Code == http.StatusOK {
		t.Fatalf("Abmeldung ohne Kiosk möglich: %d", r.Code)
	}
}

func TestKioskNoTrayNoMinimize(t *testing.T) {
	// Kein Tray: Auch mit TrayEnabled und einem Symbol wird im Kiosk keines gestartet.
	ft := newFakeTray()
	s := &service{o: Options{Kiosk: true, TrayEnabled: true, tray: ft}}
	stop := s.startTray(context.Background(), Bound{}, func(func()) {})
	stop()
	select {
	case <-ft.started:
		t.Fatal("Tray im Kiosk gestartet")
	default:
	}
	// Kein Minimieren nach einem Neustart; ohne Kiosk bleibt es.
	called := false
	if f := restartHook(true, func(int) { called = true }); f != nil {
		t.Fatal("Neustart-Hook im Kiosk")
	}
	if f := restartHook(false, func(int) { called = true }); f == nil {
		t.Fatal("Zusatz-Modus ohne Minimieren")
	} else {
		f(1)
	}
	if !called {
		t.Fatal("Minimieren im Zusatz-Modus nicht gerufen")
	}
}

func TestKioskNotifierIsOff(t *testing.T) {
	s := &service{o: Options{Kiosk: true}}
	if _, ok := s.notifier().(noNotify); !ok {
		t.Fatalf("Benachrichtigung im Kiosk: %T", s.notifier())
	}
	s = &service{}
	if _, ok := s.notifier().(noNotify); ok {
		t.Fatal("Benachrichtigung im Zusatz-Modus aus")
	}
}

func TestKioskRunStartsNoTray(t *testing.T) {
	ft := newFakeTray()
	rootFS = t.TempDir()
	t.Cleanup(func() { rootFS = "/" })
	ready := make(chan Bound, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			DataDir: t.TempDir(), RunDir: t.TempDir(),
			LocalAddr: "127.0.0.1:0", AdminAddr: "127.0.0.1:0", LocalAdminAddr: freeAddr(t),
			HelperSocket: filepath.Join(t.TempDir(), "kein.sock"),
			HardwareID:   func() string { return "test-0001" },
			Ready:        func(b Bound) { ready <- b },
			tray:         ft, Kiosk: true,
		})
	}()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("Dienst endete früh: %v", err)
	case <-time.After(20 * time.Second):
		t.Fatal("Dienst startet nicht")
	}
	select {
	case <-ft.started:
		t.Fatal("Tray im Kiosk gestartet")
	case <-time.After(200 * time.Millisecond):
	}
	cancel()
	<-done
}

func TestAdminGestureWithoutBrowserIsHarmless(t *testing.T) {
	(&service{}).adminGesture() // kein Browser: nur ein Protokolleintrag
}
