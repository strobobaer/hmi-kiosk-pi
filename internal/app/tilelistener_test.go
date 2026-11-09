package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"hmikiosk/internal/config"
)

func TestTileAddr(t *testing.T) {
	for _, c := range []struct {
		local   string
		network bool
		want    string
	}{
		{"127.0.0.1:8080", false, "127.0.0.1:8080"},
		{"127.0.0.1:8080", true, ":8080"},
		{"127.0.0.1:0", true, ":0"},
		{"[::1]:8080", true, ":8080"},
		{"localhost:8080", true, ":8080"},
	} {
		if got := tileAddr(c.local, c.network); got != c.want {
			t.Errorf("tileAddr(%q, %v) = %q, erwartet %q", c.local, c.network, got, c.want)
		}
	}
}

// freePort liefert einen gerade freien Port auf 127.0.0.1.
func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(l.Addr().String())
	l.Close()
	return port
}

// rawGet schickt eine Keep-alive-Anfrage über conn und liest die Antwort.
func rawGet(t *testing.T, br *bufio.Reader, conn net.Conn) string {
	t.Helper()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := fmt.Fprint(conn, "GET / HTTP/1.1\r\nHost: x\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func serveTileListener(t *testing.T, ln *tileListener) {
	t.Helper()
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ok") })}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
		if err := <-done; !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("Serve endet mit %v", err)
		}
	})
}

func httpGet(t *testing.T, addr string) error {
	t.Helper()
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get("http://" + addr + "/")
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// Derselbe Port: Linux bindet 0.0.0.0:8080 erst, wenn 127.0.0.1:8080 geschlossen ist.
// Offene Verbindungen (der Ereignisstrom von Chromium) bleiben dabei bestehen.
func TestTileListenerSwitchesSamePortLiveAndBack(t *testing.T) {
	port := freePort(t)
	local := "127.0.0.1:" + port
	ln, err := listenTiles(local)
	if err != nil {
		t.Fatal(err)
	}
	serveTileListener(t, ln)

	conn, err := net.Dial("tcp", local)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	br := bufio.NewReader(conn)
	if got := rawGet(t, br, conn); got != "ok" {
		t.Fatalf("vorher: %q", got)
	}

	changed, err := ln.Switch(tileAddr(local, true))
	if err != nil || !changed {
		t.Fatalf("Einschalten: %v %v", changed, err)
	}
	if a := ln.Addr().(*net.TCPAddr); !a.IP.IsUnspecified() || fmt.Sprint(a.Port) != port {
		t.Fatalf("Adresse nach dem Einschalten: %v", a)
	}
	if got := rawGet(t, br, conn); got != "ok" {
		t.Fatalf("offene Verbindung nach dem Umschalten: %q", got)
	}
	if err := httpGet(t, local); err != nil {
		t.Fatalf("lokal nach dem Einschalten: %v", err)
	}
	if changed, err := ln.Switch(":" + port); err != nil || changed {
		t.Fatalf("gleiche Adresse noch einmal: %v %v", changed, err)
	}

	if changed, err := ln.Switch(local); err != nil || !changed {
		t.Fatalf("Ausschalten: %v %v", changed, err)
	}
	if a := ln.Addr().(*net.TCPAddr); !a.IP.IsLoopback() {
		t.Fatalf("Adresse nach dem Ausschalten: %v", a)
	}
	if got := rawGet(t, br, conn); got != "ok" {
		t.Fatalf("offene Verbindung nach dem Zurückschalten: %q", got)
	}
	if err := httpGet(t, local); err != nil {
		t.Fatalf("lokal nach dem Ausschalten: %v", err)
	}
}

// Ist die neue Adresse belegt, gilt wieder die alte.
func TestTileListenerSwitchFailureKeepsOldAddress(t *testing.T) {
	port := freePort(t)
	local := "127.0.0.1:" + port
	blocker, err := net.Listen("tcp", "127.0.0.2:"+port)
	if err != nil {
		t.Skipf("127.0.0.2 nicht verfügbar: %v", err)
	}
	defer blocker.Close()
	ln, err := listenTiles(local)
	if err != nil {
		t.Fatal(err)
	}
	serveTileListener(t, ln)
	if _, err := ln.Switch(":" + port); err == nil {
		t.Fatal("belegter Port ohne Fehler")
	}
	if got := ln.Addr().String(); got != local {
		t.Fatalf("Adresse %q", got)
	}
	if err := httpGet(t, local); err != nil {
		t.Fatalf("alte Adresse nicht mehr erreichbar: %v", err)
	}
}

func TestServiceTileListenerFollowsConfig(t *testing.T) {
	f := newFixture(t)
	cfg := f.saveConfig(func(c *config.Config) { c.Kiosk.NetworkTiles = true })
	f.start()
	addr := f.s.tileAddrNow()
	if !addr.IP.IsUnspecified() {
		t.Fatalf("Start mit Freigabe: %v", addr)
	}
	if urls := f.s.tileURLs(); len(urls) == 0 || !strings.HasSuffix(urls[0], fmt.Sprintf(":%d/", addr.Port)) {
		t.Fatalf("Adressen: %v", urls)
	}
	cfg.Kiosk.NetworkTiles = false
	f.s.Apply(cfg)
	if addr := f.s.tileAddrNow(); !addr.IP.IsLoopback() {
		t.Fatalf("nach dem Ausschalten: %v", addr)
	}
	if urls := f.s.tileURLs(); urls != nil {
		t.Fatalf("Adressen ohne Freigabe: %v", urls)
	}
	cfg.Kiosk.NetworkTiles = true
	f.s.Apply(cfg)
	if addr := f.s.tileAddrNow(); !addr.IP.IsUnspecified() {
		t.Fatalf("nach dem Einschalten: %v", addr)
	}
	// Die Kachelseite antwortet über die neue Adresse, auch einem IP-Host aus dem Netz.
	req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/", f.s.tileAddrNow().Port), nil)
	req.Host = "192.168.1.20:8080"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("IP-Host bei Freigabe: %d", resp.StatusCode)
	}
}

