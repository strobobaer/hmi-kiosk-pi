package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func serveWithTouch(t *testing.T, hit func(int) bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(NewLocalWith(newFakeSession(), nil, nil, LocalExtras{TouchHit: hit}))
	t.Cleanup(srv.Close)
	return srv
}

func postJSON(t *testing.T, url, body string, hdr map[string]string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp
}

func TestSelftestTouchPageAndScript(t *testing.T) {
	srv := serveWithTouch(t, func(int) bool { return true })
	body, resp := get(t, srv.URL+"/selftest/touch")
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("Seite: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if n := strings.Count(body, `class="st-target`); n != 5 {
		t.Errorf("%d Ziele statt 5", n)
	}
	if strings.Contains(body, "<script>") || !strings.Contains(body, `src="/selftest.js"`) {
		t.Error("die Seite darf kein Inline-Skript haben (CSP script-src 'self')")
	}
	js, resp := get(t, srv.URL+"/selftest.js")
	if resp.StatusCode != http.StatusOK || !strings.Contains(js, "/api/selftest/touch") {
		t.Errorf("Skript: %d", resp.StatusCode)
	}
}

func TestSelftestTouchHitRules(t *testing.T) {
	var mu sync.Mutex
	var hits []int
	srv := serveWithTouch(t, func(i int) bool {
		mu.Lock()
		defer mu.Unlock()
		if i < 0 || i > 4 {
			return false
		}
		hits = append(hits, i)
		return true
	})
	url := srv.URL + "/api/selftest/touch"
	if r := postJSON(t, url, `{"target":3}`, okOrigin); r.StatusCode != http.StatusNoContent {
		t.Errorf("gültiger Treffer: %d", r.StatusCode)
	}
	for name, tc := range map[string]struct {
		body string
		hdr  map[string]string
		want int
	}{
		"ohne Origin":     {`{"target":1}`, nil, http.StatusForbidden},
		"fremder Origin":  {`{"target":1}`, map[string]string{"Origin": "http://evil.example"}, http.StatusForbidden},
		"ungültiges Ziel": {`{"target":9}`, okOrigin, http.StatusBadRequest},
		"kein Ziel":       {`{}`, okOrigin, http.StatusBadRequest},
		"kein JSON":       {`x`, okOrigin, http.StatusBadRequest},
	} {
		if r := postJSON(t, url, tc.body, tc.hdr); r.StatusCode != tc.want {
			t.Errorf("%s: %d, erwartet %d", name, r.StatusCode, tc.want)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(hits) != 1 || hits[0] != 3 {
		t.Errorf("Treffer: %v", hits)
	}
}

func TestSelftestTouchRejectsForeignHost(t *testing.T) {
	srv := serveWithTouch(t, func(int) bool { return true })
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/selftest/touch", nil)
	req.Host = "evil.example:8080"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("Status %d", resp.StatusCode)
	}
}

func TestSelftestTouchWithoutSelftestIsRefused(t *testing.T) {
	srv := serve(t, newFakeSession(), nil, "auto")
	if r := postJSON(t, srv.URL+"/api/selftest/touch", `{"target":1}`, okOrigin); r.StatusCode != http.StatusNotFound {
		t.Fatalf("Status %d", r.StatusCode)
	}
}

func TestSelftestResultCanBeDownloaded(t *testing.T) {
	e, b := newLoggedIn(t)
	if r := b.get("/admin/system/selftest.txt"); r.Code != http.StatusNotFound {
		t.Fatalf("vor der ersten Prüfung: %d", r.Code)
	}
	e.selftest = func(context.Context) []SelftestStep {
		return []SelftestStep{
			{Name: "client-bild", OK: true, NeedsHuman: true, Detail: "Bitte Rand prüfen"},
			{Name: "touch", OK: false, Detail: "2 von 5"},
			{Name: "terminals", OK: true, Detail: "keine Terminals eingetragen"},
		}
	}
	body := b.post("/admin/system/selftest", nil).Body.String()
	wantContains(t, body, "Prüfen: client-bild", "Fehler: touch", "OK: terminals", "/admin/system/selftest.txt")
	r := b.get("/admin/system/selftest.txt")
	if r.Code != http.StatusOK || !strings.Contains(r.Header().Get("Content-Disposition"), "selbstpruefung.txt") {
		t.Fatalf("Download: %d %v", r.Code, r.Header())
	}
	wantContains(t, r.Body.String(), "[PRÜFEN] client-bild: Bitte Rand prüfen", "[FEHLER] touch: 2 von 5", "[OK] terminals")
}

func TestLogViewIgnoresSoakFile(t *testing.T) {
	e, b := newLoggedIn(t)
	os.WriteFile(filepath.Join(e.logDir, "kiosk.log"), []byte("dienstzeile\n"), 0o600)
	past := time.Now().Add(-time.Hour)
	os.Chtimes(filepath.Join(e.logDir, "kiosk.log"), past, past)
	os.WriteFile(filepath.Join(e.logDir, "soak.jsonl"), []byte(`{"cycle":1}`+"\n"), 0o600)
	body := b.get("/admin/system").Body.String()
	wantContains(t, body, "dienstzeile")
	wantNotContains(t, body, `{&#34;cycle&#34;`, "cycle")
}
