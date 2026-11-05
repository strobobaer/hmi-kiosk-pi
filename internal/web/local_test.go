package web

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"hmikiosk/internal/coord"
	"hmikiosk/internal/session"
)

type fakeSession struct {
	mu      sync.Mutex
	calls   []string
	openErr error
	snap    session.Snapshot
	ch      chan session.Snapshot
}

func newFakeSession() *fakeSession {
	return &fakeSession{ch: make(chan session.Snapshot, 4), snap: session.Snapshot{State: session.StateTiles}}
}

func (f *fakeSession) rec(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, s)
}

func (f *fakeSession) got() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeSession) Open(id string) error { f.rec("open:" + id); return f.openErr }
func (f *fakeSession) Confirm() error       { f.rec("confirm"); return nil }
func (f *fakeSession) Cancel()              { f.rec("cancel") }
func (f *fakeSession) Back()                { f.rec("back") }
func (f *fakeSession) Snapshot() session.Snapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.snap
}
func (f *fakeSession) Subscribe() (<-chan session.Snapshot, func()) {
	f.ch <- f.Snapshot()
	return f.ch, func() {}
}

func serve(t *testing.T, s SessionAPI, tiles []TileView, layout string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(NewLocal(s, func() []TileView { return tiles }, func() string { return layout }))
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, url string) (string, *http.Response) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Host = "127.0.0.1:8080"
	resp, err := http.DefaultClient.Do(req)
	req.Host = "127.0.0.1:8080"
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var b strings.Builder
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		b.WriteString(sc.Text() + "\n")
	}
	return b.String(), resp
}

func post(t *testing.T, url string, hdr map[string]string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, nil)
	req.Host = "127.0.0.1:8080"
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

var okOrigin = map[string]string{"Origin": "http://127.0.0.1:8080"}

func TestTilesNeverContainHostOrPassword(t *testing.T) {
	// TileView hat keine Felder dafür; die Seite darf auch sonst nichts davon tragen.
	srv := serve(t, newFakeSession(), []TileView{{ID: "a", Name: "Presse 1", Icon: "press", Color: "blue", Reachable: true}}, "auto")
	body, _ := get(t, srv.URL+"/")
	for _, bad := range []string{"10.0.0.", "5900", "geheim", "Passwort", "password"} {
		if strings.Contains(strings.ToLower(body), strings.ToLower(bad)) {
			t.Fatalf("Seite enthält %q:\n%s", bad, body)
		}
	}
}

func TestTilesRenderOneButtonPerTile(t *testing.T) {
	var tiles []TileView
	for i := 0; i < 12; i++ {
		tiles = append(tiles, TileView{ID: fmt.Sprintf("hmi%d", i), Name: fmt.Sprintf("HMI %d", i), Icon: "gear", Color: "green", Reachable: i%2 == 0, Disabled: i == 5})
	}
	srv := serve(t, newFakeSession(), tiles, "3")
	body, _ := get(t, srv.URL+"/")
	if n := len(regexp.MustCompile(`<button[^>]* data-id="hmi\d+"`).FindAllString(body, -1)); n != 12 {
		t.Fatalf("%d Kacheln statt 12:\n%s", n, body)
	}
	if !strings.Contains(body, "cols-3") {
		t.Fatal("Spaltenzahl fehlt")
	}
	if n := strings.Count(body, "nicht erreichbar"); n != 6 {
		t.Fatalf("%d Kacheln nicht erreichbar markiert, erwartet 6", n)
	}
	if !regexp.MustCompile(`data-id="hmi5" disabled`).MatchString(body) {
		t.Fatal("deaktivierte Kachel ohne disabled")
	}
}

func TestTileBusyAndFallbacks(t *testing.T) {
	srv := serve(t, newFakeSession(), []TileView{
		{ID: "a", Name: "A", Icon: "unbekannt", Color: "pink", Reachable: true, BusyBy: "Terminal 2"},
		{ID: "b", Name: "B", Icon: "tank", Color: `red" onclick="x`, Reachable: true},
	}, "")
	body, _ := get(t, srv.URL+"/")
	if !strings.Contains(body, "belegt durch Terminal 2") {
		t.Fatal("belegt durch fehlt")
	}
	if !strings.Contains(body, "tile c-gray") || strings.Contains(body, "pink") || strings.Contains(body, "onclick") {
		t.Fatalf("Farbe nicht auf feste Menge begrenzt:\n%s", body)
	}
	if !strings.Contains(body, "cols-auto") {
		t.Fatal("Layout ohne Wert nicht auto")
	}
}

