package pdhterminal

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hmikiosk/internal/config"
	"hmikiosk/internal/update"
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

// startSmoke startet den Dienst; freeAddr gibt den Port frei, bevor der Dienst ihn bindet. Ein anderer Prozess kann ihn
// dazwischen nehmen, dann bleibt LocalAdmin leer: Es wird bis zu dreimal mit neuem Port versucht.
func startSmoke(t *testing.T, data, run string) (b Bound, cancel context.CancelFunc, done chan error) {
	t.Helper()
	for attempt := 1; ; attempt++ {
		ready := make(chan Bound, 1)
		ctx, c := context.WithCancel(context.Background())
		d := make(chan error, 1)
		go func() {
			d <- Run(ctx, Options{
				DataDir: data, RunDir: run,
				LocalAddr: "127.0.0.1:0", AdminAddr: "127.0.0.1:0", LocalAdminAddr: freeAddr(t),
				HelperSocket: filepath.Join(t.TempDir(), "kein.sock"),
				HardwareID:   func() string { return "test-0001" },
				Ready:        func(b Bound) { ready <- b },
			})
		}()
		select {
		case b = <-ready:
		case err := <-d:
			c()
			t.Fatalf("Dienst endete früh: %v", err)
		case <-time.After(20 * time.Second):
			c()
			t.Fatal("Dienst startet nicht")
		}
		if b.Admin != "" && b.LocalAdmin != "" {
			return b, c, d
		}
		c()
		<-d
		if attempt >= 3 {
			t.Fatalf("Admin-Seite (%q) oder Admin am Terminal (%q) nicht gebunden: Port belegt? (Versuch %d)", b.Admin, b.LocalAdmin, attempt)
		}
	}
}

