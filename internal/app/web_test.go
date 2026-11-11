package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"hmikiosk/internal/appcore"
	"hmikiosk/internal/client"
	"hmikiosk/internal/config"
	"hmikiosk/internal/session"
)

const grafanaURL = "http://grafana:3000/d/abc?kiosk"

func webHMI(c *config.Config) {
	c.HMIs = append(c.HMIs, config.HMI{ID: "grafana", Name: "Grafana", Kind: config.KindWeb, URL: grafanaURL, Icon: "monitor", Color: "green", Reach: config.ReachOff})
}

type helperCalls struct {
	mu   sync.Mutex
	args []string
	fail error
}

func (h *helperCalls) call(_ context.Context, cmd, arg string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if cmd != "set-web-origins" {
		return errors.New("unerwarteter Befehl " + cmd)
	}
	h.args = append(h.args, arg)
	return h.fail
}

func (h *helperCalls) all() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.args)
}

func TestServicePushesWebOriginsOnStartAndApply(t *testing.T) {
	f := newFixture(t)
	cfg := f.saveConfig(func(c *config.Config) { oneHMI(c); webHMI(c) })
	calls := &helperCalls{}
	f.startWith(func(s *service) { s.helperCall = calls.call })
	eventually(t, "Ursprünge beim Start", func() bool { return slices.Contains(calls.all(), `["http://grafana:3000"]`) })
	cfg.HMIs[1].URL = "https://grafana.werk:8443/d/x"
	f.s.Apply(cfg)
	eventually(t, "Ursprünge nach Apply", func() bool { return slices.Contains(calls.all(), `["https://grafana.werk:8443"]`) })
}

func startWebService(t *testing.T, mutate func(*config.Config)) *fixture {
	t.Helper()
	return startWebServiceWith(t, mutate, (&helperCalls{}).call)
}

func startWebServiceWith(t *testing.T, mutate func(*config.Config), call func(context.Context, string, string) error) *fixture {
	t.Helper()
	f := newFixture(t)
	f.saveConfig(func(c *config.Config) {
		oneHMI(c)
		webHMI(c)
		if mutate != nil {
			mutate(c)
		}
	})
	opts := f.options()
	opts.ChromiumEnabled = true
	ready := make(chan Bound, 1)
	opts.Ready = func(b Bound) { ready <- b }
	s, err := newService(opts)
	if err != nil {
		t.Fatal(err)
	}
	s.lookPath = func(name string) (string, error) { return "/opt/" + name, nil }
	s.helperCall = call
	f.s = s
	ctx, cancel := context.WithCancel(context.Background())
	f.cancel, f.done = cancel, make(chan error, 1)
	go func() { f.done <- s.serve(ctx) }()
	t.Cleanup(f.stop)
	f.bound = <-ready
	return f
}

func (f *fixture) webStarts() []client.Command {
	var out []client.Command
	for _, c := range f.runner.started() {
		if c.Path == "/opt/chromium" && slices.ContainsFunc(c.Args, func(a string) bool { return strings.HasPrefix(a, "--app=http://grafana") }) {
			out = append(out, c)
		}
	}
	return out
}

