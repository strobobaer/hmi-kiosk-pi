package app

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"hmikiosk/internal/client"
	"hmikiosk/internal/config"
	"hmikiosk/internal/coord"
	"hmikiosk/internal/release"
	"hmikiosk/internal/session"
)

func TestApplyChangesTilesAndKeyForNewRequests(t *testing.T) {
	f := newFixture(t)
	oldKey, newKey := "alter-gemeinsamer-schluessel", "neuer-gemeinsamer-schluessel"
	cfg := f.saveConfig(func(c *config.Config) {
		oneHMI(c)
		c.SharedKeyEnc = f.seal(oldKey)
		c.Peers = []config.Peer{{Name: "T2", Address: "127.0.0.1:1"}}
	})
	f.start()
	peer := config.Peer{Name: "ich", Address: f.bound.Admin}
	req := coord.ClaimReq{HMI: "presse", Claim: coord.Claim{Terminal: "x", Role: config.RoleNormal}}
	ctx := context.Background()
	oldTr := coord.NewHTTPTransport([]byte(oldKey), nil)
	newTr := coord.NewHTTPTransport([]byte(newKey), nil)
	if _, err := oldTr.Claim(ctx, peer, req); err != nil {
		t.Fatal(err)
	}
	if _, err := newTr.Claim(ctx, peer, req); !errors.Is(err, coord.ErrBadKey) {
		t.Fatalf("neuer Schlüssel vor Apply: %v", err)
	}

	cfg.SharedKeyEnc = f.seal(newKey)
	cfg.HMIs[0].Name = "Presse 8"
	f.s.Apply(cfg)
	if _, err := newTr.Claim(ctx, peer, req); err != nil {
		t.Fatalf("neuer Schlüssel: %v", err)
	}
	if _, err := oldTr.Claim(ctx, peer, req); !errors.Is(err, coord.ErrBadKey) {
		t.Fatalf("alter Schlüssel nach Apply: %v", err)
	}
	if _, body := f.local("GET", "/"); !strings.Contains(body, "Presse 8") {
		t.Fatalf("Kachel: %s", body)
	}
}

func TestApplyStoresCopyOfConfig(t *testing.T) {
	f := newFixture(t)
	cfg := f.saveConfig(oneHMI)
	f.start()
	f.s.Apply(cfg)
	cfg.HMIs[0].Name = "von außen geändert"
	if got := f.s.config().HMIs[0].Name; got != "Presse 7" {
		t.Fatalf("Name %q", got)
	}
}

