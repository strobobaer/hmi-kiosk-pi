package slides

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const testOrigin = "https://pdh.example"

func cacheReq(h http.Handler, method, origin string, body []byte) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/api/slides/cache", bytes.NewReader(body))
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func newCacheHandler(st *Store, changed *atomic.Int32) http.Handler {
	return CacheHandler(st, func() string { return testOrigin }, func() { changed.Add(1) })
}

func TestCachePostAndDelete(t *testing.T) {
	st := newStore(t)
	var changed atomic.Int32
	h := newCacheHandler(st, &changed)

	w := cacheReq(h, http.MethodPost, testOrigin, payloadJSON(t, "v1", map[string][]byte{"s-img": pngBytes(t)}))
	if w.Code != http.StatusNoContent {
		t.Fatalf("POST: %d %s", w.Code, w.Body)
	}
	if w.Header().Get("Access-Control-Allow-Origin") != testOrigin || w.Header().Get("Vary") != "Origin" {
		t.Errorf("CORS-Kopf fehlt: %v", w.Header())
	}
	if !st.Present() || changed.Load() != 1 {
		t.Fatalf("Present %v, changed %d", st.Present(), changed.Load())
	}
	if w := cacheReq(h, http.MethodDelete, testOrigin, nil); w.Code != http.StatusNoContent {
		t.Fatalf("DELETE: %d", w.Code)
	}
	if st.Present() || changed.Load() != 2 {
		t.Fatalf("nach DELETE: Present %v, changed %d", st.Present(), changed.Load())
	}
	// DELETE ohne Zwischenspeicher: 204
	if w := cacheReq(h, http.MethodDelete, testOrigin, nil); w.Code != http.StatusNoContent {
		t.Fatalf("DELETE leer: %d", w.Code)
	}
}

func TestCacheRejectsAndKeepsOld(t *testing.T) {
	st := newStore(t)
	var changed atomic.Int32
	h := newCacheHandler(st, &changed)
	if w := cacheReq(h, http.MethodPost, testOrigin, payloadJSON(t, "alt", nil)); w.Code != 204 {
		t.Fatal(w.Code)
	}
	changed.Store(0)

	if w := cacheReq(h, http.MethodPost, testOrigin, []byte(`{"version":`)); w.Code != http.StatusBadRequest {
		t.Errorf("kaputtes JSON: %d", w.Code)
	}
	big := payloadJSON(t, "neu", map[string][]byte{"s-img": padPNG(t, MaxImage+1)})
	if w := cacheReq(h, http.MethodPost, testOrigin, big); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("Bild > 5 MB: %d", w.Code)
	}
	// über 60 MB: ohne Content-Length (Chunked) liest MaxBytesReader nur bis zur Grenze
	huge := bytes.Repeat([]byte("a"), MaxBody+1)
	r := httptest.NewRequest(http.MethodPost, "/api/slides/cache", bytes.NewReader(huge))
	r.ContentLength = -1
	r.Header.Set("Origin", testOrigin)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("60 MB chunked: %d", w.Code)
	}
	if w := cacheReq(h, http.MethodPost, testOrigin, huge); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("60 MB: %d", w.Code)
	}
	if changed.Load() != 0 {
		t.Errorf("changed nach Ablehnung: %d", changed.Load())
	}
	mustVersion(t, st, "alt")
}

