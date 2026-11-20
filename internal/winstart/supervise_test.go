package winstart

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"hmikiosk/internal/appcore"
	"hmikiosk/internal/update"
)

// fakeClock ist eine Uhr, die nur der Test weiterstellt.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
	waited []time.Duration // alle angeforderten Wartezeiten
}

type fakeTimer struct {
	at time.Time
	ch chan time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{now: time.Unix(1_000_000, 0)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.waited = append(c.waited, d)
	t := &fakeTimer{at: c.now.Add(d), ch: make(chan time.Time, 1)}
	if d <= 0 {
		t.ch <- c.now
		return t.ch
	}
	c.timers = append(c.timers, t)
	return t.ch
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	rest := c.timers[:0]
	for _, t := range c.timers {
		if !t.at.After(c.now) {
			t.ch <- c.now
		} else {
			rest = append(rest, t)
		}
	}
	c.timers = rest
}

func (c *fakeClock) pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.timers)
}

// pauses liefert die Neustartpausen (ohne Abfragetakt und Gesundfrist).
func (c *fakeClock) pauses() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []time.Duration
	for _, d := range c.waited {
		if d != DefaultPoll && d != DefaultHealthTimeout {
			out = append(out, d)
		}
	}
	return out
}

type fakeProc struct {
	done  chan int
	mu    sync.Mutex
	kills int
}

func newFakeProc() *fakeProc { return &fakeProc{done: make(chan int, 1)} }

func (p *fakeProc) Done() <-chan int { return p.done }
func (p *fakeProc) Kill() {
	p.mu.Lock()
	p.kills++
	p.mu.Unlock()
	select {
	case p.done <- 137:
	default:
	}
}
func (p *fakeProc) exit(code int) { p.done <- code }
func (p *fakeProc) killed() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.kills
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("Zeitüberschreitung: %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

type harness struct {
	t      *testing.T
	clk    *fakeClock
	runDir string
	app    string
	base   Candidate

	mu     sync.Mutex
	starts []Candidate
	procs  []*fakeProc
	healed []Candidate

	// prog erzeugt je Start den Prozess (n zählt ab 1); nil: stirbt sofort mit Code 1 ohne Gesundmeldung.
	prog func(h *harness, n int, c Candidate) *fakeProc

	cancel context.CancelFunc
	result chan error
}

// newHarness legt Grundversion 0.0.1 und die Ordner folders an; verify nimmt alle an.
func newHarness(t *testing.T, folders ...string) *harness {
	h := &harness{t: t, clk: newFakeClock(), runDir: t.TempDir(), app: filepath.Join(t.TempDir(), "app"), result: make(chan error, 1)}
	h.base = Candidate{Version: "0.0.1", Exe: filepath.Join(t.TempDir(), ExeName), Base: true}
	for _, v := range folders {
		os.MkdirAll(filepath.Join(h.app, v), 0o755)
	}
	return h
}

func (h *harness) healthy(version string) {
	if err := appcore.WriteHealthy(h.runDir, version); err != nil {
		h.t.Error(err)
	}
}

func (h *harness) startCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.starts)
}

func (h *harness) startedVersions() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, c := range h.starts {
		out = append(out, c.Version)
	}
	return out
}

func (h *harness) proc(i int) *fakeProc {
	h.mu.Lock()
	defer h.mu.Unlock()
	if i >= len(h.procs) {
		return nil
	}
	return h.procs[i]
}

