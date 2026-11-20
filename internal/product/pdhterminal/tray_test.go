package pdhterminal

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeTray ersetzt das Symbol: Es merkt sich Zustand und Rückrufe.
type fakeTray struct {
	mu      sync.Mutex
	actions trayActions
	reasons []string
	started chan struct{}
	stopped bool
}

func newFakeTray() *fakeTray { return &fakeTray{started: make(chan struct{})} }

func (f *fakeTray) Start(a trayActions) error {
	f.mu.Lock()
	f.actions = a
	f.mu.Unlock()
	close(f.started)
	return nil
}

func (f *fakeTray) SetState(r string) {
	f.mu.Lock()
	f.reasons = append(f.reasons, r)
	f.mu.Unlock()
}

func (f *fakeTray) Stop() {
	f.mu.Lock()
	f.stopped = true
	f.mu.Unlock()
}

func (f *fakeTray) last() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.reasons) == 0 {
		return "<nie gesetzt>"
	}
	return f.reasons[len(f.reasons)-1]
}

func runWithTray(t *testing.T, ft *fakeTray) (done chan error, cancel context.CancelFunc, bound Bound) {
	t.Helper()
	rootFS = t.TempDir()
	t.Cleanup(func() { rootFS = "/" })
	ready := make(chan Bound, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done = make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			DataDir: t.TempDir(), RunDir: t.TempDir(),
			LocalAddr: "127.0.0.1:0", AdminAddr: "127.0.0.1:0", LocalAdminAddr: freeAddr(t),
			HelperSocket: filepath.Join(t.TempDir(), "kein.sock"),
			HardwareID:   func() string { return "test-0001" },
			Ready:        func(b Bound) { ready <- b },
			tray:         ft,
		})
	}()
	select {
	case bound = <-ready:
	case err := <-done:
		t.Fatalf("Dienst endete früh: %v", err)
	case <-time.After(20 * time.Second):
		t.Fatal("Dienst startet nicht")
	}
	return done, cancel, bound
}

// Ein Quit-Signal aus dem Tray beendet Run mit ErrQuit; das Symbol wird entfernt.
func TestQuitExitCode(t *testing.T) {
	ft := newFakeTray()
	done, cancel, _ := runWithTray(t, ft)
	defer cancel()
	<-ft.started
	ft.mu.Lock()
	quit := ft.actions.Quit
	ft.mu.Unlock()
	quit()
	select {
	case err := <-done:
		if !errors.Is(err, ErrQuit) {
			t.Fatalf("Run lieferte %v, erwartet ErrQuit", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run endet nicht nach Beenden")
	}
	ft.mu.Lock()
	defer ft.mu.Unlock()
	if !ft.stopped {
		t.Error("Symbol nicht entfernt")
	}
}

func TestRequestExitFirstWins(t *testing.T) {
	// Die Anfrage kommt von innen (Update-Aktivierung); die erste Bitte gilt.
	s := &service{exit: make(chan error, 1)}
	s.requestExit(ErrRestart)
	s.requestExit(ErrQuit)
	if err := <-s.exit; !errors.Is(err, ErrRestart) {
		t.Errorf("erste Bitte gilt: %v", err)
	}
}

func TestExitCodes(t *testing.T) {
	if ExitQuit != 3 || ExitRestart != 4 {
		t.Errorf("Rückgabewerte %d %d", ExitQuit, ExitRestart)
	}
}

func TestTrayStartsGreenWithoutWarning(t *testing.T) {
	ft := newFakeTray()
	done, cancel, _ := runWithTray(t, ft)
	<-ft.started
	if r := ft.last(); r != "" && r != "Kartenleser getrennt" {
		t.Errorf("Grund im Normalbetrieb: %q", r)
	}
	cancel()
	<-done
}

func TestSetProblemRecolors(t *testing.T) {
	ft := newFakeTray()
	svc, err := newService(Options{DataDir: t.TempDir(), RunDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.close()
	var tr tray = ft
	svc.tray.Store(&tr)
	svc.setProblem(problemEdge, "Edge fehlt")
	if got := ft.last(); !strings.Contains(got, "Edge fehlt") {
		t.Errorf("Grund: %q", got)
	}
	svc.setProblem(problemAdmin, "Port belegt, Admin-Seite nicht verfügbar")
	if got := ft.last(); !strings.Contains(got, "Edge fehlt") || !strings.Contains(got, "Port belegt") {
		t.Errorf("beide Gründe erwartet: %q", got)
	}
	svc.setProblem(problemEdge, "")
	svc.setProblem(problemAdmin, "")
	if got := ft.last(); got != "" && got != "Kartenleser getrennt" {
		t.Errorf("wieder grün erwartet: %q", got)
	}
}

func TestWaitForEdgeNotifyReportsMissingThenFound(t *testing.T) {
	var events []bool
	calls := 0
	read := func(string) (string, error) {
		calls++
		if calls < 3 {
			return "", errors.New("fehlt")
		}
		return `C:\E\msedge.exe`, nil
	}
	if _, ok := waitForEdgeNotify(context.Background(), time.Millisecond, read, func(m bool) { events = append(events, m) }); !ok {
		t.Fatal("Edge nicht gefunden")
	}
	if len(events) != 2 || !events[0] || events[1] {
		t.Errorf("erwartet fehlt, dann gefunden: %v", events)
	}
}

func TestAdminPageURL(t *testing.T) {
	if got := adminPageURL("[::]:8443", "/admin/"); got != "https://127.0.0.1:8443/admin/" {
		t.Error(got)
	}
	if got := adminPageURL("", adminPathSelftest); got != "https://127.0.0.1:8443/admin/system" {
		t.Error(got)
	}
	if got := adminPageURL("127.0.0.1:9443", "/admin/"); got != "https://127.0.0.1:9443/admin/" {
		t.Error(got)
	}
}

func TestTrayTip(t *testing.T) {
	if trayTip("") != "PDH-Terminal" || trayTip("Edge fehlt") != "PDH-Terminal: Edge fehlt" {
		t.Error("Tooltip")
	}
}

func TestRenderTrayIcon(t *testing.T) {
	for _, size := range []int{16, 32} {
		img := renderTrayIcon(size, trayGreen)
		if a := img.NRGBAAt(0, 0).A; a != 0 {
			t.Errorf("%d: Ecke muss durchsichtig sein, A=%d", size, a)
		}
		// Der Rand des Kreises hat die Farbe, die Mitte des Stamms ist weiß.
		stemX, stemY := size*39/100, size/2
		if c := img.NRGBAAt(stemX, stemY); c.R < 240 || c.G < 240 || c.B < 240 || c.A != 255 {
			t.Errorf("%d: Stamm des P nicht weiß: %+v", size, c)
		}
		if c := img.NRGBAAt(size*15/100, size/2); c != trayGreen {
			t.Errorf("%d: Kreisfläche nicht grün: %+v", size, c)
		}
	}
	if renderTrayIcon(32, trayRed).NRGBAAt(5, 16) != trayRed {
		t.Error("rotes Symbol nicht rot")
	}
}
