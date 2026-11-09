package app

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"hmikiosk/internal/client"
	"hmikiosk/internal/config"
	"hmikiosk/internal/release"
	"hmikiosk/internal/rfb"
	"hmikiosk/internal/secrets"
	"hmikiosk/internal/session"
)

const waitLimit = 15 * time.Second

type safeBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type fakeProc struct {
	done chan error
	once sync.Once
}

func (p *fakeProc) Done() <-chan error { return p.done }

func (p *fakeProc) Stop(time.Duration) error {
	p.once.Do(func() { close(p.done) })
	return nil
}

type fakeRunner struct {
	mu   sync.Mutex
	cmds []client.Command
	// onStart läuft vor dem Eintrag, außerhalb der Sperre.
	onStart func(client.Command)
}

func (r *fakeRunner) Start(c client.Command) (client.Process, error) {
	if r.onStart != nil {
		r.onStart(c)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cmds = append(r.cmds, c)
	return &fakeProc{done: make(chan error, 1)}, nil
}

func (r *fakeRunner) started() []client.Command {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]client.Command(nil), r.cmds...)
}

type fixture struct {
	t       *testing.T
	data    string
	run     string
	runner  *fakeRunner
	s       *service
	bound   Bound
	cancel  context.CancelFunc
	done    chan error
	stopped bool
	hw      string // Hardware-Kennung des Testgeräts, leer: nicht lesbar
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	return &fixture{t: t, data: t.TempDir(), run: t.TempDir(), runner: &fakeRunner{}}
}

func (f *fixture) seal(s string) string {
	f.t.Helper()
	b, err := secrets.OpenBox(filepath.Join(f.data, "secrets.key"), false)
	if err != nil {
		f.t.Fatal(err)
	}
	enc, err := b.Seal(s)
	if err != nil {
		f.t.Fatal(err)
	}
	return enc
}

func (f *fixture) saveConfig(mutate func(*config.Config)) config.Config {
	f.t.Helper()
	c := config.Default()
	c.Terminal.Name = "T1"
	if mutate != nil {
		mutate(&c)
	}
	if err := config.Open(f.data).Save(c); err != nil {
		f.t.Fatal(err)
	}
	return c
}

func (f *fixture) options() Options {
	return Options{
		DataDir: f.data, RunDir: f.run,
		LocalAddr: "127.0.0.1:0", AdminAddr: "127.0.0.1:0", LocalAdminAddr: "127.0.0.1:0",
		HelperSocket:       filepath.Join(f.run, "kein-helfer.sock"),
		SessionProfileFile: filepath.Join(f.run, "keine-sitzung", "profile"),
		Runner:             f.runner,
		HardwareID:         func() string { return f.hw },
	}
}

func (f *fixture) start() *fixture {
	f.t.Helper()
	return f.startWith(nil)
}

// startWith führt den Dienst bis zur Bereitschaft; prepare läuft nach dem Aufbau
// und vor dem Start der Server.
func (f *fixture) startWith(prepare func(*service)) *fixture {
	f.t.Helper()
	ready := make(chan Bound, 1)
	opts := f.options()
	opts.Ready = func(b Bound) { ready <- b }
	s, err := newService(opts)
	if err != nil {
		f.t.Fatal(err)
	}
	if prepare != nil {
		prepare(s)
	}
	f.s = s
	ctx, cancel := context.WithCancel(context.Background())
	f.cancel, f.done, f.stopped = cancel, make(chan error, 1), false
	go func() { f.done <- s.serve(ctx) }()
	f.t.Cleanup(f.stop)
	select {
	case f.bound = <-ready:
	case err := <-f.done:
		f.t.Fatalf("Dienst endet beim Start: %v", err)
	case <-time.After(waitLimit):
		f.t.Fatal("Dienst wird nicht bereit")
	}
	return f
}

func (f *fixture) stop() {
	f.t.Helper()
	if f.stopped {
		return
	}
	f.stopped = true
	f.cancel()
	select {
	case err := <-f.done:
		if err != nil {
			f.t.Errorf("Dienst endet mit %v", err)
		}
	case <-time.After(waitLimit):
		f.t.Fatal("Dienst endet nicht")
	}
}

// local spricht die Kachelseite an; sie lässt nur den festen Host zu.
func (f *fixture) local(method, path string) (int, string) {
	f.t.Helper()
	req, err := http.NewRequest(method, "http://"+f.bound.Local+path, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Origin", "http://127.0.0.1:8080")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func insecureClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(waitLimit)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("warte auf %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func hasWarning(status []string, part string) bool {
	for _, w := range status {
		if strings.Contains(w, part) {
			return true
		}
	}
	return false
}

func oneHMI(c *config.Config) {
	c.HMIs = []config.HMI{{ID: "presse", Name: "Presse 7", Host: "127.0.0.1", Port: 1, Icon: "press", Color: "blue"}}
}

func listenVNC(t *testing.T, password string) int {
	t.Helper()
	srv := rfb.NewTestServer(64, 48, password)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close(); srv.DropClients() })
	go srv.Serve(l)
	return l.Addr().(*net.TCPAddr).Port
}