func TestApplyClearsConfigWarningAfterSave(t *testing.T) {
	f := newFixture(t)
	cfg := f.saveConfig(func(c *config.Config) {
		oneHMI(c)
		c.Admin.PasswordHash = "hash"
	})
	if err := config.Open(f.data).MarkGood(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.data, "config.json"), []byte("{kaputt"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.start()
	f.s.Apply(cfg)
	if w := f.bound.Status().Warnings; hasWarning(w, "Rückfall") {
		t.Fatalf("Warnung bleibt nach neuem Stand: %v", w)
	}
}

func TestMissingSharedKeyMeansNoPeersWithWarning(t *testing.T) {
	f := newFixture(t)
	cfg := f.saveConfig(func(c *config.Config) {
		oneHMI(c)
		c.Peers = []config.Peer{{Name: "T2", Address: "127.0.0.1:1"}}
	})
	f.start()
	if w := f.bound.Status().Warnings; !hasWarning(w, "gemeinsamer Schlüssel") {
		t.Fatalf("Warnungen %v", w)
	}
	res := f.s.coord.Request(context.Background(), "presse")
	f.s.coord.Release("presse")
	if len(res.Unreachable) != 0 || len(res.BadKey) != 0 {
		t.Fatalf("Terminal gefragt: %+v", res)
	}

	cfg.SharedKeyEnc = f.seal("jetzt-gibt-es-einen-schluessel")
	f.s.Apply(cfg)
	if w := f.bound.Status().Warnings; hasWarning(w, "gemeinsamer Schlüssel") {
		t.Fatalf("Warnung bleibt: %v", w)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res = f.s.coord.Request(ctx, "presse")
	f.s.coord.Release("presse")
	if len(res.Unreachable) != 1 {
		t.Fatalf("Terminal aus der neuen Konfiguration nicht gefragt: %+v", res)
	}
}

func TestUnreadableSharedKeyMeansNoPeersWithWarning(t *testing.T) {
	f := newFixture(t)
	f.saveConfig(func(c *config.Config) {
		oneHMI(c)
		c.SharedKeyEnc = "unlesbar"
		c.Peers = []config.Peer{{Name: "T2", Address: "127.0.0.1:1"}}
	})
	f.start()
	if w := f.bound.Status().Warnings; !hasWarning(w, "gemeinsamer Schlüssel") {
		t.Fatalf("Warnungen %v", w)
	}
	res := f.s.coord.Request(context.Background(), "presse")
	f.s.coord.Release("presse")
	if len(res.Unreachable) != 0 || len(res.BadKey) != 0 {
		t.Fatalf("Terminal gefragt: %+v", res)
	}
}

func TestPermanentAndRulesReadCurrentConfig(t *testing.T) {
	f := newFixture(t)
	cfg := f.saveConfig(oneHMI)
	f.start()
	s := f.s
	if s.isPermanent("presse") {
		t.Fatal("Daueranzeige ohne Einstellung")
	}
	if r := s.rulesFor("presse"); r.Mode != config.ModeTakeover {
		t.Fatalf("Regeln %+v", r)
	}
	lock := config.ModeLock
	cfg.HMIs[0].Mode = &lock
	cfg.HMIs[0].Takeover = config.StyleImmediate
	cfg.HMIs[0].AllowWatch = true
	cfg.Kiosk.PermanentHMI = "presse"
	s.Apply(cfg)
	if !s.isPermanent("presse") {
		t.Fatal("Daueranzeige nicht übernommen")
	}
	r := s.rulesFor("presse")
	if r.Mode != config.ModeLock || r.Style != config.StyleImmediate || !r.AllowWatch {
		t.Fatalf("Regeln %+v", r)
	}
	if r := s.rulesFor("unbekannt"); r.Mode != cfg.DefaultMode {
		t.Fatalf("Regeln %+v", r)
	}
}

func TestTilesShowReachabilityAndOrder(t *testing.T) {
	port := listenVNC(t, "")
	f := newFixture(t)
	f.saveConfig(func(c *config.Config) {
		c.HMIs = []config.HMI{
			{ID: "zwei", Name: "Zweite", Host: "127.0.0.1", Port: 1, Order: 2, Reach: config.ReachPort},
			{ID: "eins", Name: "Erste", Host: "127.0.0.1", Port: port, Order: 1, Reach: config.ReachPort},
		}
	})
	f.start()
	eventually(t, "Erreichbarkeit", func() bool {
		tiles := f.s.tiles()
		return len(tiles) == 2 && tiles[0].ID == "eins" && tiles[0].Reachable && !tiles[1].Reachable
	})
	for _, tile := range f.s.tiles() {
		if tile.Disabled || tile.BusyBy != "" {
			t.Fatalf("Kachel %+v", tile)
		}
	}
}

func TestSessionWritesJournalAndCleansRAMDir(t *testing.T) {
	port := listenVNC(t, "geheim")
	f := newFixture(t)
	f.saveConfig(func(c *config.Config) {
		c.HMIs = []config.HMI{{ID: "presse", Name: "Presse 7", Host: "127.0.0.1", Port: port, PasswordEnc: f.seal("geheim")}}
	})
	f.start()
	if code, _ := f.local("POST", "/api/hmi/presse/open"); code != http.StatusNoContent {
		t.Fatalf("Open %d", code)
	}
	eventually(t, "Client", func() bool { return len(f.runner.started()) == 1 })

	sessions := filepath.Join(f.run, "sessions")
	entries, err := os.ReadDir(sessions)
	if err != nil || len(entries) != 1 {
		t.Fatalf("Sitzungsordner %v, %v", entries, err)
	}
	info, _ := entries[0].Info()
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("Rechte %v", info.Mode())
	}

	if code, _ := f.local("POST", "/api/back"); code != http.StatusNoContent {
		t.Fatalf("Back %d", code)
	}
	eventually(t, "Kacheln", func() bool { return f.bound.Snapshot().State == session.StateTiles })
	eventually(t, "leerer Sitzungsordner", func() bool {
		entries, err := os.ReadDir(sessions)
		return err == nil && len(entries) == 0
	})
	f.stop()
	b, err := os.ReadFile(filepath.Join(f.data, "log", "kiosk.log"))
	if err != nil {
		t.Fatal(err)
	}
	line := ""
	for _, l := range strings.Split(string(b), "\n") {
		if strings.Contains(l, "Sitzungsende") {
			line = l
		}
	}
	for _, want := range []string{`"T1"`, `"Presse 7"`, `"Bedienen"`, "Zurück"} {
		if !strings.Contains(line, want) {
			t.Errorf("%q fehlt in %q", want, line)
		}
	}
}

func TestWrongVNCPasswordStartsNoClient(t *testing.T) {
	port := listenVNC(t, "geheim")
	f := newFixture(t)
	f.saveConfig(func(c *config.Config) {
		c.HMIs = []config.HMI{{ID: "presse", Name: "Presse", Host: "127.0.0.1", Port: port, PasswordEnc: f.seal("falsch")}}
	})
	f.start()
	f.local("POST", "/api/hmi/presse/open")
	eventually(t, "Hinweis", func() bool { return strings.Contains(f.bound.Snapshot().Notice, "Passwort") })
	if n := len(f.runner.started()); n != 0 {
		t.Fatalf("%d Clients gestartet", n)
	}
}

func TestAdminProbeDecryptsAndHonoursLimit(t *testing.T) {
	port := listenVNC(t, "geheim")
	f := newFixture(t)
	f.saveConfig(nil)
	f.start()
	h := config.HMI{Host: "127.0.0.1", Port: port, PasswordEnc: f.seal("geheim")}
	if res := f.s.adminProbe(context.Background(), h); !res.AuthOK {
		t.Fatalf("Ergebnis %+v", res)
	}
	h.PasswordEnc = "kein gültiger Wert"
	if res := f.s.adminProbe(context.Background(), h); res.Err == "" || res.AuthOK {
		t.Fatalf("Ergebnis %+v", res)
	}
	// Ein Server, der nie antwortet: Die Frist des Aufrufers gilt.
	mute, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer mute.Close()
	h = config.HMI{Host: "127.0.0.1", Port: mute.Addr().(*net.TCPAddr).Port}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	f.s.adminProbe(ctx, h)
	if time.Since(start) > 5*time.Second {
		t.Fatal("Frist nicht beachtet")
	}
}

func TestPeerAndAdminShareTLSPort(t *testing.T) {
	f := newFixture(t)
	f.saveConfig(nil)
	f.start()
	c := insecureClient()
	resp, err := c.Post("https://"+f.bound.Admin+"/peer/nonce", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("Terminal-API %d", resp.StatusCode)
	}
	resp, err = c.Get("https://" + f.bound.Admin + "/admin/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || !strings.Contains(resp.Header.Get("Location"), "/admin/setup") {
		t.Fatalf("Admin %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
}

func TestAdminPortRootLeadsToAdminPage(t *testing.T) {
	f := newFixture(t)
	f.saveConfig(nil)
	f.start()
	c := insecureClient()
	resp, err := c.Get("https://" + f.bound.Admin + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/admin/" {
		t.Fatalf("Wurzel %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, err = c.Get("https://" + f.bound.Admin + "/gibt-es-nicht")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(string(body), "Seite nicht gefunden") {
		t.Fatalf("unbekannter Pfad %d %q", resp.StatusCode, body)
	}
}

func TestServerTimeouts(t *testing.T) {
	local := newLocalServer(context.Background(), nil)
	if local.ReadHeaderTimeout != 5*time.Second || local.WriteTimeout != 0 || local.IdleTimeout != 60*time.Second || local.BaseContext == nil {
		t.Fatalf("lokal %+v", local)
	}
	admin := newAdminServer(nil, nil)
	if admin.ReadHeaderTimeout != 5*time.Second || admin.ReadTimeout != 5*time.Minute || admin.WriteTimeout != 5*time.Minute || admin.IdleTimeout != 60*time.Second {
		t.Fatalf("Admin %+v", admin)
	}
}

func TestShutdownWithOpenEventStreamIsQuick(t *testing.T) {
	f := newFixture(t)
	f.saveConfig(nil)
	f.start()
	req, _ := http.NewRequest("GET", "http://"+f.bound.Local+"/api/events", nil)
	req.Host = "127.0.0.1:8080"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	start := time.Now()
	f.stop()
	if d := time.Since(start); d > 4*time.Second {
		t.Fatalf("Herunterfahren dauert %v", d)
	}
}

func TestNotifyReadyAndWatchdogWired(t *testing.T) {
	name := filepath.Join(t.TempDir(), "notify.sock")
	c := listenDatagram(t, name)
	t.Setenv("NOTIFY_SOCKET", name)
	t.Setenv("WATCHDOG_USEC", strconv.Itoa(100_000))
	t.Setenv("WATCHDOG_PID", "")
	f := newFixture(t)
	f.saveConfig(nil)
	f.start()
	if got := readDatagram(t, c, waitLimit); got != "READY=1" {
		t.Fatalf("erste Meldung %q", got)
	}
	if got := readDatagram(t, c, waitLimit); got != "WATCHDOG=1" {
		t.Fatalf("zweite Meldung %q", got)
	}
}

func TestChromiumStartsOnlyWhenEnabled(t *testing.T) {
	f := newFixture(t)
	f.saveConfig(nil)
	f.start()
	if n := len(f.runner.started()); n != 0 {
		t.Fatalf("%d Prozesse ohne Chromium", n)
	}
	f.stop()

	g := newFixture(t)
	g.saveConfig(func(c *config.Config) { c.Display.GraphicsProfile = "x11" })
	ready := make(chan Bound, 1)
	opts := g.options()
	opts.ChromiumEnabled = true
	opts.Ready = func(b Bound) { ready <- b }
	s, err := newService(opts)
	if err != nil {
		t.Fatal(err)
	}
	s.lookPath = func(name string) (string, error) { return "/opt/" + name, nil }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.serve(ctx) }()
	<-ready
	eventually(t, "Chromium", func() bool { return len(g.runner.started()) >= 1 })
	cmd := g.runner.started()[0]
	args := strings.Join(cmd.Args, " ")
	if cmd.Path != "/opt/chromium" || !strings.Contains(args, "--ozone-platform=x11") || !strings.Contains(args, "--kiosk") || !strings.Contains(args, filepath.Join(g.run, "chromium")) {
		t.Fatalf("Aufruf %+v", cmd)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestStatusReportsOverlayAndVersion(t *testing.T) {
	root := t.TempDir()
	mounts := func(content string) {
		if err := os.WriteFile(filepath.Join(root, "mounts"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mounts("/dev/mmcblk0p2 / ext4 ro 0 0\noverlay /tmp overlay rw 0 0\n")
	if overlayActive(root) {
		t.Fatal("kein Overlay auf /")
	}
	mounts("/dev/mmcblk0p2 / ext4 ro 0 0\noverlay / overlay rw,lowerdir=/x 0 0\nproc /proc proc rw 0 0\n")
	if !overlayActive(root) {
		t.Fatal("Overlay auf / nicht erkannt")
	}
	if overlayActive(filepath.Join(root, "fehlt")) {
		t.Fatal("ohne Datei aktiv")
	}

	f := newFixture(t)
	f.saveConfig(nil)
	f.start()
	st := f.bound.Status()
	if st.Version != release.Version || st.Display == "" || st.DataTotalBytes <= 0 {
		t.Fatalf("Status %+v", st)
	}
}

// Chromium folgt der laufenden Grafiksitzung, nicht der gespeicherten Einstellung,
// und startet erst, wenn die Kachelseite antwortet.
func TestChromiumUsesRunningSessionProfileAndStartsAfterLocalServer(t *testing.T) {
	g := newFixture(t)
	g.saveConfig(func(c *config.Config) { c.Display.GraphicsProfile = "x11" })
	opts := g.options()
	opts.ChromiumEnabled = true
	if err := os.MkdirAll(filepath.Dir(opts.SessionProfileFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(opts.SessionProfileFile, []byte("labwc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	opts.LocalAddr = l.Addr().String()
	l.Close()
	listening := make(chan bool, 1)
	g.runner.onStart = func(c client.Command) {
		if c.Path != "/opt/chromium" {
			return
		}
		conn, err := net.DialTimeout("tcp", opts.LocalAddr, time.Second)
		if err == nil {
			conn.Close()
		}
		select {
		case listening <- err == nil:
		default:
		}
	}
	ready := make(chan Bound, 1)
	opts.Ready = func(b Bound) { ready <- b }
	s, err := newService(opts)
	if err != nil {
		t.Fatal(err)
	}
	s.lookPath = func(name string) (string, error) { return "/opt/" + name, nil }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.serve(ctx) }()
	<-ready
	byPath := func(p string) (client.Command, bool) {
		for _, c := range g.runner.started() {
			if c.Path == p {
				return c, true
			}
		}
		return client.Command{}, false
	}
	eventually(t, "Chromium", func() bool { _, ok := byPath("/opt/chromium"); return ok })
	if !<-listening {
		t.Error("Chromium startete, bevor die Kachelseite erreichbar war")
	}
	// Unter labwc läuft auch die Bildschirmtastatur, verborgen.
	eventually(t, "Bildschirmtastatur", func() bool { _, ok := byPath("/opt/wvkbd-mobintl"); return ok })
	if kb, _ := byPath("/opt/wvkbd-mobintl"); !slices.Contains(kb.Args, "--hidden") {
		t.Errorf("Tastatur nicht verborgen gestartet: %v", kb.Args)
	}
	chromium, _ := byPath("/opt/chromium")
	args := strings.Join(chromium.Args, " ")
	if !strings.Contains(args, "--ozone-platform=wayland") {
		t.Errorf("Plattform nicht aus der laufenden Sitzung: %s", args)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
