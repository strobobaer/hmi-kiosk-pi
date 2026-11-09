package web

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// fromNet ist eine Anfrage aus dem Netz, die Host und Origin des Terminals vortäuscht.
func fromNet(method, path, remote string) *http.Request {
	req := httptest.NewRequest(method, "http://127.0.0.1:8080"+path, nil)
	req.Host = "127.0.0.1:8080"
	req.RemoteAddr = remote
	req.Header.Set("Origin", "http://127.0.0.1:8080")
	return req
}

func TestLocalOnlyRoutesNeedLoopbackPeer(t *testing.T) {
	var on atomic.Bool
	on.Store(true)
	kb := &fakeKeyboard{}
	var shown, hits atomic.Int32
	h := NewLocalWith(newFakeSession(), func() []TileView { return nil }, nil, LocalExtras{
		Network: on.Load, Keyboard: kb,
		TilesShown: func() { shown.Add(1) },
		TouchHit:   func(int) bool { hits.Add(1); return true },
	})
	for _, remote := range []string{"192.168.1.20:5555", "[2001:db8::5]:5555", "kaputt", ""} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, fromNet(http.MethodPost, "/api/keyboard/show", remote))
		if w.Code != http.StatusForbidden {
			t.Errorf("Tastatur von %q: %d", remote, w.Code)
		}
		req := fromNet(http.MethodPost, "/api/selftest/touch", remote)
		w = httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("Touch-Treffer von %q: %d", remote, w.Code)
		}
		w = httptest.NewRecorder()
		h.ServeHTTP(w, fromNet(http.MethodGet, "/", remote))
		if w.Code != http.StatusOK {
			t.Errorf("Kachelseite von %q: %d", remote, w.Code)
		}
	}
	if len(kb.got()) != 0 || shown.Load() != 0 || hits.Load() != 0 {
		t.Fatalf("aus dem Netz ausgelöst: Tastatur %v, Kachelseite %d, Touch %d", kb.got(), shown.Load(), hits.Load())
	}
	// Vom Terminal selbst (Loopback) geht es.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, fromNet(http.MethodPost, "/api/keyboard/show", "127.0.0.1:40000"))
	if w.Code != http.StatusNoContent {
		t.Fatalf("Loopback: %d", w.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, fromNet(http.MethodGet, "/", "[::1]:40000"))
	if shown.Load() != 1 {
		t.Fatal("Kachelseite über Loopback nicht gemeldet")
	}
}

func TestSecureLocalNeedsLoopbackPeer(t *testing.T) {
	var called atomic.Int32
	h := SecureLocal(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { called.Add(1) }))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, fromNet(http.MethodPost, "/api/selftest/run", "10.0.0.9:1234"))
	if w.Code != http.StatusForbidden || called.Load() != 0 {
		t.Fatalf("Selbstprüfung aus dem Netz: %d", w.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, fromNet(http.MethodPost, "/api/selftest/run", "127.0.0.1:1234"))
	if called.Load() != 1 {
		t.Fatalf("Loopback abgelehnt: %d", w.Code)
	}
}