// Ist der Port im Netz belegt, startet die Kachelseite lokal und meldet es.
func TestServiceStartFallsBackToLocalAddr(t *testing.T) {
	port := freePort(t)
	blocker, err := net.Listen("tcp", "127.0.0.2:"+port)
	if err != nil {
		t.Skipf("127.0.0.2 nicht verfügbar: %v", err)
	}
	defer blocker.Close()
	f := newFixture(t)
	f.saveConfig(func(c *config.Config) { c.Kiosk.NetworkTiles = true })
	f.startWith(func(s *service) { s.opts.LocalAddr = "127.0.0.1:" + port })
	if got := f.bound.Local; got != "127.0.0.1:"+port {
		t.Fatalf("Kachelseite auf %q", got)
	}
	found := false
	for _, w := range f.s.Status().Warnings {
		found = found || strings.Contains(w, "nicht im Netz")
	}
	if !found {
		t.Fatalf("keine Warnung: %v", f.s.Status().Warnings)
	}
}

func TestTileURLsFor(t *testing.T) {
	_, n1, _ := net.ParseCIDR("192.168.1.20/24")
	n1.IP = net.ParseIP("192.168.1.20")
	lo := &net.IPNet{IP: net.ParseIP("127.0.0.1"), Mask: net.CIDRMask(8, 32)}
	ll := &net.IPNet{IP: net.ParseIP("169.254.3.4"), Mask: net.CIDRMask(16, 32)}
	v6 := &net.IPNet{IP: net.ParseIP("fe80::1"), Mask: net.CIDRMask(64, 128)}
	got := tileURLsFor([]net.Addr{lo, n1, ll, v6}, 8080)
	if len(got) != 1 || got[0] != "http://192.168.1.20:8080/" {
		t.Fatalf("%v", got)
	}
}
