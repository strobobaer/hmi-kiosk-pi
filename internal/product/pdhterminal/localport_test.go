package pdhterminal

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"hmikiosk/internal/config"
	"hmikiosk/internal/localport"
)

// freePortPair liefert einen Port P, bei dem P und P+1 auf 127.0.0.1 frei sind.
func freePortPair(t *testing.T) int {
	t.Helper()
	for range 50 {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		p := l.Addr().(*net.TCPAddr).Port
		l.Close()
		if p < 1024 || p > 65000 {
			continue
		}
		l2, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p+1))
		if err != nil {
			continue
		}
		l2.Close()
		return p
	}
	t.Fatal("kein freies Portpaar")
	return 0
}

func writeRaw(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func resetLocalPort(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { localport.Set(0) })
}

func TestNewServiceDefaultPort(t *testing.T) {
	resetLocalPort(t)
	s, err := newService(Options{DataDir: t.TempDir(), RunDir: t.TempDir(), HardwareID: func() string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	if s.o.LocalAddr != "127.0.0.1:8080" || s.o.LocalAdminAddr != "127.0.0.1:8081" || localport.Get() != 8080 || s.port != 8080 {
		t.Fatalf("Grundwert: %s %s %d", s.o.LocalAddr, s.o.LocalAdminAddr, localport.Get())
	}
}

func TestNewServiceTakesPortFromConfig(t *testing.T) {
	resetLocalPort(t)
	data := t.TempDir()
	cfg := config.Default()
	cfg.Kiosk.LocalPort = 18080
	if err := config.Open(data).Save(cfg); err != nil {
		t.Fatal(err)
	}
	s, err := newService(Options{DataDir: data, RunDir: t.TempDir(), HardwareID: func() string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	if s.o.LocalAddr != "127.0.0.1:18080" || s.o.LocalAdminAddr != "127.0.0.1:18081" {
		t.Fatalf("Adressen: %s %s", s.o.LocalAddr, s.o.LocalAdminAddr)
	}
	if localport.Get() != 18080 || localport.StartURL() != "http://127.0.0.1:18080/" || s.port != 18080 {
		t.Fatalf("localport: %d", localport.Get())
	}
	if localAdminOrigin() != "http://127.0.0.1:18081" {
		t.Fatalf("Ursprung der Admin-Seite am Terminal: %s", localAdminOrigin())
	}
}

// Ausdrücklich angegebene Adressen (Tests, Entwicklung) gelten weiter; der Port aus der Konfiguration bestimmt dann nur die Prüfungen.
func TestNewServiceExplicitAddrWins(t *testing.T) {
	resetLocalPort(t)
	s, err := newService(Options{DataDir: t.TempDir(), RunDir: t.TempDir(), LocalAddr: "127.0.0.1:0", LocalAdminAddr: "127.0.0.1:0",
		HardwareID: func() string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	if s.o.LocalAddr != "127.0.0.1:0" || s.o.LocalAdminAddr != "127.0.0.1:0" {
		t.Fatalf("Adressen: %s %s", s.o.LocalAddr, s.o.LocalAdminAddr)
	}
}

// Ungültiger Port in der Datei: Rückfall wie bei jedem ungültigen Wert (hier: Grundeinstellung ohne guten Stand).
func TestNewServiceInvalidPortInFileFallsBack(t *testing.T) {
	resetLocalPort(t)
	data := t.TempDir()
	good := config.Default()
	st := config.Open(data)
	if err := st.Save(good); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkGood(); err != nil {
		t.Fatal(err)
	}
	writeRaw(t, data, `{"schemaVersion":1,"terminal":{"id":"x","name":"T","role":"normal"},"defaultMode":"takeover",`+
		`"client":{"profile":"remmina","quality":2,"colorDepth":32,"scale":true},"kiosk":{"inactivityMinutes":15,"local_port":80},`+
		`"display":{"graphicsProfile":"labwc"},"update":{"repo":"strobobaer/hmi-kiosk-pi","prerelease":true}}`)
	s, err := newService(Options{DataDir: data, RunDir: t.TempDir(), HardwareID: func() string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	if s.port != 8080 || s.o.LocalAddr != "127.0.0.1:8080" {
		t.Fatalf("Port %d %s", s.port, s.o.LocalAddr)
	}
	if !strings.Contains(s.cfgWarn, "Port der lokalen Startseite") {
		t.Errorf("Warnung: %q", s.cfgWarn)
	}
}

type helperLog struct {
	mu    sync.Mutex
	calls []string
	err   error
}

func (h *helperLog) call(_ context.Context, cmd, arg string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, cmd+" "+arg)
	return h.err
}

func (h *helperLog) got() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.calls...)
}

func fastRestart(t *testing.T) {
	t.Helper()
	old := restartDelay
	restartDelay = 10 * time.Millisecond
	t.Cleanup(func() { restartDelay = old })
}

func newPortService(t *testing.T, h *helperLog) *service {
	t.Helper()
	resetLocalPort(t)
	fastRestart(t)
	s, err := newService(Options{DataDir: t.TempDir(), RunDir: t.TempDir(), HardwareID: func() string { return "" }, HelperCall: h.call})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.close)
	return s
}

func TestApplyPortChangeSetsPolicyThenRestarts(t *testing.T) {
	h := &helperLog{}
	s := newPortService(t, h)
	c := config.Default()
	c.Kiosk.LocalPort = 18080
	s.apply(c)
	select {
	case err := <-s.exit:
		if !errors.Is(err, ErrRestart) {
			t.Fatalf("Ende mit %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("kein Neustart angefordert")
	}
	var ports []string
	for _, c := range h.got() {
		if strings.HasPrefix(c, "set-local-port ") {
			ports = append(ports, c)
		}
	}
	if len(ports) != 1 || ports[0] != "set-local-port 18080" {
		t.Fatalf("Helferaufrufe: %v", h.got())
	}
}

func TestApplySamePortDoesNotRestart(t *testing.T) {
	h := &helperLog{}
	s := newPortService(t, h)
	s.apply(config.Default())
	c := config.Default()
	c.Kiosk.LocalPort = 8080 // ausdrücklich der Grundwert ist dasselbe
	s.apply(c)
	select {
	case err := <-s.exit:
		t.Fatalf("Neustart ohne Portänderung: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	for _, c := range h.got() {
		if strings.HasPrefix(c, "set-local-port") {
			t.Fatalf("Helferaufruf ohne Portänderung: %v", h.got())
		}
	}
}

// Schlägt das Setzen der Richtlinie fehl, läuft das Terminal weiter auf dem alten Port (sonst bliebe Chromium gesperrt).
func TestApplyPortChangeHelperFailureKeepsRunning(t *testing.T) {
	h := &helperLog{err: errors.New("Helfer nicht erreichbar")}
	s := newPortService(t, h)
	c := config.Default()
	c.Kiosk.LocalPort = 18080
	s.apply(c)
	select {
	case err := <-s.exit:
		t.Fatalf("Neustart trotz Fehler: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	s.mu.RLock()
	warns := strings.Join(s.warnings, "|")
	s.mu.RUnlock()
	if !strings.Contains(warns, "18080") || !strings.Contains(warns, "Helfer nicht erreichbar") {
		t.Errorf("Warnung fehlt: %q", warns)
	}
	// Ein neuer Versuch ist möglich.
	h.mu.Lock()
	h.err = nil
	h.mu.Unlock()
	s.apply(c)
	select {
	case err := <-s.exit:
		if !errors.Is(err, ErrRestart) {
			t.Fatalf("Ende mit %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("zweiter Versuch ohne Neustart")
	}
}

// Die Startseite bindet den Port aus der Konfiguration (ohne ausdrückliche Adresse) und nur ihn.
func TestRunBindsConfiguredPort(t *testing.T) {
	resetLocalPort(t)
	p := freePortPair(t)
	q := freePortPair(t) // Port der Admin-Seite über HTTPS: nicht p+1 (Windows vergibt freie Ports der Reihe nach)
	for q-p < 3 && p-q < 3 {
		q = freePortPair(t)
	}
	data := t.TempDir()
	cfg := config.Default()
	cfg.Kiosk.LocalPort = p
	if err := config.Open(data).Save(cfg); err != nil {
		t.Fatal(err)
	}
	rootFS = t.TempDir()
	t.Cleanup(func() { rootFS = "/" })
	ready := make(chan Bound, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			DataDir: data, RunDir: t.TempDir(), AdminAddr: "127.0.0.1:" + strconv.Itoa(q),
			HelperCall: func(context.Context, string, string) error { return nil },
			HardwareID: func() string { return "t" },
			Ready:      func(b Bound) { ready <- b },
		})
	}()
	var b Bound
	select {
	case b = <-ready:
	case err := <-done:
		t.Fatalf("Ende: %v", err)
	case <-time.After(20 * time.Second):
		t.Fatal("Start")
	}
	if b.Local != "127.0.0.1:"+strconv.Itoa(p) || b.LocalAdmin != "127.0.0.1:"+strconv.Itoa(p+1) {
		t.Errorf("gebunden: %q %q", b.Local, b.LocalAdmin)
	}
	cancel()
	<-done
}

func TestPortBusyTextNamesPortAndHint(t *testing.T) {
	got := portBusyText("127.0.0.1:18080", startPortHint)
	if !strings.Contains(got, "Port 18080 belegt") || !strings.Contains(got, "Port im Admin-Bereich ändern") {
		t.Errorf("Text: %q", got)
	}
}

// --- Admin-Seite ---

func TestAdminLocalPortSaveAndShow(t *testing.T) {
	resetLocalPort(t)
	e := newAdminEnv(t, Client{})
	body := e.get("/admin/pdh").Body.String()
	for _, w := range []string{"Port der lokalen Startseite", "Die lokale Admin-Seite nutzt den Port darüber. Ändern, wenn 8080 auf diesem Rechner gesperrt oder belegt ist.", `name="localPort"`} {
		if !strings.Contains(body, w) {
			t.Errorf("Seite zeigt %q nicht", w)
		}
	}
	if !strings.Contains(body, "8080") || !strings.Contains(body, "8081") {
		t.Errorf("benutzter Port nicht angezeigt")
	}
	r := e.post("/admin/pdh/localport", url.Values{"localPort": {"18080"}})
	if r.Code != 200 {
		t.Fatalf("Speichern: %d %s", r.Code, r.Body.String())
	}
	c, _, _ := e.store.Load()
	if c.Kiosk.LocalPort != 18080 {
		t.Fatalf("gespeichert: %d", c.Kiosk.LocalPort)
	}
	if !strings.Contains(r.Body.String(), "neu gestartet") || !strings.Contains(r.Body.String(), "18080") {
		t.Errorf("Hinweis auf den Neustart fehlt: %s", r.Body.String())
	}
	// Der Grundwert wird als leer gespeichert (die Datei bleibt wie vor der Änderung).
	r = e.post("/admin/pdh/localport", url.Values{"localPort": {"8080"}})
	c, _, _ = e.store.Load()
	if r.Code != 200 || c.Kiosk.LocalPort != 0 {
		t.Fatalf("Grundwert: %d %d", r.Code, c.Kiosk.LocalPort)
	}
	r = e.post("/admin/pdh/localport", url.Values{"localPort": {""}})
	c, _, _ = e.store.Load()
	if r.Code != 200 || c.Kiosk.LocalPort != 0 {
		t.Fatalf("leer: %d %d", r.Code, c.Kiosk.LocalPort)
	}
}

func TestAdminLocalPortRejectsInvalid(t *testing.T) {
	resetLocalPort(t)
	e := newAdminEnv(t, Client{})
	e.product.adminPort = func() int { return 9443 }
	for _, tc := range []struct{ in, want string }{
		{"abc", "Zahl"},
		{"80", "zwischen 1024 und 65534"},
		{"1023", "zwischen 1024 und 65534"},
		{"65535", "zwischen 1024 und 65534"},
		{"-1", "zwischen 1024 und 65534"},
		{"99999999999999999999", "Zahl"},
		{"9443", "Admin-Seite"},
		{"9442", "Admin-Seite"},
		{"8080.5", "Zahl"},
	} {
		r := e.post("/admin/pdh/localport", url.Values{"localPort": {tc.in}})
		if r.Code != 400 || !strings.Contains(r.Body.String(), tc.want) {
			t.Errorf("%q: %d, erwartet %q:\n%s", tc.in, r.Code, tc.want, r.Body.String())
		}
		if c, _, _ := e.store.Load(); c.Kiosk.LocalPort != 0 {
			t.Errorf("%q gespeichert: %d", tc.in, c.Kiosk.LocalPort)
		}
	}
	// Der falsche Wert bleibt im Feld stehen.
	r := e.post("/admin/pdh/localport", url.Values{"localPort": {"80"}})
	if !strings.Contains(r.Body.String(), `value="80"`) {
		t.Errorf("Eingabe nicht erhalten: %s", r.Body.String())
	}
}

// Andere Einstellungen der PDH-Seite lassen den Port unberührt.
func TestAdminPDHSettingsKeepLocalPort(t *testing.T) {
	resetLocalPort(t)
	e := newAdminEnv(t, Client{})
	e.post("/admin/pdh/localport", url.Values{"localPort": {"18080"}})
	if r := e.post("/admin/pdh/settings", url.Values{"url": {"https://pdh.firma.de"}}); r.Code != 200 {
		t.Fatalf("%d %s", r.Code, r.Body.String())
	}
	if c, _, _ := e.store.Load(); c.Kiosk.LocalPort != 18080 {
		t.Fatalf("Port verloren: %d", c.Kiosk.LocalPort)
	}
}

func TestAdminShowsActualPortAndProblem(t *testing.T) {
	resetLocalPort(t)
	localport.Set(18080)
	e := newAdminEnvWith(t, Client{}, func(p *adminProduct) {
		p.local = func() localState {
			return localState{Port: 18080, Problem: "Port 18080 belegt, Startseite nicht verfügbar"}
		}
	})
	body := e.get("/admin/pdh").Body.String()
	if !strings.Contains(body, "18080") || !strings.Contains(body, "18081") || !strings.Contains(body, "Startseite nicht verfügbar") {
		t.Errorf("Anzeige: %s", body)
	}
}

// PDH erfährt den eingestellten Port über eine freiwillige Kopfzeile; beim Grundwert fehlt sie (Anfragen wie bisher).
func TestClientHeadersCarryNonDefaultPortOnly(t *testing.T) {
	resetLocalPort(t)
	req, _ := http.NewRequest(http.MethodGet, "https://pdh.example/x", nil)
	setClientHeaders(req)
	if v := req.Header.Get("X-Terminal-Local-Port"); v != "" {
		t.Errorf("Grundwert: Kopfzeile %q", v)
	}
	localport.Set(18080)
	req, _ = http.NewRequest(http.MethodGet, "https://pdh.example/x", nil)
	setClientHeaders(req)
	if v := req.Header.Get("X-Terminal-Local-Port"); v != "18080" {
		t.Errorf("Kopfzeile %q", v)
	}
}

// Die Startseite prüft den Host mit dem eingestellten Port, auch die Routen für die PDH-Seite (Karte, Tastatur).
func TestStartHandlerChecksConfiguredPort(t *testing.T) {
	resetLocalPort(t)
	localport.Set(18080)
	h, _ := startSetup(t, Client{}, false)
	for _, host := range []string{"127.0.0.1:18080", "localhost:18080"} {
		if w := startReq(t, h, "GET", "/start", host); w.Code != 200 {
			t.Errorf("Host %s: %d", host, w.Code)
		}
	}
	for _, host := range []string{"127.0.0.1:8080", "localhost:8080", "127.0.0.1:18081", "evil:18080", "127.0.0.1"} {
		if w := startReq(t, h, "GET", "/start", host); w.Code != http.StatusForbidden {
			t.Errorf("Host %s: %d, erwartet 403", host, w.Code)
		}
	}
}

// Der gespeicherte Port, der sich nicht binden ließ, löst bei späteren Speichervorgängen keinen Neustart aus.
func TestPortChangedIgnoresFailedPort(t *testing.T) {
	h := &helperLog{}
	s := newPortService(t, h)
	s.badPort.Store(18080)
	c := config.Default()
	c.Kiosk.LocalPort = 18080
	s.apply(c)
	select {
	case err := <-s.exit:
		t.Fatalf("Neustart: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
}

// Beim Start ruft der Dienst den Helfer nur, wenn der Port vom Grundwert abweicht oder die Richtlinie noch auf einem
// anderen Port steht (Vermerk policy-port).
func TestServeSyncsPolicyPortAtStart(t *testing.T) {
	resetLocalPort(t)
	cases := []struct {
		name   string
		port   int    // 0: Grundwert, -1: freies Paar
		marker string // Inhalt des Vermerks, leer: keiner
		want   string // erwarteter Aufruf, leer: keiner
	}{
		{"Grundwert ohne Vermerk", 0, "", ""},
		{"Grundwert, Richtlinie stand auf anderem Port", 0, "18080", "set-local-port 8080"},
		{"Grundwert, Vermerk gleich", 0, "8080", ""},
		{"eingestellter Port", -1, "", "set-local-port %d"},
	}
	for _, tc := range cases {
		h := &helperLog{}
		data := t.TempDir()
		cfg := config.Default()
		if tc.port == -1 {
			cfg.Kiosk.LocalPort = freePortPair(t)
		}
		if err := config.Open(data).Save(cfg); err != nil {
			t.Fatal(err)
		}
		if tc.marker != "" {
			if err := os.WriteFile(filepath.Join(data, policyPortFile), []byte(tc.marker+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		rootFS = t.TempDir()
		ready := make(chan Bound, 1)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			done <- Run(ctx, Options{
				DataDir: data, RunDir: t.TempDir(), AdminAddr: "127.0.0.1:0",
				LocalAddr: "127.0.0.1:0", LocalAdminAddr: "127.0.0.1:0",
				HelperCall: h.call, HardwareID: func() string { return "t" },
				Ready: func(b Bound) { ready <- b },
			})
		}()
		select {
		case <-ready:
		case err := <-done:
			t.Fatalf("%s: Ende: %v", tc.name, err)
		case <-time.After(20 * time.Second):
			t.Fatal("Start")
		}
		if tc.want != "" {
			want := strings.Replace(tc.want, "%d", strconv.Itoa(cfg.LocalPort()), 1)
			waitUntil(t, tc.name, func() bool {
				for _, c := range h.got() {
					if c == want {
						return true
					}
				}
				return false
			})
			waitUntil(t, tc.name+": Vermerk", func() bool {
				m, ok := policyPortMarker(data)
				return ok && m == cfg.LocalPort()
			})
		} else {
			time.Sleep(200 * time.Millisecond)
			for _, c := range h.got() {
				if strings.HasPrefix(c, "set-local-port") {
					t.Errorf("%s: unerwarteter Aufruf %v", tc.name, h.got())
				}
			}
		}
		cancel()
		<-done
	}
	rootFS = "/"
}

// Ein eingestellter Port, der sich nicht binden lässt, darf die HTTPS-Admin-Seite nicht verhindern: Rückfall auf 8080,
// wenn 8080 frei ist (Warnung, Richtlinie wieder auf 8080).
func TestRunFallsBackTo8080WhenConfiguredPortBusy(t *testing.T) {
	resetLocalPort(t)
	probe, err := net.Listen("tcp", "127.0.0.1:8080")
	if err != nil {
		t.Skip("Port 8080 ist auf diesem Rechner nicht frei")
	}
	probe.Close()
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	bp := busy.Addr().(*net.TCPAddr).Port
	data := t.TempDir()
	cfg := config.Default()
	cfg.Kiosk.LocalPort = bp
	if err := config.Open(data).Save(cfg); err != nil {
		t.Fatal(err)
	}
	h := &helperLog{}
	rootFS = t.TempDir()
	t.Cleanup(func() { rootFS = "/" })
	ready := make(chan Bound, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			DataDir: data, RunDir: t.TempDir(), AdminAddr: "127.0.0.1:0",
			HelperCall: h.call, HardwareID: func() string { return "t" },
			Ready: func(b Bound) { ready <- b },
		})
	}()
	var b Bound
	select {
	case b = <-ready:
	case err := <-done:
		t.Fatalf("Ende: %v", err)
	case <-time.After(20 * time.Second):
		t.Fatal("Start")
	}
	if b.Local != "127.0.0.1:8080" || b.Admin == "" || localport.Get() != 8080 {
		t.Errorf("Local %q Admin %q Port %d", b.Local, b.Admin, localport.Get())
	}
	waitUntil(t, "Richtlinie auf 8080", func() bool {
		for _, c := range h.got() {
			if c == "set-local-port 8080" {
				return true
			}
		}
		return false
	})
	cancel()
	<-done
}

// --- Speichern: Bindetest und Richtlinie zuerst ---

func TestAdminLocalPortRejectsPortThatCannotBeOpened(t *testing.T) {
	resetLocalPort(t)
	e := newAdminEnv(t, Client{})
	for _, second := range []bool{false, true} {
		p := freePortPair(t)
		hold := p
		if second {
			hold = p + 1 // P selbst frei, P+1 belegt
		}
		l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(hold))
		if err != nil {
			t.Skip("Port nicht belegbar")
		}
		r := e.post("/admin/pdh/localport", url.Values{"localPort": {strconv.Itoa(p)}})
		l.Close()
		if r.Code != 400 || !strings.Contains(r.Body.String(), "lässt sich auf diesem Rechner nicht öffnen") {
			t.Errorf("P+1 belegt=%v: %d\n%s", second, r.Code, r.Body.String())
		}
		if c, _, _ := e.store.Load(); c.Kiosk.LocalPort != 0 {
			t.Errorf("trotz Fehler gespeichert: %d", c.Kiosk.LocalPort)
		}
	}
}

// Die Ports dieses Laufs gelten als frei (das Terminal hält sie selbst).
func TestAdminLocalPortOwnPortsCountAsFree(t *testing.T) {
	resetLocalPort(t)
	e := newAdminEnv(t, Client{})
	e.product.bindProbe = func(addr string) error { return errors.New("belegt: " + addr) }
	// Der Lauf hält 8080/8081; ein anderer Port wird geprüft und scheitert, der eigene nicht.
	if r := e.post("/admin/pdh/localport", url.Values{"localPort": {"18080"}}); r.Code != 400 {
		t.Fatalf("fremder Port: %d", r.Code)
	}
	localport.Set(18080)
	if r := e.post("/admin/pdh/localport", url.Values{"localPort": {"18080"}}); r.Code != 200 {
		t.Fatalf("eigener Port abgelehnt: %d %s", r.Code, r.Body.String())
	}
}

// Schlägt set-local-port fehl, wird nichts gespeichert.
func TestAdminLocalPortHelperFailureKeepsConfig(t *testing.T) {
	resetLocalPort(t)
	e := newAdminEnv(t, Client{})
	testHelperCall = func(string, string) error { return errors.New("Unbekannter Befehl") }
	t.Cleanup(func() { testHelperCall = nil })
	r := e.post("/admin/pdh/localport", url.Values{"localPort": {"18080"}})
	if r.Code != 400 || !strings.Contains(r.Body.String(), "Browser-Richtlinie konnte nicht angepasst werden – Port nicht geändert (Systemteile aktualisieren)") {
		t.Fatalf("%d\n%s", r.Code, r.Body.String())
	}
	if c, _, _ := e.store.Load(); c.Kiosk.LocalPort != 0 {
		t.Fatalf("gespeichert: %d", c.Kiosk.LocalPort)
	}
	if _, ok := policyPortMarker(e.dir); ok {
		t.Error("Vermerk trotz Fehler")
	}
}

func TestAdminLocalPortCallsHelperBeforeSaving(t *testing.T) {
	resetLocalPort(t)
	e := newAdminEnv(t, Client{})
	var calls []string
	testHelperCall = func(cmd, arg string) error {
		c, _, _ := e.store.Load()
		calls = append(calls, cmd+" "+arg+" gespeichert="+strconv.Itoa(c.Kiosk.LocalPort))
		return nil
	}
	t.Cleanup(func() { testHelperCall = nil })
	if r := e.post("/admin/pdh/localport", url.Values{"localPort": {"18080"}}); r.Code != 200 {
		t.Fatalf("%d %s", r.Code, r.Body.String())
	}
	if len(calls) != 1 || calls[0] != "set-local-port 18080 gespeichert=0" {
		t.Fatalf("Aufrufe: %v", calls)
	}
	if m, ok := policyPortMarker(e.dir); !ok || m != 18080 {
		t.Errorf("Vermerk %d %v", m, ok)
	}
}

func TestAdminShowsFallbackNotice(t *testing.T) {
	resetLocalPort(t)
	e := newAdminEnvWith(t, Client{}, func(p *adminProduct) {
		p.local = func() localState { return localState{Port: 8080, Failed: 18080} }
	})
	e.post("/admin/pdh/localport", url.Values{"localPort": {"18080"}})
	body := e.get("/admin/pdh").Body.String()
	if !strings.Contains(body, "Eingestellter Port 18080 nicht verfügbar – läuft auf 8080") || strings.Contains(body, "gilt nach dem Neustart") {
		t.Errorf("Hinweis: %s", body)
	}
}

// Der Abgleich der Richtlinie läuft vor dem Start des Browsers, nicht daneben.
func TestPolicySyncRunsBeforeBrowserStart(t *testing.T) {
	resetLocalPort(t)
	p := freePortPair(t)
	data := t.TempDir()
	cfg := config.Default()
	cfg.Kiosk.LocalPort = p
	if err := config.Open(data).Save(cfg); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var events []string
	add := func(e string) { mu.Lock(); events = append(events, e); mu.Unlock() }
	browserStartHook = func(string) { add("browser") }
	t.Cleanup(func() { browserStartHook = nil })
	rootFS = t.TempDir()
	t.Cleanup(func() { rootFS = "/" })
	ready := make(chan Bound, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			DataDir: data, RunDir: t.TempDir(), AdminAddr: "127.0.0.1:0",
			HelperCall: func(_ context.Context, cmd, arg string) error {
				time.Sleep(300 * time.Millisecond) // ein langsamer Helfer darf den Browser nicht überholen
				add(cmd + " " + arg)
				return nil
			},
			HardwareID: func() string { return "t" }, Ready: func(b Bound) { ready <- b },
		})
	}()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("Ende: %v", err)
	case <-time.After(20 * time.Second):
		t.Fatal("Start")
	}
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	var ordered []string
	for _, e := range events {
		if strings.HasPrefix(e, "set-local-port") || e == "browser" {
			ordered = append(ordered, e)
		}
	}
	if len(ordered) < 2 || ordered[0] != "set-local-port "+strconv.Itoa(p) || ordered[1] != "browser" {
		t.Fatalf("Reihenfolge: %v", events)
	}
}

// --- Fix-Runde 4: Warnung in jedem Fall, Rücksetzen auf den laufenden Port, Admin-Seite vor dem Abgleich ---

// Scheitert der Abgleich auf 8080 (nach dem Rückfall oder beim Rückwechsel nach einem früheren Port), erscheint die
// Warnung trotzdem: auf der Systemseite und als Problem auf der Seite PDH.
func TestPolicySyncFailureAt8080Warns(t *testing.T) {
	for _, tc := range []struct {
		name     string
		fellBack bool
		marker   string
	}{
		{"nach dem Rückfall", true, ""},
		{"Rückwechsel, Richtlinie stand auf 18080", false, "18080"},
	} {
		h := &helperLog{err: errors.New("Helfer nicht erreichbar")}
		s := newPortService(t, h)
		if tc.fellBack {
			s.badPort.Store(18080) // der Rückfall hat s.port schon auf 8080 gestellt
		}
		if tc.marker != "" {
			if err := os.WriteFile(filepath.Join(s.o.DataDir, policyPortFile), []byte(tc.marker+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if s.port != localport.Default {
			t.Fatalf("%s: Port %d", tc.name, s.port)
		}
		s.syncPolicyAtStart(context.Background(), tc.fellBack)
		if got := h.got(); len(got) != 1 || got[0] != "set-local-port 8080" {
			t.Fatalf("%s: Helferaufrufe %v", tc.name, got)
		}
		warned := false
		for _, w := range s.status().Warnings {
			if strings.Contains(w, "Chromium-Richtlinie erlaubt den Port 8080 der Startseite womöglich nicht") {
				warned = true
			}
		}
		if !warned {
			t.Errorf("%s: keine Warnung: %v", tc.name, s.status().Warnings)
		}
		if p := s.localState().Problem; !strings.Contains(p, "Browser-Richtlinie nicht auf Port 8080 gesetzt") {
			t.Errorf("%s: Problemanzeige %q", tc.name, p)
		}
		e := newAdminEnvWith(t, Client{}, func(p *adminProduct) { p.local = s.localState })
		if body := e.get("/admin/pdh").Body.String(); !strings.Contains(body, "Browser-Richtlinie nicht auf Port 8080 gesetzt") {
			t.Errorf("%s: Seite PDH ohne Problem:\n%s", tc.name, body)
		}
	}
}

// Scheitert das Speichern nach dem Setzen der Richtlinie, geht sie auf den Port zurück, an den die Startseite gerade
// gebunden ist, auch wenn er vom gespeicherten abweicht und kein Rückfall vorliegt.
func TestAdminLocalPortRevertTargetsRunningPort(t *testing.T) {
	resetLocalPort(t)
	e := newAdminEnvWith(t, Client{}, func(p *adminProduct) {
		p.local = func() localState { return localState{Port: 18090} } // gespeichert ist 8080, Failed 0
		p.bindProbe = func(string) error { return nil }
	})
	var calls []string
	testHelperCall = func(cmd, arg string) error {
		calls = append(calls, cmd+" "+arg)
		if len(calls) == 1 { // danach scheitert das Speichern: config.json unlesbar
			if err := os.WriteFile(filepath.Join(e.dir, "config.json"), []byte("{"), 0o600); err != nil {
				t.Error(err)
			}
		}
		return nil
	}
	t.Cleanup(func() { testHelperCall = nil })
	if r := e.post("/admin/pdh/localport", url.Values{"localPort": {"18080"}}); r.Code == http.StatusOK {
		t.Fatalf("Speichern trotz unlesbarer Konfiguration erfolgreich: %s", r.Body.String())
	}
	if len(calls) != 2 || calls[0] != "set-local-port 18080" || calls[1] != "set-local-port 18090" {
		t.Fatalf("Aufrufe: %v", calls)
	}
	if m, ok := policyPortMarker(e.dir); !ok || m != 18090 {
		t.Errorf("Vermerk %d %v", m, ok)
	}
}

// Ein hängender Helfer hält die Admin-Seite über HTTPS nicht auf; der Browser startet spätestens nach der Frist.
func TestHangingPolicySyncDoesNotBlockAdmin(t *testing.T) {
	resetLocalPort(t)
	old := policySyncStartLimit
	policySyncStartLimit = 1500 * time.Millisecond
	t.Cleanup(func() { policySyncStartLimit = old })
	p := freePortPair(t)
	adminPort := freePortPair(t)
	for adminPort == p || adminPort == p+1 || adminPort+1 == p {
		adminPort = freePortPair(t)
	}
	data := t.TempDir()
	cfg := config.Default()
	cfg.Kiosk.LocalPort = p
	if err := config.Open(data).Save(cfg); err != nil {
		t.Fatal(err)
	}
	entered := make(chan time.Time, 1)
	browser := make(chan time.Time, 1)
	browserStartHook = func(string) {
		select {
		case browser <- time.Now():
		default:
		}
	}
	t.Cleanup(func() { browserStartHook = nil })
	rootFS = t.TempDir()
	t.Cleanup(func() { rootFS = "/" })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			DataDir: data, RunDir: t.TempDir(), AdminAddr: "127.0.0.1:" + strconv.Itoa(adminPort),
			HelperCall: func(hctx context.Context, cmd, _ string) error {
				if cmd != helperSetLocalPort {
					return nil
				}
				select {
				case entered <- time.Now():
				default:
				}
				<-hctx.Done() // hängt bis zur Frist
				return hctx.Err()
			},
			HardwareID: func() string { return "t" },
		})
	}()
	var t0 time.Time
	select {
	case t0 = <-entered:
	case err := <-done:
		t.Fatalf("Ende: %v", err)
	case <-time.After(20 * time.Second):
		t.Fatal("Abgleich nicht begonnen")
	}
	c := &http.Client{Timeout: 5 * time.Second,
		Transport:     &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec // Test mit selbst ausgestelltem Zertifikat
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Get("https://127.0.0.1:" + strconv.Itoa(adminPort) + "/admin/")
	if err != nil {
		t.Fatalf("Admin-Seite während des Abgleichs nicht erreichbar: %v", err)
	}
	resp.Body.Close()
	select {
	case <-browser:
		t.Fatal("Browser vor dem Ende des Abgleichs gestartet")
	default:
	}
	select {
	case tb := <-browser:
		if d := tb.Sub(t0); d < policySyncStartLimit-300*time.Millisecond {
			t.Errorf("Browser nach %v gestartet, Frist %v", d, policySyncStartLimit)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Browser nach der Frist nicht gestartet")
	}
	cancel()
	<-done
}
