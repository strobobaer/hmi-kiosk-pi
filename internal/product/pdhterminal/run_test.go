package pdhterminal

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hmikiosk/internal/config"
)

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

// TestRunSmoke startet den ganzen Dienst ohne Chromium in temporären Ordnern.
func TestRunSmoke(t *testing.T) {
	data, run := t.TempDir(), t.TempDir()
	rootFS = t.TempDir()
	t.Cleanup(func() { rootFS = "/" })
	ready := make(chan Bound, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			DataDir: data, RunDir: run,
			LocalAddr: "127.0.0.1:0", AdminAddr: "127.0.0.1:0", LocalAdminAddr: freeAddr(t),
			HelperSocket: filepath.Join(t.TempDir(), "kein.sock"),
			HardwareID:   func() string { return "test-0001" },
			Ready:        func(b Bound) { ready <- b },
		})
	}()
	var b Bound
	select {
	case b = <-ready:
	case err := <-done:
		t.Fatalf("Dienst endete früh: %v", err)
	case <-time.After(20 * time.Second):
		t.Fatal("Dienst startet nicht")
	}

	get := func(c *http.Client, url string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, url, nil)
		if strings.HasPrefix(url, "http://") { // die Seiten am Terminal prüfen den Host: im Betrieb ist es 127.0.0.1:8080/8081
			req.Host = map[bool]string{true: "127.0.0.1:8080", false: "127.0.0.1:8081"}[strings.Contains(url, "/start")]
		}
		resp, err := c.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", url, err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}
	plain := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if code, body := get(plain, "http://"+b.Local+"/start"); code != 200 || !strings.Contains(body, "Gerät nicht gekoppelt") {
		t.Errorf("/start: %d %q", code, body)
	}
	tlsClient := &http.Client{Timeout: 5 * time.Second,
		Transport:     &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec // Test mit selbst ausgestelltem Zertifikat
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if code, _ := get(tlsClient, "https://"+b.Admin+"/admin/"); code != http.StatusSeeOther {
		t.Errorf("/admin/: Status %d", code)
	}
	if code, body := get(plain, "http://"+b.LocalAdmin+"/admin/"); code >= 500 {
		t.Errorf("Admin am Terminal: %d %q", code, body)
	}
	// Die Gesundmarke steht.
	if v, err := os.ReadFile(filepath.Join(run, "healthy")); err != nil || len(v) == 0 {
		t.Errorf("Gesundmarke: %q %v", v, err)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Ende: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Dienst endet nicht")
	}
}

func TestRunNeedsDirs(t *testing.T) {
	if err := Run(context.Background(), Options{}); err == nil {
		t.Fatal("ohne Ordner kein Start")
	}
}

func TestCornerConfig(t *testing.T) {
	c := cornerConfig(1280, 800)
	if c.ScreenW != 1280 || c.ScreenH != 800 || c.RegionPx != 80 || c.Hold != 5*time.Second {
		t.Errorf("%+v", c)
	}
}

// TestOriginsSentAtStartAndOnApply: Die PDH-Ursprünge gehen beim Start und nach jedem
// Speichern als set-web-origins an den Helfer.
func TestOriginsSentAtStartAndOnApply(t *testing.T) {
	data := t.TempDir()
	cfg := config.Default()
	cfg.PDH.URL = "https://pdh.firma.de"
	if err := config.Open(data).Save(cfg); err != nil {
		t.Fatal(err)
	}
	type call struct{ cmd, arg string }
	calls := make(chan call, 8)
	s, err := newService(Options{DataDir: data, RunDir: t.TempDir(), HardwareID: func() string { return "" },
		HelperCall: func(_ context.Context, cmd, arg string) error { calls <- call{cmd, arg}; return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.origins.Run(ctx)
	want := func(c config.Config) {
		t.Helper()
		b, _ := json.Marshal(config.PDHOrigins(c.PDH))
		select {
		case got := <-calls:
			if got.cmd != "set-web-origins" || got.arg != string(b) {
				t.Errorf("Aufruf %+v, erwartet %s", got, b)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("kein Aufruf, erwartet %s", b)
		}
	}
	want(cfg)
	cfg.PDH.URL = "https://andere.firma.de"
	s.apply(cfg)
	want(cfg)
	cfg.PDH.URL = ""
	s.apply(cfg)
	want(cfg) // leere Liste, solange keine Adresse eingetragen ist
}
