package web

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type fakeKeyboard struct {
	mu    sync.Mutex
	calls []string
	err   error
}

func (k *fakeKeyboard) rec(s string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.calls = append(k.calls, s)
	return k.err
}
func (k *fakeKeyboard) Show() error   { return k.rec("show") }
func (k *fakeKeyboard) Hide() error   { return k.rec("hide") }
func (k *fakeKeyboard) Toggle() error { return k.rec("toggle") }
func (k *fakeKeyboard) got() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]string(nil), k.calls...)
}

func serveWith(t *testing.T, ex LocalExtras) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(NewLocalWith(newFakeSession(), func() []TileView { return nil }, nil, ex))
	t.Cleanup(srv.Close)
	return srv
}

var gearRe = regexp.MustCompile(`<a[^>]*class="gear"[^>]*href="([^"]+)"`)

func TestTilesShowGearToLocalAdmin(t *testing.T) {
	srv := serveWith(t, LocalExtras{})
	body, _ := get(t, srv.URL+"/")
	m := gearRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("Zahnrad fehlt:\n%s", body)
	}
	if m[1] != "http://127.0.0.1:8081/admin/" {
		t.Fatalf("Zahnrad führt nach %q", m[1])
	}
	wantContains(t, body, `aria-label="Einstellungen"`, `<svg`)
}

func TestGearFromNetworkPointsToHTTPSAdmin(t *testing.T) {
	var on atomic.Bool
	on.Store(true)
	srv := serveWith(t, LocalExtras{Network: on.Load})
	for host, want := range map[string]string{
		"192.168.1.20:8080":  "https://192.168.1.20:8443/admin/",
		"[2001:db8::5]:8080": "https://[2001:db8::5]:8443/admin/",
	} {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
		req.Host = host
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		buf := make([]byte, 64<<10)
		for {
			n, err := resp.Body.Read(buf)
			b.Write(buf[:n])
			if err != nil {
				break
			}
		}
		resp.Body.Close()
		m := gearRe.FindStringSubmatch(b.String())
		if m == nil || m[1] != want {
			t.Errorf("Host %s: Zahnrad %v, erwartet %s", host, m, want)
		}
	}
}

func TestKeyboardAPICallsController(t *testing.T) {
	kb := &fakeKeyboard{}
	srv := serveWith(t, LocalExtras{Keyboard: kb})
	for _, a := range []string{"show", "hide", "toggle"} {
		if resp := post(t, srv.URL+"/api/keyboard/"+a, okOrigin); resp.StatusCode != http.StatusNoContent {
			t.Fatalf("%s: %d", a, resp.StatusCode)
		}
	}
	if got := strings.Join(kb.got(), ","); got != "show,hide,toggle" {
		t.Fatalf("Aufrufe %q", got)
	}
	if resp := post(t, srv.URL+"/api/keyboard/show", map[string]string{"Origin": "http://evil.example"}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("fremder Ursprung: %d", resp.StatusCode)
	}
	if resp := post(t, srv.URL+"/api/keyboard/show", nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("ohne Ursprung: %d", resp.StatusCode)
	}
	if n := len(kb.got()); n != 3 {
		t.Fatalf("%d Aufrufe nach abgelehnten Anfragen", n)
	}
}

func TestKeyboardAPIStaysLocalWithNetworkTiles(t *testing.T) {
	var on atomic.Bool
	on.Store(true)
	kb := &fakeKeyboard{}
	srv := serveWith(t, LocalExtras{Keyboard: kb, Network: on.Load})
	code := doReq(t, http.MethodPost, srv.URL+"/api/keyboard/show", "192.168.1.20:8080", map[string]string{"Origin": "http://192.168.1.20:8080"})
	if code != http.StatusForbidden {
		t.Fatalf("aus dem Netz: %d", code)
	}
	if len(kb.got()) != 0 {
		t.Fatal("Tastatur aus dem Netz gesteuert")
	}
}

func TestKeyboardAPIWithoutKeyboard(t *testing.T) {
	srv := serveWith(t, LocalExtras{})
	if resp := post(t, srv.URL+"/api/keyboard/show", okOrigin); resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("ohne Tastatur: %d", resp.StatusCode)
	}
	kb := &fakeKeyboard{err: errors.New("läuft nicht")}
	srv = serveWith(t, LocalExtras{Keyboard: kb})
	if resp := post(t, srv.URL+"/api/keyboard/toggle", okOrigin); resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("Fehler der Tastatur: %d", resp.StatusCode)
	}
}

func TestTilesKeyboardModeFollowsInputMode(t *testing.T) {
	for mode, want := range map[string]string{"touch": `data-osk="auto"`, "": `data-osk="auto"`, "mouse": `data-osk="manual"`} {
		srv := serveWith(t, LocalExtras{InputMode: func() string { return mode }})
		body, _ := get(t, srv.URL+"/")
		if !strings.Contains(body, want) {
			t.Errorf("Bedienart %q: %s fehlt", mode, want)
		}
		wantContains(t, body, `src="/keyboard.js"`)
	}
	srv := serveWith(t, LocalExtras{})
	body, _ := get(t, srv.URL+"/keyboard.js")
	wantContains(t, body, "focusin", "/api/keyboard/")
}

func TestBarHasKeyboardButton(t *testing.T) {
	srv := serveWith(t, LocalExtras{})
	body, _ := get(t, srv.URL+"/bar")
	wantContains(t, body, `data-osk-toggle`, `Tastatur`, `src="/keyboard.js"`)
}

func TestTilesShownOnlyForLocalHost(t *testing.T) {
	var on atomic.Bool
	on.Store(true)
	var shown atomic.Int32
	srv := serveWith(t, LocalExtras{Network: on.Load, TilesShown: func() { shown.Add(1) }})
	get(t, srv.URL+"/")
	if shown.Load() != 1 {
		t.Fatalf("Kachelseite lokal: %d", shown.Load())
	}
	doReq(t, http.MethodGet, srv.URL+"/", "192.168.1.20:8080", nil)
	get(t, srv.URL+"/bar")
	if shown.Load() != 1 {
		t.Fatalf("Aufruf aus dem Netz oder Leiste zählt: %d", shown.Load())
	}
}

func TestNoGearWithoutLocalAdmin(t *testing.T) {
	srv := serveWith(t, LocalExtras{LocalAdmin: func() bool { return false }})
	body, _ := get(t, srv.URL+"/")
	if gearRe.MatchString(body) {
		t.Fatal("Zahnrad ohne laufende Admin-Seite am Terminal: Chromium bliebe auf einer Fehlerseite stehen")
	}
}