func TestNameIsEscapedInHTML(t *testing.T) {
	srv := serve(t, newFakeSession(), []TileView{{ID: "a", Name: `<script>alert(1)</script>`, Reachable: true, BusyBy: `<b>x</b>`}}, "auto")
	body, _ := get(t, srv.URL+"/")
	if strings.Contains(body, "<script>alert") || strings.Contains(body, "<b>x") {
		t.Fatalf("Name nicht maskiert:\n%s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Fatal("maskierter Name fehlt")
	}
}

func TestOpenCallsSession(t *testing.T) {
	fs := newFakeSession()
	srv := serve(t, fs, nil, "auto")
	if r := post(t, srv.URL+"/api/hmi/presse1/open", okOrigin); r.StatusCode != http.StatusNoContent {
		t.Fatalf("Status %d", r.StatusCode)
	}
	post(t, srv.URL+"/api/confirm", okOrigin)
	post(t, srv.URL+"/api/cancel", okOrigin)
	post(t, srv.URL+"/api/back", map[string]string{"Referer": "http://localhost:8080/bar"})
	want := []string{"open:presse1", "confirm", "cancel", "back"}
	if got := fs.got(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Aufrufe %v", got)
	}
}

func TestOpenErrorIsConflictWithGermanJSON(t *testing.T) {
	fs := newFakeSession()
	fs.openErr = errors.New("HMI \"x\" ist nicht bekannt")
	srv := serve(t, fs, nil, "auto")
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/hmi/x/open", nil)
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Origin", "http://127.0.0.1:8080")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var sb strings.Builder
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		sb.WriteString(sc.Text())
	}
	if resp.StatusCode != http.StatusConflict || !strings.Contains(sb.String(), `"error"`) || !strings.Contains(sb.String(), "nicht bekannt") {
		t.Fatalf("%d %s", resp.StatusCode, sb.String())
	}
}

func TestPostRejectedWithForeignOrigin(t *testing.T) {
	fs := newFakeSession()
	srv := serve(t, fs, nil, "auto")
	cases := []map[string]string{
		nil,
		{"Origin": "http://evil.example"},
		{"Origin": "http://127.0.0.1:9999"},
		{"Origin": "null"},
		{"Referer": "http://127.0.0.1:8080.evil.example/"},
		{"Referer": "https://127.0.0.1:8080/"},
		{"Origin": "http://evil.example", "Referer": "http://127.0.0.1:8080/"},
	}
	for _, h := range cases {
		if r := post(t, srv.URL+"/api/hmi/a/open", h); r.StatusCode != http.StatusForbidden {
			t.Errorf("Kopfzeilen %v: Status %d", h, r.StatusCode)
		}
		if r := post(t, srv.URL+"/api/back", h); r.StatusCode != http.StatusForbidden {
			t.Errorf("Kopfzeilen %v: back Status %d", h, r.StatusCode)
		}
	}
	if got := fs.got(); len(got) != 0 {
		t.Fatalf("Sitzung trotz Ablehnung aufgerufen: %v", got)
	}
}

func TestEventsStreamsSnapshot(t *testing.T) {
	fs := newFakeSession()
	fs.snap = session.Snapshot{
		State: session.StateConfirming, HMI: "a", HMIName: "Presse 1",
		Confirm: &coord.Holder{Name: "Terminal 2"}, BadKey: []string{"T3"}, Unreachable: []string{"T4"},
	}
	srv := serve(t, fs, nil, "auto")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/events", nil)
	req.Host = "127.0.0.1:8080"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type %q", ct)
	}
	sc := bufio.NewScanner(resp.Body)
	if !sc.Scan() {
		t.Fatalf("keine Daten: %v", sc.Err())
	}
	line := sc.Text()
	for _, part := range []string{`data: {`, `"state":"confirming"`, `"hmiName":"Presse 1"`, `"confirmName":"Terminal 2"`, `"badKey":["T3"]`, `"unreachable":["T4"]`} {
		if !strings.Contains(line, part) {
			t.Fatalf("%q fehlt in %s", part, line)
		}
	}
}