func TestServiceOpensWebTileInOwnChromium(t *testing.T) {
	f := startWebService(t, nil)
	if code, body := f.local("GET", "/"); code != 200 || !strings.Contains(body, "Grafana") {
		t.Fatalf("Kachelseite %d: %s", code, body)
	}
	if code, body := f.local("POST", "/api/hmi/grafana/open"); code != 204 {
		t.Fatalf("Öffnen %d: %s", code, body)
	}
	eventually(t, "Chromium der Web-Kachel", func() bool { return len(f.webStarts()) == 1 })
	args := f.webStarts()[0].Args
	profile := filepath.Join(f.data, "web-profile")
	for _, want := range []string{"--app=" + grafanaURL, "--user-data-dir=" + profile, "--class=" + client.WebAppClass, "--ozone-platform=wayland"} {
		if !slices.Contains(args, want) {
			t.Errorf("%q fehlt in %q", want, args)
		}
	}
	if fi, err := os.Stat(profile); err != nil || !fi.IsDir() {
		t.Fatalf("Profilordner: %v", err)
	}
	eventually(t, "verbunden", func() bool { return f.s.mgr.Snapshot().State == session.StateConnected })
	// Keine Belegung: Die Kachel bleibt für andere Terminals frei.
	if occ := f.s.coord.Occupancy(); len(occ) != 0 {
		t.Fatalf("Belegung %v", occ)
	}
	// Löschen der Anmeldungen geht nur ohne offene Web-Kachel.
	if err := f.s.clearWebLogins(); !errors.Is(err, session.ErrWebOpen) {
		t.Fatalf("Löschen bei offener Kachel: %v", err)
	}
	if _, err := os.Stat(profile); err != nil {
		t.Fatal("Profil trotz offener Kachel gelöscht")
	}
	f.local("POST", "/api/back")
	eventually(t, "Kacheln", func() bool { return f.s.mgr.Snapshot().State == session.StateTiles })
	if err := os.WriteFile(filepath.Join(profile, "Cookies"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.s.clearWebLogins(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(profile); !os.IsNotExist(err) {
		t.Fatalf("Profil noch da: %v", err)
	}
}

func TestPermanentWebTileStartsAutomatically(t *testing.T) {
	f := startWebService(t, func(c *config.Config) { c.Kiosk.PermanentHMI = "grafana" })
	eventually(t, "Daueranzeige", func() bool { return len(f.webStarts()) == 1 })
}

func TestWebTileReachUsesURLHostAndPort(t *testing.T) {
	var gotHost string
	var gotPort int
	r := newReachability(func() []config.HMI {
		return []config.HMI{{ID: "w", Kind: config.KindWeb, URL: "https://grafana.werk/d/x", Reach: config.ReachPort}}
	}, func() string { return "" }, func(_ context.Context, host string, port int) bool {
		gotHost, gotPort = host, port
		return true
	})
	r.round(context.Background())
	if gotHost != "grafana.werk" || gotPort != 443 || !r.reachable("w") {
		t.Fatalf("Prüfung %q:%d", gotHost, gotPort)
	}
	var pinged string
	r = newReachability(func() []config.HMI {
		return []config.HMI{{ID: "w", Kind: config.KindWeb, URL: "http://10.0.0.7:3000/"}}
	}, func() string { return "" }, nil)
	r.ping = func(_ context.Context, host string) (bool, error) { pinged = host; return true, nil }
	r.round(context.Background())
	if pinged != "10.0.0.7" {
		t.Fatalf("Ping an %q", pinged)
	}
}

func TestWebSessionJournalMode(t *testing.T) {
	f := newFixture(t)
	f.saveConfig(nil)
	f.start()
	f.s.onSessionEnd(session.Ended{HMIName: "Grafana", Web: true, Reason: "x"})
	eventually(t, "Protokollzeile", func() bool {
		b, _ := os.ReadFile(filepath.Join(f.data, "log", appcore.LogFileName))
		return strings.Contains(string(b), `modus="Webseite"`)
	})
}

func TestAdminProbeOfWebTileChecksPortOnly(t *testing.T) {
	var gotHost string
	var gotPort int
	answer := true
	old := tcpReachable
	tcpReachable = func(_ context.Context, host string, port int) bool { gotHost, gotPort = host, port; return answer }
	t.Cleanup(func() { tcpReachable = old })
	f := newFixture(t)
	f.saveConfig(nil)
	f.start()
	h := config.HMI{ID: "w", Kind: config.KindWeb, URL: "https://grafana.werk/d/x"}
	res := f.s.adminProbe(context.Background(), h)
	if !res.Reachable || res.AuthOK || res.AuthRejected || gotHost != "grafana.werk" || gotPort != 443 {
		t.Fatalf("Ergebnis %+v, geprüft %q:%d", res, gotHost, gotPort)
	}
	answer = false
	if res = f.s.adminProbe(context.Background(), h); res.Reachable {
		t.Fatalf("nicht erreichbar gemeldet als %+v", res)
	}
}

func TestAdminDeletesWebLoginsThroughService(t *testing.T) {
	f := newFixture(t)
	f.saveConfig(nil)
	f.start()
	profile := filepath.Join(f.data, "web-profile")
	if err := os.MkdirAll(profile, 0o700); err != nil {
		t.Fatal(err)
	}
	deps := f.s.adminDeps()
	if deps.ClearWebLogins == nil {
		t.Fatal("Admin-Seite ohne Löschen der Web-Anmeldungen")
	}
	if err := deps.ClearWebLogins(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(profile); !os.IsNotExist(err) {
		t.Fatalf("Profil noch da: %v", err)
	}
}

func TestWebTileWaitsForPolicyAndReportsFailure(t *testing.T) {
	old := webReadyLimit
	webReadyLimit = 200 * time.Millisecond
	t.Cleanup(func() { webReadyLimit = old })
	f := startWebServiceWith(t, nil, (&helperCalls{fail: errors.New("Der Helfer ist nicht erreichbar")}).call)
	// Neue Adresse auf einem anderen Server: Der Helfer müsste sie erst freigeben.
	cfg := f.s.config()
	cfg.HMIs = slices.Clone(cfg.HMIs)
	cfg.HMIs[1].URL = "http://neu.werk:3000/"
	f.s.Apply(cfg)
	if code, body := f.local("POST", "/api/hmi/grafana/open"); code != 204 {
		t.Fatalf("Öffnen %d: %s", code, body)
	}
	eventually(t, "Hinweis", func() bool {
		s := f.s.mgr.Snapshot()
		return s.State == session.StateTiles && strings.Contains(s.Notice, "Freigabe")
	})
	if n := len(f.webStarts()); n != 0 {
		t.Fatalf("%d Web-Chromium trotz fehlender Freigabe", n)
	}
}

// Beim Start gilt die Richtlinie, die der Helfer vorher aus seiner gespeicherten Liste
// geschrieben hat (ExecStartPre): Die Daueranzeige wartet nicht auf den Helfer.
func TestPermanentWebTileStartsWithoutHelperAtBoot(t *testing.T) {
	old := webReadyLimit
	webReadyLimit = 200 * time.Millisecond
	t.Cleanup(func() { webReadyLimit = old })
	f := startWebServiceWith(t, func(c *config.Config) { c.Kiosk.PermanentHMI = "grafana" },
		(&helperCalls{fail: errors.New("Der Helfer ist nicht erreichbar")}).call)
	eventually(t, "Daueranzeige", func() bool { return len(f.webStarts()) == 1 })
}
