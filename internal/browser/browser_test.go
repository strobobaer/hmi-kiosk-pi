package browser

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"hmikiosk/internal/client"
	"hmikiosk/internal/coord"
	"hmikiosk/internal/session"
)

type fakeProc struct {
	done    chan error
	mu      sync.Mutex
	stopped int
}

func (p *fakeProc) Done() <-chan error { return p.done }
func (p *fakeProc) Stop(time.Duration) error {
	p.mu.Lock()
	p.stopped++
	p.mu.Unlock()
	return nil
}
func (p *fakeProc) stopCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stopped
}

type fakeRunner struct {
	started chan client.Command
	mu      sync.Mutex
	procs   []*fakeProc
}

func (r *fakeRunner) Start(c client.Command) (client.Process, error) {
	p := &fakeProc{done: make(chan error, 1)}
	r.mu.Lock()
	r.procs = append(r.procs, p)
	r.mu.Unlock()
	r.started <- c
	return p, nil
}

func (r *fakeRunner) proc(i int) *fakeProc {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.procs[i]
}

type fakeClock struct {
	coord.SystemClock
	mu     sync.Mutex
	now    time.Time
	afters chan time.Duration
	fire   chan time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}
func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}
func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.afters <- d
	return c.fire
}

type rig struct {
	s   *Supervisor
	r   *fakeRunner
	clk *fakeClock
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &fakeRunner{started: make(chan client.Command, 16)}
	clk := &fakeClock{now: time.Unix(1000, 0), afters: make(chan time.Duration, 16), fire: make(chan time.Time, 1)}
	s := New(r, "/usr/bin/chromium", "/run/ram", func() int { return 1920 })
	s.clock = clk
	s.logf = func(string, ...any) {}
	return &rig{s, r, clk}
}

func (g *rig) run(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() { g.s.Run(ctx); close(finished) }()
	t.Cleanup(func() { cancel(); <-finished })
}

func (g *rig) nextStart(t *testing.T) client.Command {
	t.Helper()
	select {
	case c := <-g.r.started:
		return c
	case <-time.After(2 * time.Second):
		t.Fatal("kein Start")
	}
	return client.Command{}
}

func (g *rig) noStart(t *testing.T) {
	t.Helper()
	select {
	case <-g.r.started:
		t.Fatal("unerwarteter Start")
	case <-time.After(50 * time.Millisecond):
	}
}

func (g *rig) nextAfter(t *testing.T) time.Duration {
	t.Helper()
	select {
	case d := <-g.clk.afters:
		return d
	case <-time.After(2 * time.Second):
		t.Fatal("kein Wecker")
	}
	return 0
}

func TestTilesArgsIncludeKioskAndRAMProfile(t *testing.T) {
	g := newRig(t)
	g.run(t)
	g.s.SetProfile("labwc")
	g.s.Show(TargetTiles)
	c := g.nextStart(t)
	if c.Path != "/usr/bin/chromium" {
		t.Fatalf("Pfad %q", c.Path)
	}
	for _, w := range []string{"--kiosk", "--user-data-dir=/run/ram/profile-tiles", "--disk-cache-dir=/run/ram/cache",
		"--noerrdialogs", "--disable-infobars", "--touch-events=enabled", "--ozone-platform=wayland",
		"--no-first-run", "--password-store=basic", "--disable-features=Translate", "http://127.0.0.1:8080/"} {
		if !slices.Contains(c.Args, w) {
			t.Errorf("%s fehlt in %v", w, c.Args)
		}
	}
}

func TestBarArgsWindowSizeFromScreen(t *testing.T) {
	g := newRig(t)
	g.run(t)
	g.s.SetProfile("x11")
	g.s.Show(TargetBar)
	c := g.nextStart(t)
	for _, w := range []string{"--app=http://127.0.0.1:8080/bar", "--window-position=0,0", "--window-size=1920,48",
		"--user-data-dir=/run/ram/profile-bar", "--ozone-platform=x11"} {
		if !slices.Contains(c.Args, w) {
			t.Errorf("%s fehlt in %v", w, c.Args)
		}
	}
	if slices.Contains(c.Args, "--kiosk") {
		t.Error("Leiste ohne --kiosk")
	}
}

func TestCrashRestartsWithBackoff(t *testing.T) {
	g := newRig(t)
	g.run(t)
	g.s.Show(TargetTiles)
	g.nextStart(t)
	for i, want := range []time.Duration{1, 2, 4, 8, 16, 30, 30} {
		g.r.proc(i).done <- nil
		if d := g.nextAfter(t); d != want*time.Second {
			t.Fatalf("Versuch %d: %v, erwartet %v", i, d, want*time.Second)
		}
		g.clk.fire <- time.Time{}
		g.nextStart(t)
	}
}