// --- aus der Aufgabe ---

func TestStartupKillsLeftoverChildren(t *testing.T) {
	leftover, gone := startNamed(t, "remmina-alt")
	stranger, strangerGone := startNamed(t, "fremd")
	f := newFixture(t)
	content := fmt.Sprintf("%d\n%d\n", leftover.Process.Pid, stranger.Process.Pid)
	if err := os.WriteFile(filepath.Join(f.run, pidFileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(f.run, "sessions", "s-alt")
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	f.saveConfig(nil)
	f.start()
	awaitGone(t, gone, "alter Client")
	select {
	case <-strangerGone:
		t.Fatal("fremder Prozess beendet")
	default:
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("alter Sitzungsordner bleibt: %v", err)
	}
}

func TestStartupUsesFallbackConfigAndFlagsWarning(t *testing.T) {
	f := newFixture(t)
	f.saveConfig(func(c *config.Config) {
		oneHMI(c)
		c.Admin.PasswordHash = "hash"
	})
	if err := config.Open(f.data).MarkGood(); err != nil {
		t.Fatal(err)
	}
	good, _ := os.ReadFile(filepath.Join(f.data, "config.json.good"))
	if err := os.WriteFile(filepath.Join(f.data, "config.json"), []byte("{kaputt"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.start()
	if w := f.bound.Status().Warnings; !hasWarning(w, "Rückfall") {
		t.Fatalf("Warnungen %v", w)
	}
	if code, body := f.local("GET", "/"); code != 200 || !strings.Contains(body, "Presse 7") {
		t.Fatalf("Kachelseite %d: %s", code, body)
	}
	after, _ := os.ReadFile(filepath.Join(f.data, "config.json.good"))
	if string(after) != string(good) {
		t.Fatal("Rückfall überschrieben: Der defekte Stand darf nicht gut werden")
	}
}

func TestReadOnlyDataDirStillServesTiles(t *testing.T) {
	// Der Test läuft als root, Dateirechte greifen nicht: Eine Datei statt des
	// Ordners lässt jeden Schreib- und Lesezugriff unter /data scheitern.
	f := newFixture(t)
	blocker := filepath.Join(t.TempDir(), "kein-ordner")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	f.data = blocker
	f.start()
	if code, _ := f.local("GET", "/"); code != 200 {
		t.Fatalf("Kachelseite %d", code)
	}
	status := f.bound.Status()
	if !hasWarning(status.Warnings, "Konfiguration") || !hasWarning(status.Warnings, "Protokoll") {
		t.Fatalf("Warnungen %v", status.Warnings)
	}
	if f.bound.Admin == "" {
		t.Fatal("Admin-Seite meldet den Fehler nicht, sie läuft gar nicht")
	}
	resp, err := insecureClient().Get("https://" + f.bound.Admin + "/admin/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusInternalServerError || !strings.Contains(string(body), "Konfiguration nicht lesbar") {
		t.Fatalf("Admin %d: %s", resp.StatusCode, body)
	}
}

func TestBrokenDataPartsKeepLoadedConfig(t *testing.T) {
	f := newFixture(t)
	f.saveConfig(func(c *config.Config) {
		oneHMI(c)
		c.HMIs[0].PasswordEnc = "unlesbar"
	})
	// Schlüssel unlesbar, Protokollordner unbrauchbar, Zertifikat nicht anlegbar.
	if err := os.Mkdir(filepath.Join(f.data, "secrets.key"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.data, "log"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.data, "tls"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	f.start()
	if code, body := f.local("GET", "/"); code != 200 || !strings.Contains(body, "Presse 7") {
		t.Fatalf("Kachelseite %d: %s", code, body)
	}
	status := f.bound.Status()
	if !hasWarning(status.Warnings, "Schlüsseldatei") || !hasWarning(status.Warnings, "Protokoll") || !hasWarning(status.Warnings, "Zertifikat") {
		t.Fatalf("Warnungen %v", status.Warnings)
	}
	if code, _ := f.local("POST", "/api/hmi/presse/open"); code != http.StatusNoContent {
		t.Fatalf("Open %d", code)
	}
	eventually(t, "Hinweis", func() bool {
		s := f.bound.Snapshot()
		return s.State == session.StateTiles && strings.Contains(s.Notice, "Passwort")
	})
}

func TestHealthyMarkerWrittenAfterStart(t *testing.T) {
	f := newFixture(t)
	marker := filepath.Join(f.run, "healthy")
	if err := os.WriteFile(marker, []byte("0.0.1-alt"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.saveConfig(nil)
	var atStart bool
	f.startWith(func(*service) {
		_, err := os.Stat(marker)
		atStart = err == nil
	})
	if atStart {
		t.Fatal("alte Marke gilt noch, bevor der Dienst läuft")
	}
	if code, _ := f.local("GET", "/"); code != 200 {
		t.Fatalf("Kachelseite %d", code)
	}
	info, err := os.Lstat(marker)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("keine reguläre Datei: %v", info.Mode())
	}
	b, _ := os.ReadFile(marker)
	if string(b) != release.Version {
		t.Fatalf("Marke %q", b)
	}
	entries, _ := os.ReadDir(f.run)
	for _, e := range entries {
		if strings.Contains(e.Name(), "healthy") && e.Name() != "healthy" {
			t.Fatalf("Reste: %s", e.Name())
		}
	}
}

// --- Start ---

func TestFirstStartPersistsTerminalID(t *testing.T) {
	f := newFixture(t)
	f.start()
	c, info, err := config.Open(f.data).Load()
	if err != nil || info.FirstStart {
		t.Fatalf("nicht gespeichert: %v %+v", err, info)
	}
	if c.Terminal.ID == "" || f.s.config().Terminal.ID != c.Terminal.ID {
		t.Fatalf("Kennung %q", c.Terminal.ID)
	}
	f.stop()

	f.start()
	again, _, err := config.Open(f.data).Load()
	if err != nil || again.Terminal.ID != c.Terminal.ID {
		t.Fatalf("Kennung gewechselt: %v", err)
	}
}

func TestFirstStartWithoutDiskKeepsRunningInMemory(t *testing.T) {
	f := newFixture(t)
	// Der Ordner ist da und leer, das Speichern scheitert: config.json.tmp-* ist
	// nicht anlegbar, weil /proc keine neuen Dateien annimmt.
	if _, err := os.Stat("/proc/self"); err != nil {
		t.Skip("kein /proc")
	}
	f.data = "/proc/self/kein-ordner-hier"
	f.start()
	if code, _ := f.local("GET", "/"); code != 200 {
		t.Fatalf("Kachelseite %d", code)
	}
	if w := f.bound.Status().Warnings; !hasWarning(w, "Konfiguration") {
		t.Fatalf("Warnungen %v", w)
	}
}

func TestMarkGoodOnlyWithPassword(t *testing.T) {
	good := func(f *fixture) string { return filepath.Join(f.data, "config.json.good") }

	without := newFixture(t)
	without.saveConfig(nil)
	without.start()
	if _, err := os.Stat(good(without)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Rückfall ohne Passwort: %v", err)
	}

	with := newFixture(t)
	with.saveConfig(func(c *config.Config) { c.Admin.PasswordHash = "hash" })
	with.start()
	if _, err := os.Stat(good(with)); err != nil {
		t.Fatal(err)
	}
}

func TestRunRequiresRunner(t *testing.T) {
	f := newFixture(t)
	opts := f.options()
	opts.Runner = nil
	if err := Run(context.Background(), opts); err == nil {
		t.Fatal("kein Fehler ohne Runner")
	}
}

func TestLocalPortBusyIsAnError(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	f := newFixture(t)
	opts := f.options()
	opts.LocalAddr = l.Addr().String()
	ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
	defer cancel()
	if err := Run(ctx, opts); err == nil || ctx.Err() != nil {
		t.Fatalf("Ergebnis %v", err)
	}
}

func TestAdminPortBusyKeepsTiles(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	f := newFixture(t)
	f.saveConfig(nil)
	ready := make(chan Bound, 1)
	opts := f.options()
	opts.AdminAddr = l.Addr().String()
	opts.Ready = func(b Bound) { ready <- b }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, opts) }()
	select {
	case b := <-ready:
		if b.Admin != "" || !hasWarning(b.Status().Warnings, "Admin-Seite") {
			t.Fatalf("%q %v", b.Admin, b.Status().Warnings)
		}
	case err := <-done:
		t.Fatalf("Dienst endet: %v", err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestTruncatedKeyFileIsReplacedOnlyWithoutSealedValues(t *testing.T) {
	f := newFixture(t)
	f.saveConfig(nil)
	if err := os.WriteFile(filepath.Join(f.data, "secrets.key"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := newService(f.options())
	if err != nil {
		t.Fatal(err)
	}
	if !s.box.Available() {
		t.Fatalf("leere Schlüsseldatei ohne verschlüsselte Werte nicht ersetzt: %v", s.Status().Warnings)
	}
	s.close()

	g := newFixture(t)
	g.saveConfig(func(c *config.Config) {
		c.HMIs = []config.HMI{{ID: "a", Name: "Presse", Host: "10.0.0.1", Port: 5900, PasswordEnc: "AAAA"}}
	})
	if err := os.WriteFile(filepath.Join(g.data, "secrets.key"), []byte("kurz"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err = newService(g.options())
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	if s.box.Available() || !strings.Contains(strings.Join(s.Status().Warnings, " "), "beschädigt") {
		t.Fatalf("beschädigte Schlüsseldatei bei verschlüsselten Werten: box=%v %v", s.box.Available(), s.Status().Warnings)
	}
	if raw, _ := os.ReadFile(filepath.Join(g.data, "secrets.key")); string(raw) != "kurz" {
		t.Fatal("beschädigte Schlüsseldatei überschrieben")
	}
}