func (h *harness) run() {
	cfg := Config{
		Choose: func() (Candidate, error) {
			cands := append(Discover(filepath.Dir(h.base.Exe), "0.0.1", h.app), h.base)
			// Discover findet die Exe der Grundversion nicht (Datei fehlt); h.base ist gesetzt.
			return Choose(dedupe(cands), func(Candidate) error { return nil })
		},
		Start: func(c Candidate) (Proc, error) {
			h.mu.Lock()
			n := len(h.starts) + 1
			h.starts = append(h.starts, c)
			h.mu.Unlock()
			var p *fakeProc
			if h.prog != nil {
				p = h.prog(h, n, c)
			} else {
				p = newFakeProc()
				p.exit(1)
			}
			h.mu.Lock()
			h.procs = append(h.procs, p)
			h.mu.Unlock()
			return p, nil
		},
		RunDir:    h.runDir,
		Clock:     h.clk,
		MarkBad:   MarkBad,
		OnHealthy: func(c Candidate) { h.mu.Lock(); h.healed = append(h.healed, c); h.mu.Unlock() },
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	go func() { h.result <- Supervise(ctx, cfg) }()
	h.t.Cleanup(func() {
		cancel()
		select {
		case <-h.result:
		case <-time.After(5 * time.Second):
			h.t.Error("Supervise endet nicht")
		}
	})
}

func dedupe(cs []Candidate) []Candidate {
	seen := map[string]bool{}
	var out []Candidate
	for _, c := range cs {
		k := c.Version
		if c.Base {
			k += "/base"
		}
		if !seen[k] {
			seen[k] = true
			out = append(out, c)
		}
	}
	return out
}

// healthyThenExit: Das Programm meldet sich gesund und endet mit code.
func healthyThenExit(code int) func(*harness, int, Candidate) *fakeProc {
	return func(h *harness, _ int, c Candidate) *fakeProc {
		h.healthy(c.Version)
		p := newFakeProc()
		p.exit(code)
		return p
	}
}

func TestSuperviseHealthTimeout(t *testing.T) {
	h := newHarness(t, "0.0.2")
	h.healthy("0.0.2") // eine alte Marke darf nicht als Gesundmeldung zählen
	h.prog = func(h *harness, n int, c Candidate) *fakeProc {
		p := newFakeProc()
		if n == 2 { // die Grundversion läuft gesund weiter
			h.healthy(c.Version)
		}
		return p
	}
	h.run()
	waitFor(t, "erster Start", func() bool { return h.startCount() == 1 && h.clk.pending() >= 2 })
	if v := h.startedVersions()[0]; v != "0.0.2" {
		t.Fatalf("erster Start %s", v)
	}
	h.clk.Advance(DefaultHealthTimeout)
	waitFor(t, "zweiter Start", func() bool { return h.startCount() == 2 })
	if got := h.startedVersions(); got[1] != "0.0.1" {
		t.Errorf("Rückfall auf %v, erwartet die Grundversion", got)
	}
	if _, err := os.Stat(filepath.Join(h.app, "0.0.2", update.BadMarker)); err != nil {
		t.Errorf("bad-Datei fehlt: %v", err)
	}
	if h.proc(0).killed() == 0 {
		t.Error("ungesunder Prozess wurde nicht beendet")
	}
	if p := h.clk.pauses(); len(p) != 0 {
		t.Errorf("Rückfall braucht keine Pause, war %v", p)
	}
}

// Meldet sich das Programm nach der Gesundfrist gesund, ändert sich nichts mehr: kein Beenden, keine Markierung.
func TestSuperviseHealthyStaysRunning(t *testing.T) {
	h := newHarness(t, "0.0.2")
	h.prog = func(h *harness, n int, c Candidate) *fakeProc { return newFakeProc() }
	h.run()
	waitFor(t, "Start", func() bool { return h.startCount() == 1 && h.clk.pending() >= 2 })
	h.healthy("0.0.2")
	h.clk.Advance(DefaultPoll)
	waitFor(t, "Gesundmeldung erkannt", func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return len(h.healed) == 1
	})
	h.clk.Advance(30 * time.Minute)
	time.Sleep(20 * time.Millisecond)
	if h.proc(0).killed() != 0 || h.startCount() != 1 {
		t.Error("gesunder Prozess wurde angetastet")
	}
	if _, err := os.Stat(filepath.Join(h.app, "0.0.2", update.BadMarker)); err == nil {
		t.Error("gesunde Version als schlecht markiert")
	}
}

// Eine Marke mit anderer Version zählt nicht als gesund.
func TestSuperviseWrongVersionIsNotHealthy(t *testing.T) {
	h := newHarness(t, "0.0.2")
	h.prog = func(h *harness, n int, c Candidate) *fakeProc {
		if n == 1 {
			h.healthy("0.0.9")
		}
		return newFakeProc()
	}
	h.run()
	waitFor(t, "Start", func() bool { return h.startCount() == 1 && h.clk.pending() >= 2 })
	h.clk.Advance(DefaultHealthTimeout)
	waitFor(t, "Rückfall", func() bool { return h.startCount() == 2 })
}

func TestSuperviseRestartBackoff(t *testing.T) {
	h := newHarness(t)
	h.prog = healthyThenExit(1)
	h.run()
	want := []time.Duration{5 * time.Second, 30 * time.Second, 5 * time.Minute, 5 * time.Minute}
	for i, d := range want {
		waitFor(t, "Pause", func() bool { return len(h.clk.pauses()) == i+1 })
		if got := h.clk.pauses()[i]; got != d {
			t.Fatalf("Pause %d war %v, erwartet %v", i+1, got, d)
		}
		if h.startCount() != i+1 {
			t.Fatalf("während der Pause %d lief %d Starts", i+1, h.startCount())
		}
		h.clk.Advance(d)
	}
	waitFor(t, "fünfter Start", func() bool { return h.startCount() == 5 })
}

// Ein Lauf über 10 Minuten setzt die Pausen zurück.
func TestSuperviseBackoffResetAfterLongRun(t *testing.T) {
	h := newHarness(t)
	h.prog = func(h *harness, n int, c Candidate) *fakeProc {
		h.healthy(c.Version)
		p := newFakeProc()
		if n < 3 {
			p.exit(1)
		}
		return p
	}
	h.run()
	waitFor(t, "Pause 1", func() bool { return len(h.clk.pauses()) == 1 })
	h.clk.Advance(5 * time.Second)
	waitFor(t, "Pause 2", func() bool { return len(h.clk.pauses()) == 2 })
	h.clk.Advance(30 * time.Second)
	waitFor(t, "dritter Start", func() bool { return h.startCount() == 3 && h.proc(2) != nil })
	h.clk.Advance(11 * time.Minute)
	h.proc(2).exit(1)
	waitFor(t, "Pause 3", func() bool { return len(h.clk.pauses()) == 3 })
	if got := h.clk.pauses()[2]; got != 5*time.Second {
		t.Errorf("Pause nach langem Lauf %v, erwartet 5 s", got)
	}
}

