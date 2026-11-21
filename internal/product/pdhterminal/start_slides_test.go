package pdhterminal

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"hmikiosk/internal/config"
	"hmikiosk/internal/product/pdhterminal/slides"
)

// slideCache legt einen gültigen Zwischenspeicher an.
func slideCache(t *testing.T) *slides.Store {
	t.Helper()
	st := &slides.Store{Dir: filepath.Join(t.TempDir(), "slides")}
	if err := st.Save([]byte(`{"version":"v1","saved_at":"2026-11-21T10:30:00Z","slides":[{"id":"a","kind":"text","title":"Hallo","duration":10}],"images":{}}`)); err != nil {
		t.Fatal(err)
	}
	return st
}

// Offline mit Zwischenspeicher: die lokale Folienschau statt der Offline-Meldung; ohne: wie bisher.
func TestStartOfflineWithCacheRedirectsToSlides(t *testing.T) {
	p := newFakePDH(t)
	dir := t.TempDir()
	_ = SaveDevice(dir, Device{DeviceID: "a1-1", Secret: testSecret, URL: p.hostURL()})
	cfg := func() config.PDHCfg { return config.PDHCfg{URL: p.hostURL()} }
	client := p.client()
	p.srv.Close()

	// ohne Zwischenspeicher
	for _, st := range []*slides.Store{nil, {Dir: filepath.Join(t.TempDir(), "leer")}} {
		h := StartHandler(dir, cfg, client, nil, st)
		w := startReq(t, h, "GET", "/start", "127.0.0.1:8080")
		if w.Code != 200 || !strings.Contains(w.Body.String(), "PDH nicht erreichbar. Neuer Versuch in 30 Sekunden.") {
			t.Errorf("ohne Zwischenspeicher: %d %s", w.Code, w.Body)
		}
	}
	// mit Zwischenspeicher
	st := slideCache(t)
	h := StartHandler(dir, cfg, client, nil, st)
	w := startReq(t, h, "GET", "/start", "127.0.0.1:8080")
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/slides" {
		t.Fatalf("mit Zwischenspeicher: %d %v", w.Code, w.Header())
	}
	if w := startReq(t, h, "GET", "/slides", "127.0.0.1:8080"); w.Code != 200 || !strings.Contains(w.Body.String(), "Server offline") {
		t.Errorf("/slides: %d", w.Code)
	}
	if w := startReq(t, h, "GET", "/slides/online", "127.0.0.1:8080"); !strings.Contains(w.Body.String(), `"online":false`) {
		t.Errorf("online: %s", w.Body)
	}
	if w := startReq(t, h, "GET", "/slides", "evil:8080"); w.Code != http.StatusForbidden {
		t.Errorf("fremder Host: %d", w.Code)
	}
	// Zwischenspeicher gelöscht: wieder die Offline-Meldung
	if err := st.Clear(); err != nil {
		t.Fatal(err)
	}
	if w := startReq(t, h, "GET", "/start", "127.0.0.1:8080"); w.Code != 200 || !strings.Contains(w.Body.String(), "PDH nicht erreichbar") {
		t.Errorf("nach Löschen: %d", w.Code)
	}
}

// Ist PDH erreichbar, bleibt es bei der Anmeldung, auch mit Zwischenspeicher; /slides/online meldet true.
func TestStartOnlineWithCacheStillLogsIn(t *testing.T) {
	p := newFakePDH(t)
	dir := t.TempDir()
	_ = SaveDevice(dir, Device{DeviceID: "a1-1", Secret: testSecret, URL: p.hostURL()})
	h := StartHandler(dir, func() config.PDHCfg { return config.PDHCfg{URL: p.hostURL()} }, p.client(), nil, slideCache(t))
	if w := startReq(t, h, "GET", "/start", "127.0.0.1:8080"); w.Code != 200 || !strings.Contains(w.Body.String(), "<form") {
		t.Errorf("online: %d %s", w.Code, w.Body)
	}
	if w := startReq(t, h, "GET", "/slides/online", "127.0.0.1:8080"); !strings.Contains(w.Body.String(), `"online":true`) {
		t.Errorf("online: %s", w.Body)
	}
}

// Die Cache-Route liegt wie die Kartenrouten vor der GET/HEAD-Sperre und hinter der Host-Prüfung.
func TestStartSlidesCacheRoute(t *testing.T) {
	p := newFakePDH(t)
	st := &slides.Store{Dir: filepath.Join(t.TempDir(), "slides")}
	origin := cardOrigin(config.PDHCfg{URL: p.hostURL()})
	api := http.NewServeMux()
	api.Handle("/api/slides/cache", slides.CacheHandler(st, func() string { return origin }, nil))
	h := StartHandler(t.TempDir(), func() config.PDHCfg { return config.PDHCfg{URL: p.hostURL()} }, p.client(), api, st)
	do := func(method, host, org, body string) int {
		r := httptest.NewRequest(method, "/api/slides/cache", strings.NewReader(body))
		r.Host, r.RemoteAddr = host, "127.0.0.1:50000"
		if org != "" {
			r.Header.Set("Origin", org)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if c := do("POST", "127.0.0.1:8080", origin, `{"version":"v","slides":[]}`); c != 204 || !st.Present() {
		t.Fatalf("POST: %d, Present %v", c, st.Present())
	}
	if c := do("POST", "127.0.0.1:8080", "http://evil.example", `{"version":"v","slides":[]}`); c != 403 {
		t.Errorf("fremder Ursprung: %d", c)
	}
	if c := do("POST", "evil:8080", origin, `{"version":"v","slides":[]}`); c != 403 {
		t.Errorf("fremder Host: %d", c)
	}
	if c := do("DELETE", "127.0.0.1:8080", origin, ""); c != 204 || st.Present() {
		t.Errorf("DELETE: %d, Present %v", c, st.Present())
	}
}