func TestCacheOriginAndCORS(t *testing.T) {
	st := newStore(t)
	var changed atomic.Int32
	h := newCacheHandler(st, &changed)
	for _, c := range []struct{ name, origin string }{
		{"kein Origin", ""}, {"fremd", "http://evil.example"}, {"anderer Port", "https://pdh.example:8443"},
	} {
		for _, m := range []string{http.MethodPost, http.MethodDelete, http.MethodOptions} {
			w := cacheReq(h, m, c.origin, payloadJSON(t, "x", nil))
			if w.Code != http.StatusForbidden || w.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Errorf("%s %s: %d %v", c.name, m, w.Code, w.Header())
			}
		}
	}
	if st.Present() || changed.Load() != 0 {
		t.Error("fremder Ursprung hat gespeichert")
	}
	// nicht konfiguriert
	h2 := CacheHandler(st, func() string { return "" }, nil)
	if w := cacheReq(h2, http.MethodPost, testOrigin, nil); w.Code != http.StatusForbidden {
		t.Errorf("nicht konfiguriert: %d", w.Code)
	}
	// Preflight
	r := httptest.NewRequest(http.MethodOptions, "/api/slides/cache", nil)
	r.Header.Set("Origin", testOrigin)
	r.Header.Set("Access-Control-Request-Private-Network", "true")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	hd := w.Header()
	if w.Code != http.StatusNoContent || hd.Get("Access-Control-Allow-Origin") != testOrigin ||
		hd.Get("Access-Control-Allow-Methods") != "POST, DELETE" || hd.Get("Access-Control-Allow-Headers") != "Content-Type" ||
		hd.Get("Access-Control-Allow-Private-Network") != "true" {
		t.Errorf("Preflight: %d %v", w.Code, hd)
	}
	r = httptest.NewRequest(http.MethodOptions, "/api/slides/cache", nil)
	r.Header.Set("Origin", testOrigin)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Header().Get("Access-Control-Allow-Private-Network") != "" {
		t.Error("Private-Network ohne Anfrage")
	}
	if w := cacheReq(h, http.MethodGet, testOrigin, nil); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", w.Code)
	}
}

func offlineMux(st *Store, online bool) http.Handler {
	mux := http.NewServeMux()
	Register(mux, st, func(context.Context) bool { return online })
	return mux
}

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

func TestOfflinePages(t *testing.T) {
	st := newStore(t)
	h := offlineMux(st, false)
	// ohne Zwischenspeicher: zurück zur Startseite
	if w := get(h, "/slides"); w.Code != http.StatusFound || w.Header().Get("Location") != "/start" {
		t.Errorf("ohne Zwischenspeicher: %d %v", w.Code, w.Header())
	}
	if w := get(h, "/slides/data.json"); w.Code != http.StatusNotFound {
		t.Errorf("data.json ohne: %d", w.Code)
	}
	png := pngBytes(t)
	if err := st.Save(payloadJSON(t, "v1", map[string][]byte{"s-img": png})); err != nil {
		t.Fatal(err)
	}
	w := get(h, "/slides")
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, "Server offline") || !strings.Contains(body, "nextSlideIndex") ||
		!strings.Contains(body, "slideDurationMs") || !strings.Contains(body, "isSwipeLeft") {
		t.Fatalf("/slides: %d", w.Code)
	}
	if strings.Contains(body, "innerHTML") || strings.Contains(body, "data-kind") || strings.Contains(body, "location.href") {
		t.Error("Seite darf kein innerHTML, keine antippbaren Vorgänge, keine Verweise enthalten")
	}
	if !strings.Contains(body, "Stand ") {
		t.Error("Vermerk Stand fehlt")
	}
	csp := w.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'none'") || strings.Contains(csp, "http") {
		t.Errorf("CSP: %q", csp)
	}
	w = get(h, "/slides/data.json")
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), "application/json") ||
		!strings.Contains(w.Body.String(), `"/slides/img/s-img"`) || !strings.Contains(w.Body.String(), `"saved_at":"2026-11-21T10:30:00Z"`) {
		t.Errorf("data.json: %d %s", w.Code, w.Body)
	}
	w = get(h, "/slides/img/s-img")
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/png" || w.Header().Get("X-Content-Type-Options") != "nosniff" ||
		!bytes.Equal(w.Body.Bytes(), png) {
		t.Errorf("Bild: %d %v", w.Code, w.Header())
	}
	if w := get(h, "/slides/img/fehlt"); w.Code != http.StatusNotFound {
		t.Errorf("Bild fehlt: %d", w.Code)
	}
	if w := get(h, "/slides/online"); !strings.Contains(w.Body.String(), `"online":false`) {
		t.Errorf("online: %s", w.Body)
	}
	if w := get(offlineMux(st, true), "/slides/online"); !strings.Contains(w.Body.String(), `"online":true`) {
		t.Errorf("online: %s", w.Body)
	}
}