func TestSuperviseQuit(t *testing.T) {
	h := newHarness(t)
	h.prog = healthyThenExit(ExitQuit)
	h.run()
	select {
	case err := <-h.result:
		if err != nil {
			t.Fatalf("Supervise: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Starter endet nicht bei Rückgabewert 3")
	}
	if h.startCount() != 1 {
		t.Errorf("%d Starts, erwartet 1", h.startCount())
	}
	h.result <- nil // für das Aufräumen im Cleanup
}

func TestSuperviseRestartCode(t *testing.T) {
	h := newHarness(t)
	h.prog = func(h *harness, n int, c Candidate) *fakeProc {
		h.healthy(c.Version)
		p := newFakeProc()
		if n < 3 {
			p.exit(ExitRestart)
		}
		return p
	}
	h.run()
	waitFor(t, "drei Starts ohne Uhr", func() bool { return h.startCount() == 3 })
	if p := h.clk.pauses(); len(p) != 0 {
		t.Errorf("Neustart (Rückgabewert 4) hat gewartet: %v", p)
	}
}

// Beendet sich das Programm mit Neustartwunsch, bevor es gesund war, ist das ein Fehlstart mit Pause (keine Endlosschleife).
func TestSuperviseRestartCodeBeforeHealthyPauses(t *testing.T) {
	h := newHarness(t)
	h.prog = func(h *harness, n int, c Candidate) *fakeProc {
		p := newFakeProc()
		p.exit(ExitRestart)
		return p
	}
	h.run()
	waitFor(t, "Pause", func() bool { return len(h.clk.pauses()) == 1 })
	if h.startCount() != 1 {
		t.Errorf("%d Starts während der Pause", h.startCount())
	}
}

// Stürzt eine Version dreimal ab, ohne je gesund zu sein, gilt sie als schlecht und die nächstniedrigere startet.
func TestSuperviseCrashLoopMarksBad(t *testing.T) {
	h := newHarness(t, "0.0.2")
	h.prog = func(h *harness, n int, c Candidate) *fakeProc {
		p := newFakeProc()
		if c.Base {
			h.healthy(c.Version)
			return p
		}
		p.exit(1)
		return p
	}
	h.run()
	waitFor(t, "Pause 1", func() bool { return len(h.clk.pauses()) == 1 })
	h.clk.Advance(5 * time.Second)
	waitFor(t, "Pause 2", func() bool { return len(h.clk.pauses()) == 2 })
	h.clk.Advance(30 * time.Second)
	waitFor(t, "Rückfall", func() bool { return h.startCount() == 4 })
	got := h.startedVersions()
	if got[0] != "0.0.2" || got[1] != "0.0.2" || got[2] != "0.0.2" || got[3] != "0.0.1" {
		t.Errorf("Starts %v", got)
	}
	if _, err := os.Stat(filepath.Join(h.app, "0.0.2", update.BadMarker)); err != nil {
		t.Errorf("bad-Datei fehlt: %v", err)
	}
}

func TestSuperviseStopKillsChild(t *testing.T) {
	h := newHarness(t)
	h.prog = func(h *harness, n int, c Candidate) *fakeProc {
		h.healthy(c.Version)
		return newFakeProc()
	}
	h.run()
	waitFor(t, "Start", func() bool { return h.startCount() == 1 && h.clk.pending() >= 2 })
	h.clk.Advance(DefaultPoll)
	waitFor(t, "gesund", func() bool { h.mu.Lock(); defer h.mu.Unlock(); return len(h.healed) == 1 })
	h.cancel()
	select {
	case err := <-h.result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Supervise endet nicht")
	}
	if h.proc(0).killed() == 0 {
		t.Error("Prozess lief nach dem Ende des Starters weiter")
	}
	h.result <- nil
}

// Gibt es keinen startbaren Kandidaten, wartet der Starter (Pause) und versucht es erneut.
func TestSuperviseNoCandidateWaits(t *testing.T) {
	h := newHarness(t)
	calls := 0
	var mu sync.Mutex
	cfg := Config{
		Choose: func() (Candidate, error) { mu.Lock(); calls++; mu.Unlock(); return Candidate{}, os.ErrNotExist },
		Start:  func(Candidate) (Proc, error) { t.Error("Start ohne Kandidat"); return nil, nil },
		RunDir: h.runDir, Clock: h.clk,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Supervise(ctx, cfg) }()
	waitFor(t, "Pause", func() bool { return len(h.clk.pauses()) == 1 })
	h.clk.Advance(5 * time.Second)
	waitFor(t, "zweiter Versuch", func() bool { mu.Lock(); defer mu.Unlock(); return calls == 2 })
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