func TestEventsHeartbeat(t *testing.T) {
	old := heartbeat
	heartbeat = 20 * time.Millisecond
	t.Cleanup(func() { heartbeat = old })
	srv := serve(t, newFakeSession(), nil, "auto")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/events", nil)
	req.Host = "127.0.0.1:8080"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), ":") {
			return
		}
	}
	t.Fatal("kein Heartbeat")
}

func TestEventsEndWithContext(t *testing.T) {
	h := NewLocal(newFakeSession(), nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/api/events", nil).WithContext(ctx))
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Strom endet nicht")
	}
}

func TestBarShowsNameModeAndWarning(t *testing.T) {
	fs := newFakeSession()
	fs.snap = session.Snapshot{State: session.StateConnected, HMI: "a", HMIName: "Presse <1>"}
	srv := serve(t, fs, nil, "auto")
	body, _ := get(t, srv.URL+"/bar")
	for _, part := range []string{"Presse &lt;1&gt;", "Bedienen", "Zurück"} {
		if !strings.Contains(body, part) {
			t.Fatalf("%q fehlt:\n%s", part, body)
		}
	}
	if strings.Contains(body, "Nur ansehen") {
		t.Fatal("falscher Modus")
	}
	fs.mu.Lock()
	fs.snap = session.Snapshot{State: session.StateWarned, HMIName: "P", ViewOnly: true, Notice: "Übernahme von T2 läuft"}
	fs.mu.Unlock()
	body, _ = get(t, srv.URL+"/bar")
	if !strings.Contains(body, "Nur ansehen") || !strings.Contains(body, "Übernahme von T2 läuft") || strings.Contains(body, "bar-notice\" hidden") {
		t.Fatalf("Warnung fehlt:\n%s", body)
	}
}

func TestAssetsServedWithSecurityHeaders(t *testing.T) {
	srv := serve(t, newFakeSession(), nil, "auto")
	for path, ctype := range map[string]string{"/kiosk.css": "text/css", "/kiosk.js": "text/javascript"} {
		_, resp := get(t, srv.URL+path)
		if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), ctype) {
			t.Errorf("%s: %d %s", path, resp.StatusCode, resp.Header.Get("Content-Type"))
		}
		if resp.Header.Get("Content-Security-Policy") == "" || resp.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: Schutzkopfzeilen fehlen", path)
		}
	}
}

func TestAssetsHaveNoExternalURLs(t *testing.T) {
	re := regexp.MustCompile(`(?i)(https?:)?//[a-z0-9.\-\[]+`)
	n := 0
	err := fs.WalkDir(assets, "assets", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		n++
		b, err := fs.ReadFile(assets, p)
		if err != nil {
			return err
		}
		for _, m := range re.FindAllString(string(b), -1) {
			if !strings.Contains(m, "127.0.0.1") {
				t.Errorf("%s: externe Adresse %q", p, m)
			}
		}
		return nil
	})
	if err != nil || n < 4 {
		t.Fatalf("Dateien %d, Fehler %v", n, err)
	}
}

func TestAssetsHaveNoInlineScriptOrStyle(t *testing.T) {
	for _, name := range []string{"assets/tiles.html", "assets/bar.html"} {
		b, _ := assets.ReadFile(name)
		s := string(b)
		if regexp.MustCompile(`<script[^>]*>[^<]`).MatchString(s) || strings.Contains(s, " style=") || regexp.MustCompile(` on[a-z]+=`).MatchString(s) {
			t.Errorf("%s enthält Inline-Skript oder -Stil", name)
		}
	}
}

func TestForeignHostRejectedEverywhere(t *testing.T) {
	fs := newFakeSession()
	srv := serve(t, fs, nil, "auto")
	for _, host := range []string{"evil.example", "evil.example:8080", "127.0.0.1", "127.0.0.1:9999", ""} {
		for _, c := range []struct{ method, path string }{
			{http.MethodGet, "/"}, {http.MethodGet, "/bar"}, {http.MethodGet, "/api/events"}, {http.MethodPost, "/api/back"},
		} {
			req, _ := http.NewRequest(c.method, srv.URL+c.path, nil)
			req.Host = host
			req.Header.Set("Origin", "http://127.0.0.1:8080")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("Host %q %s %s: Status %d", host, c.method, c.path, resp.StatusCode)
			}
		}
	}
	if got := fs.got(); len(got) != 0 {
		t.Fatalf("Sitzung aufgerufen: %v", got)
	}
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	req.Host = "localhost:8080"
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != 200 {
		t.Fatalf("localhost:8080 abgelehnt: %v", err)
	}
}
