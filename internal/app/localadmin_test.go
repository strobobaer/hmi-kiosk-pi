package app

import (
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"hmikiosk/internal/coord"
)

func TestListenLoopbackOnly(t *testing.T) {
	for _, addr := range []string{":0", "0.0.0.0:0", "[::]:0", "192.0.2.1:0", "localhost:0", "hmi.example:0", "127.0.0.1"} {
		if l, err := listenLoopback(addr); err == nil {
			l.Close()
			t.Errorf("%q gebunden, erwartet Ablehnung", addr)
		}
	}
	l, err := listenLoopback("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if a := l.Addr().(*net.TCPAddr); !a.IP.IsLoopback() {
		t.Fatalf("gebunden an %v", a)
	}
}

type stepClock struct {
	coord.SystemClock
	mu  sync.Mutex
	now time.Time
}

func (c *stepClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *stepClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func TestLocalAdminPresenceExpiresAfterIdle(t *testing.T) {
	clk := &stepClock{now: time.Unix(1000, 0)}
	p := &adminPresence{clock: clk}
	if p.active() {
		t.Fatal("ohne Aufruf aktiv")
	}
	p.touch()
	clk.advance(localAdminIdle - time.Second)
	if !p.active() {
		t.Fatal("vor Ablauf nicht aktiv")
	}
	clk.advance(time.Second)
	if p.active() {
		t.Fatal("nach 5 Minuten noch aktiv")
	}
	p.touch()
	p.left()
	if p.active() {
		t.Fatal("nach Rückkehr noch aktiv")
	}
}

// adminLocal spricht die Admin-Seite am Terminal an.
func (f *fixture) adminLocal(method, path, origin string) (int, string, http.Header) {
	f.t.Helper()
	req, err := http.NewRequest(method, "http://"+f.bound.LocalAdmin+path, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	req.Host = "127.0.0.1:8081"
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := insecureClient().Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

func TestServiceServesLocalAdminOnLoopback(t *testing.T) {
	f := newFixture(t)
	f.saveConfig(nil)
	f.start()
	if f.bound.LocalAdmin == "" {
		t.Fatal("Admin-Seite am Terminal läuft nicht")
	}
	host, _, _ := net.SplitHostPort(f.bound.LocalAdmin)
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		t.Fatalf("gebunden an %s", f.bound.LocalAdmin)
	}
	if code, _, _ := f.adminLocal(http.MethodGet, "/admin/setup", ""); code != http.StatusOK {
		t.Fatalf("/admin/setup: %d", code)
	}
	if code, _, _ := f.adminLocal(http.MethodGet, "/peer/occupancy", ""); code != http.StatusNotFound {
		t.Fatalf("/peer/ am Terminal: %d", code)
	}
}

func TestLocalAdminBlocksSessionsUntilTilesReturn(t *testing.T) {
	f := newFixture(t)
	f.saveConfig(nil)
	f.start()
	if msg := f.s.sessionBlocked(); msg != "" {
		t.Fatalf("gesperrt ohne Admin-Seite: %q", msg)
	}
	f.adminLocal(http.MethodGet, "/admin/setup", "")
	if msg := f.s.sessionBlocked(); !strings.Contains(msg, "Admin-Seite") {
		t.Fatalf("Admin-Seite am Terminal sperrt nicht: %q", msg)
	}
	if code, _ := f.local(http.MethodGet, "/"); code != http.StatusOK {
		t.Fatalf("Kachelseite: %d", code)
	}
	if msg := f.s.sessionBlocked(); msg != "" {
		t.Fatalf("nach Rückkehr zur Kachelseite noch gesperrt: %q", msg)
	}
}

type recordingKeyboard struct {
	mu    sync.Mutex
	calls []string
}

func (k *recordingKeyboard) rec(s string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.calls = append(k.calls, s)
	return nil
}
func (k *recordingKeyboard) Show() error   { return k.rec("show") }
func (k *recordingKeyboard) Hide() error   { return k.rec("hide") }
func (k *recordingKeyboard) Toggle() error { return k.rec("toggle") }
func (k *recordingKeyboard) got() string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return strings.Join(k.calls, ",")
}

func TestKeyboardAPIReachesController(t *testing.T) {
	f := newFixture(t)
	f.saveConfig(nil)
	kb := &recordingKeyboard{}
	f.startWith(func(s *service) { s.kbd = kb })
	if code, _ := f.local(http.MethodPost, "/api/keyboard/show"); code != http.StatusNoContent {
		t.Fatalf("Kachelseite: %d", code)
	}
	if code, _, _ := f.adminLocal(http.MethodPost, "/api/keyboard/toggle", "http://127.0.0.1:8081"); code != http.StatusNoContent {
		t.Fatalf("Admin am Terminal: %d", code)
	}
	if got := kb.got(); got != "show,toggle" {
		t.Fatalf("Aufrufe %q", got)
	}
}

func TestKeyboardAPIWithoutWvkbdDoesNotCrash(t *testing.T) {
	f := newFixture(t)
	f.saveConfig(nil)
	f.start() // ohne Chromium läuft auch keine Tastatur
	if code, _ := f.local(http.MethodPost, "/api/keyboard/show"); code != http.StatusServiceUnavailable {
		t.Fatalf("ohne wvkbd: %d", code)
	}
	if code, _ := f.local(http.MethodGet, "/"); code != http.StatusOK {
		t.Fatalf("Kachelseite danach: %d", code)
	}
}