// TestRunSmoke startet den ganzen Dienst ohne Chromium in temporären Ordnern.
func TestRunSmoke(t *testing.T) {
	data, run := t.TempDir(), t.TempDir()
	rootFS = t.TempDir()
	t.Cleanup(func() { rootFS = "/" })
	b, cancel, done := startSmoke(t, data, run)

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

func TestAdminSelftestUnknownDisplayTypeIsRed(t *testing.T) {
	skipOnWindows(t)
	s := &service{cfg: config.Config{StatusDisplay: config.StatusDisplayCfg{Type: "ssd1306"}}}
	_, reason := s.cfg.StatusDisplay.Known()
	for _, st := range s.adminSelftest(context.Background()) {
		if st.Name != "statusdisplay" {
			continue
		}
		if st.OK || !strings.Contains(st.Detail, reason) {
			t.Fatalf("Schritt statusdisplay: %+v, erwartet rot mit Grund %q", st, reason)
		}
		return
	}
	t.Fatal("Schritt statusdisplay fehlt")
}

// TestRunWithoutHelper: Ohne Helfer (HelperSocket "", wie unter Windows) läuft der Dienst,
// und die Admin-Seite bietet keinen Netzwerk-Abschnitt an.
func TestRunWithoutHelper(t *testing.T) {
	rootFS = t.TempDir()
	t.Cleanup(func() { rootFS = "/" })
	ready := make(chan Bound, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			DataDir: t.TempDir(), RunDir: t.TempDir(),
			LocalAddr: "127.0.0.1:0", AdminAddr: "127.0.0.1:0", LocalAdminAddr: freeAddr(t),
			HelperSocket: "", ChromiumEnabled: false,
			HardwareID: func() string { return "test-0002" },
			Ready:      func(b Bound) { ready <- b },
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
	c := &http.Client{Timeout: 5 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} //nolint:gosec // Test mit selbst ausgestelltem Zertifikat
	resp, err := c.Get("https://" + b.Admin + "/admin/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("/admin/: Status %d", resp.StatusCode)
	}
	if strings.Contains(string(body), `href="/admin/network"`) {
		t.Errorf("Netzwerk-Abschnitt trotz fehlendem Helfer: %s", body)
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

// TestNoHelperDeps: Ohne Helfer sind die Schnittstellen der Admin-Seite wirklich leer
// (keine Schnittstelle mit nil-Zeiger darin).
func TestNoHelperDeps(t *testing.T) {
	s, err := newService(Options{DataDir: t.TempDir(), RunDir: t.TempDir(), HardwareID: func() string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	if s.helper != nil {
		t.Error("s.helper gesetzt")
	}
	d := s.adminDeps()
	if d.Helper != nil || d.Backup != nil || d.Net != nil {
		t.Errorf("Helfer-Schnittstellen nicht nil: %v %v %v", d.Helper, d.Backup, d.Net)
	}
}

// TestUpdateActivateWithoutHelper: Ohne Helfer (Windows) schreibt die Aktivierung nichts, sondern bittet verzögert
// um einen Neustart (ErrRestart); der Starter wählt dann die neue Version.
func TestUpdateActivateWithoutHelper(t *testing.T) {
	old := restartDelay
	restartDelay = 50 * time.Millisecond
	t.Cleanup(func() { restartDelay = old })
	dir := t.TempDir()
	s, err := newService(Options{DataDir: dir, RunDir: t.TempDir(), HardwareID: func() string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	inst, ok := s.adminDeps().Update.(*update.Installer)
	if !ok {
		t.Fatalf("Update ist %T", s.adminDeps().Update)
	}
	start := time.Now()
	if err := inst.Activate(context.Background(), "1.2.3"); err != nil {
		t.Fatalf("Aktivierung: %v", err)
	}
	select {
	case <-s.exit:
		t.Fatal("Neustart kam ohne Verzögerung: die Antwort der Admin-Seite ginge verloren")
	default:
	}
	select {
	case err := <-s.exit:
		if !errors.Is(err, ErrRestart) {
			t.Errorf("Ende mit %v, erwartet ErrRestart", err)
		}
		if time.Since(start) < 40*time.Millisecond {
			t.Errorf("Neustart nach %v", time.Since(start))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("kein Neustart angefordert")
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		for _, e := range ents {
			if e.Name() == "app" {
				t.Errorf("Aktivierung hat in den Datenordner geschrieben: %s", e.Name())
			}
		}
	}
}

// TestSystemLevelOnlyWithHelper: Ohne Helfer (Windows) gibt es keine Systemstand-Prüfung,
// sonst lehnte jedes Update mit "braucht neuere Systemteile" ab.
func TestSystemLevelOnlyWithHelper(t *testing.T) {
	for _, tc := range []struct {
		socket string
		want   bool
	}{{"", false}, {filepath.Join(t.TempDir(), "h.sock"), true}} {
		s, err := newService(Options{DataDir: t.TempDir(), RunDir: t.TempDir(), HelperSocket: tc.socket, HardwareID: func() string { return "" }})
		if err != nil {
			t.Fatal(err)
		}
		inst := s.adminDeps().Update.(*update.Installer)
		if got := inst.SystemLevel != nil; got != tc.want {
			t.Errorf("Socket %q: SystemLevel gesetzt = %v, erwartet %v", tc.socket, got, tc.want)
		}
		s.close()
	}
}

// TestRunLinuxExitsWhenStartPortBusy: Wie bisher endet der Dienst unter Linux ohne Startseite (systemd startet neu).
func TestRunLinuxExitsWhenStartPortBusy(t *testing.T) {
	old := staysUpWithoutStart
	staysUpWithoutStart = false
	t.Cleanup(func() { staysUpWithoutStart = old })
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	rootFS = t.TempDir()
	t.Cleanup(func() { rootFS = "/" })
	err = Run(context.Background(), Options{
		DataDir: t.TempDir(), RunDir: t.TempDir(),
		LocalAddr: busy.Addr().String(), AdminAddr: "127.0.0.1:0", LocalAdminAddr: "127.0.0.1:0",
		HelperSocket: filepath.Join(t.TempDir(), "kein.sock"), HardwareID: func() string { return "x" },
	})
	if err == nil || !strings.Contains(err.Error(), "Startseite") {
		t.Fatalf("Fehler %v, erwartet Startseite", err)
	}
}

// TestRunStaysUpDegradedWhenStartPortBusy: Windows. Port 8080 belegt: Der Dienst bleibt oben, das Symbol ist rot mit
// Grund, die Selbstprüfung ist rot, die Gesundmarke steht (der Prozess ist gesund), und sobald der Port frei ist,
// wird die Startseite gebunden, ohne Neustart.
func TestRunStaysUpDegradedWhenStartPortBusy(t *testing.T) {
	oldStay, oldEvery := staysUpWithoutStart, portRetryEvery
	staysUpWithoutStart, portRetryEvery = true, 50*time.Millisecond
	t.Cleanup(func() { staysUpWithoutStart, portRetryEvery = oldStay, oldEvery })
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	startAddr := busy.Addr().String()
	_, port, _ := net.SplitHostPort(startAddr)

	rootFS = t.TempDir()
	t.Cleanup(func() { rootFS = "/" })
	run := t.TempDir()
	ft := newFakeTray()
	ready := make(chan Bound, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			DataDir: t.TempDir(), RunDir: run,
			LocalAddr: startAddr, AdminAddr: "127.0.0.1:0", LocalAdminAddr: "127.0.0.1:0",
			HelperSocket: filepath.Join(t.TempDir(), "kein.sock"), HardwareID: func() string { return "x" },
			Ready: func(b Bound) { ready <- b }, tray: ft,
		})
	}()
	select {
	case b := <-ready:
		if b.Local != "" {
			t.Errorf("Startseite gebunden trotz belegtem Port: %q", b.Local)
		}
	case err := <-done:
		t.Fatalf("Dienst endete trotz belegtem Port: %v", err)
	case <-time.After(20 * time.Second):
		t.Fatal("Dienst startet nicht")
	}
	// Das Symbol wird rot mit dem Grund.
	waitUntil(t, "rotes Symbol mit Port", func() bool { return strings.Contains(ft.last(), "Port "+port+" belegt") })
	if v, err := os.ReadFile(filepath.Join(run, "healthy")); err != nil || len(v) == 0 {
		t.Errorf("Gesundmarke fehlt trotz gesundem Prozess: %q %v", v, err)
	}
	// Der Selbsttest-Grund (Windows-Schritt „ports“) nennt den Port.
	e := &service{problems: map[string]string{}}
	e.problems[problemStart] = portBusyText(startAddr, "Startseite nicht verfügbar")
	if got := e.portProblems(); !strings.Contains(got, "Port "+port+" belegt") {
		t.Errorf("portProblems: %q", got)
	}

	busy.Close() // frei: der nächste Versuch bindet
	waitUntil(t, "Startseite gebunden", func() bool {
		c, err := net.DialTimeout("tcp", startAddr, time.Second)
		if err == nil {
			c.Close()
		}
		return err == nil
	})
	waitUntil(t, "Symbol wieder grün", func() bool { return ft.last() == "" })

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

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("Timeout: %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