func TestBackoffResetsAfterStableRun(t *testing.T) {
	g := newRig(t)
	g.run(t)
	g.s.Show(TargetTiles)
	g.nextStart(t)
	g.r.proc(0).done <- nil
	if d := g.nextAfter(t); d != time.Second {
		t.Fatal(d)
	}
	g.clk.fire <- time.Time{}
	g.nextStart(t)
	g.clk.advance(61 * time.Second)
	g.r.proc(1).done <- nil
	if d := g.nextAfter(t); d != time.Second {
		t.Fatalf("nach stabilem Lauf %v", d)
	}
}

func TestShowSwitchesAndStopsPrevious(t *testing.T) {
	g := newRig(t)
	g.run(t)
	g.s.Show(TargetTiles)
	g.nextStart(t)
	g.s.Show(TargetBar)
	c := g.nextStart(t)
	if g.r.proc(0).stopCount() != 1 {
		t.Fatal("alter Prozess nicht gestoppt")
	}
	if !slices.ContainsFunc(c.Args, func(a string) bool { return strings.HasPrefix(a, "--app=") }) {
		t.Fatal(c.Args)
	}
	select {
	case d := <-g.clk.afters:
		t.Fatalf("Wechsel gilt nicht als Absturz (Wecker %v)", d)
	case <-time.After(50 * time.Millisecond):
	}
	g.s.Show(TargetNone)
	time.Sleep(50 * time.Millisecond)
	if g.r.proc(1).stopCount() != 1 {
		t.Fatal("TargetNone muss stoppen")
	}
	g.noStart(t)
}

func TestShowSameTargetDoesNotRestart(t *testing.T) {
	g := newRig(t)
	g.run(t)
	g.s.Show(TargetTiles)
	g.nextStart(t)
	g.s.Show(TargetTiles)
	g.noStart(t)
	if g.r.proc(0).stopCount() != 0 {
		t.Fatal("gestoppt")
	}
}

func TestMissingBinaryRetriesOnBackoff(t *testing.T) {
	g := newRig(t)
	g.s.path = ""
	g.run(t)
	g.s.Show(TargetTiles)
	if d := g.nextAfter(t); d != time.Second {
		t.Fatal(d)
	}
	g.clk.fire <- time.Time{}
	if d := g.nextAfter(t); d != 2*time.Second {
		t.Fatal(d)
	}
}

func TestFindChromium(t *testing.T) {
	look := func(have string) func(string) (string, error) {
		return func(n string) (string, error) {
			if n == have {
				return "/usr/bin/" + n, nil
			}
			return "", errors.New("nicht gefunden")
		}
	}
	if p, err := FindChromium(look("chromium-browser")); err != nil || p != "/usr/bin/chromium-browser" {
		t.Fatal(p, err)
	}
	if p, _ := FindChromium(func(n string) (string, error) { return "/x/" + n, nil }); p != "/x/chromium" {
		t.Fatal(p)
	}
	if _, err := FindChromium(look("none")); err == nil {
		t.Fatal("Fehler erwartet")
	}
}

func TestTargetForMapping(t *testing.T) {
	cases := []struct {
		st   session.State
		hide bool
		want Target
	}{
		{session.StateTiles, false, TargetTiles},
		{session.StateRequesting, false, TargetTiles},
		{session.StateConfirming, false, TargetTiles},
		{session.StateReconnecting, false, TargetTiles},
		{session.StateStarting, false, TargetTiles},
		{session.StateConnected, false, TargetBar},
		{session.StateWarned, false, TargetBar},
		{session.StateStopping, false, TargetBar},
		{session.StateConnected, true, TargetNone},
		{session.StateStopping, true, TargetNone},
		{session.StateTiles, true, TargetTiles},
	}
	for _, c := range cases {
		if got := TargetFor(session.Snapshot{State: c.st, HideBar: c.hide}); got != c.want {
			t.Errorf("%s hide=%v: %v, erwartet %v", c.st, c.hide, got, c.want)
		}
	}
}

func TestTouchTestTargetOpensSelftestPage(t *testing.T) {
	g := newRig(t)
	g.run(t)
	g.s.Show(TargetTouchTest)
	c := g.nextStart(t)
	if !slices.Contains(c.Args, "--kiosk") || !slices.Contains(c.Args, "http://127.0.0.1:8080/selftest/touch") {
		t.Fatalf("Argumente: %v", c.Args)
	}
}
