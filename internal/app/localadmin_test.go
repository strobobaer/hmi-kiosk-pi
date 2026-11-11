package app

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"hmikiosk/internal/config"
	"hmikiosk/internal/coord"
)

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
	} else {
		// wie Chromium nach dem Antippen des Zahnrads auf der Kachelseite
		req.Header.Set("Sec-Fetch-Mode", "navigate")
		req.Header.Set("Sec-Fetch-Site", "same-site")
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

func TestGearOnlyWhileLocalAdminRuns(t *testing.T) {
	f := newFixture(t)
	f.saveConfig(nil)
	f.start()
	if _, body := f.local(http.MethodGet, "/"); !strings.Contains(body, `class="gear"`) {
		t.Fatal("Zahnrad fehlt, obwohl die Admin-Seite am Terminal läuft")
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	g := newFixture(t)
	g.saveConfig(nil)
	g.startWith(func(s *service) { s.opts.LocalAdminAddr = l.Addr().String() })
	if g.bound.LocalAdmin != "" {
		t.Fatal("belegter Port gebunden")
	}
	if _, body := g.local(http.MethodGet, "/"); strings.Contains(body, `class="gear"`) {
		t.Fatal("Zahnrad ohne Admin-Seite am Terminal")
	}
}

func TestLocalOnlyAPIsRejectForgedHostFromNetwork(t *testing.T) {
	f := newFixture(t)
	f.saveConfig(func(c *config.Config) { c.Kiosk.NetworkTiles = true })
	kb := &recordingKeyboard{}
	f.startWith(func(s *service) { s.kbd = kb })
	f.adminLocal(http.MethodGet, "/admin/setup", "")
	f.s.presence.Touch()
	h := f.s.localHandler()
	for _, path := range []string{"/api/selftest/run", "/api/selftest/status", "/api/keyboard/show"} {
		method := http.MethodPost
		if strings.HasSuffix(path, "status") {
			method = http.MethodGet
		}
		req := httptest.NewRequest(method, "http://127.0.0.1:8080"+path, nil)
		req.Host, req.RemoteAddr = "127.0.0.1:8080", "192.168.1.20:4444"
		req.Header.Set("Origin", "http://127.0.0.1:8080")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s aus dem Netz: %d", path, w.Code)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/", nil)
	req.Host, req.RemoteAddr = "127.0.0.1:8080", "192.168.1.20:4444"
	h.ServeHTTP(httptest.NewRecorder(), req)
	if kb.got() != "" {
		t.Fatalf("Tastatur aus dem Netz: %q", kb.got())
	}
	if !f.s.presence.Active() {
		t.Fatal("Aufruf aus dem Netz beendet die Admin-Seite am Terminal")
	}
}

func TestForeignRequestsDoNotHoldLocalAdminOpen(t *testing.T) {
	f := newFixture(t)
	f.saveConfig(nil)
	f.start()
	for _, path := range []string{"/admin/admin.css", "/admin/login", "/admin/setup"} {
		req, _ := http.NewRequest(http.MethodGet, "http://"+f.bound.LocalAdmin+path, nil)
		req.Host = "127.0.0.1:8081"
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		req.Header.Set("Sec-Fetch-Mode", "no-cors")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	if msg := f.s.sessionBlocked(); msg != "" {
		t.Fatalf("Abruf einer fremden Seite sperrt Sitzungen: %q", msg)
	}
}

func TestNoticeClearedWhenPresenceExpiredNaturally(t *testing.T) {
	f := newFixture(t)
	f.saveConfig(nil)
	clk := &stepClock{now: time.Unix(1000, 0)}
	f.startWith(func(s *service) { s.presence.Clock = clk })
	f.s.presence.Touch()
	_ = f.s.mgr.Open("irgendein-hmi")
	if got := f.s.mgr.Snapshot().Notice; got != localAdminNotice {
		t.Fatalf("Hinweis %q", got)
	}
	clk.advance(localAdminIdle + time.Second)
	f.local(http.MethodGet, "/")
	if got := f.s.mgr.Snapshot().Notice; got == localAdminNotice {
		t.Fatal("Hinweis bleibt nach abgelaufener Admin-Seite stehen")
	}
}
