package keyboard

import (
	"context"
	"errors"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"hmikiosk/internal/client"
	"hmikiosk/internal/coord"
)

type fakeProc struct {
	done    chan error
	mu      sync.Mutex
	signals []os.Signal
	stopped int
	sigErr  error
}

func (p *fakeProc) Done() <-chan error { return p.done }
func (p *fakeProc) Stop(time.Duration) error {
	p.mu.Lock()
	p.stopped++
	p.mu.Unlock()
	return nil
}
func (p *fakeProc) Signal(s os.Signal) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.signals = append(p.signals, s)
	return p.sigErr
}
func (p *fakeProc) got() []os.Signal {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.signals)
}

type fakeRunner struct {
	started chan client.Command
	mu      sync.Mutex
	procs   []*fakeProc
	err     error
}

func (r *fakeRunner) Start(c client.Command) (client.Process, error) {
	if r.err != nil {
		r.started <- c
		return nil, r.err
	}
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
	afters chan time.Duration
	fire   chan time.Time
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.afters <- d
	return c.fire
}

type rig struct {
	k   *Supervisor
	r   *fakeRunner
	clk *fakeClock
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &fakeRunner{started: make(chan client.Command, 16)}
	clk := &fakeClock{afters: make(chan time.Duration, 16), fire: make(chan time.Time, 1)}
	k := New(r, func() (string, error) { return "/usr/bin/wvkbd-mobintl", nil }, func() int { return 1080 })
	k.clock = clk
	k.logf = func(string, ...any) {}
	return &rig{k, r, clk}
}

func (g *rig) run(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() { g.k.Run(ctx); close(finished) }()
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

// waitRunning wartet, bis der Steuerer den Prozess kennt.
func (g *rig) waitRunning(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !g.k.running() {
		if time.Now().After(deadline) {
			t.Fatal("Tastatur läuft nicht")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestStartsHiddenWithThirdOfScreenHeight(t *testing.T) {
	g := newRig(t)
	g.k.SetEnabled(true)
	g.run(t)
	c := g.nextStart(t)
	if c.Path != "/usr/bin/wvkbd-mobintl" {
		t.Fatalf("Programm %q", c.Path)
	}
	for _, want := range [][]string{{"--hidden"}, {"-H", "360"}, {"-L", "360"}} {
		if i := slices.Index(c.Args, want[0]); i < 0 || (len(want) == 2 && (i+1 >= len(c.Args) || c.Args[i+1] != want[1])) {
			t.Errorf("Argument %v fehlt in %v", want, c.Args)
		}
	}
}

func TestDisabledStartsNothingAndShowDoesNotFail(t *testing.T) {
	g := newRig(t)
	g.run(t)
	g.noStart(t)
	if err := g.k.Show(); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Show ohne Tastatur: %v", err)
	}
	if err := g.k.Hide(); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Hide ohne Tastatur: %v", err)
	}
	if err := g.k.Toggle(); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Toggle ohne Tastatur: %v", err)
	}
}

func TestWithoutRunNothingPanics(t *testing.T) {
	k := New(&fakeRunner{started: make(chan client.Command, 1)}, nil, func() int { return 0 })
	k.SetEnabled(true)
	if err := k.Show(); err == nil {
		t.Fatal("Show ohne laufendes wvkbd meldet keinen Fehler")
	}
	var nilK *Supervisor
	if err := nilK.Toggle(); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil-Steuerer: %v", err)
	}
}

func TestShowHideToggleSendSignals(t *testing.T) {
	g := newRig(t)
	g.k.SetEnabled(true)
	g.run(t)
	g.nextStart(t)
	g.waitReady(t)
	if err := g.k.Show(); err != nil {
		t.Fatal(err)
	}
	if err := g.k.Hide(); err != nil {
		t.Fatal(err)
	}
	if err := g.k.Toggle(); err != nil { // verborgen → zeigen
		t.Fatal(err)
	}
	if err := g.k.Toggle(); err != nil { // sichtbar → verbergen
		t.Fatal(err)
	}
	want := []os.Signal{sigShow, sigHide, sigShow, sigHide}
	if got := g.r.proc(0).got(); !slices.Equal(got, want) {
		t.Fatalf("Signale %v, erwartet %v", got, want)
	}
}

func TestRestartAfterCrashStartsHidden(t *testing.T) {
	g := newRig(t)
	g.k.SetEnabled(true)
	g.run(t)
	g.nextStart(t)
	g.waitReady(t)
	_ = g.k.Show()
	g.r.proc(0).done <- errors.New("abgestürzt")
	select {
	case d := <-g.clk.afters:
		if d != backoffMin {
			t.Fatalf("Wartezeit %v", d)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("kein Neustart geplant")
	}
	if err := g.k.Show(); err == nil {
		t.Fatal("Show während des Neustarts meldet keinen Fehler")
	}
	g.clk.fire <- time.Now()
	c := g.nextStart(t)
	if !slices.Contains(c.Args, "--hidden") {
		t.Fatal("Neustart nicht verborgen")
	}
	g.waitReady(t)
	if g.k.Visible() {
		t.Fatal("nach Neustart gilt die Tastatur als sichtbar")
	}
}

func TestStartFailureRetries(t *testing.T) {
	g := newRig(t)
	g.r.err = errors.New("fehlt")
	g.k.SetEnabled(true)
	g.run(t)
	g.nextStart(t)
	select {
	case <-g.clk.afters:
	case <-time.After(2 * time.Second):
		t.Fatal("kein neuer Versuch geplant")
	}
}

func TestDisableStopsProcess(t *testing.T) {
	g := newRig(t)
	g.k.SetEnabled(true)
	g.run(t)
	g.nextStart(t)
	g.waitReady(t)
	g.k.SetEnabled(false)
	deadline := time.Now().Add(2 * time.Second)
	for g.k.running() {
		if time.Now().After(deadline) {
			t.Fatal("Tastatur läuft weiter")
		}
		time.Sleep(time.Millisecond)
	}
	p := g.r.proc(0)
	p.mu.Lock()
	stopped := p.stopped
	p.mu.Unlock()
	if stopped == 0 {
		t.Fatal("Prozess nicht beendet")
	}
}

func TestSignalErrorIsReported(t *testing.T) {
	g := newRig(t)
	g.k.SetEnabled(true)
	g.run(t)
	g.nextStart(t)
	g.waitReady(t)
	p := g.r.proc(0)
	p.mu.Lock()
	p.sigErr = errors.New("weg")
	p.mu.Unlock()
	if err := g.k.Show(); err == nil {
		t.Fatal("Fehler beim Signal nicht gemeldet")
	}
	if g.k.Visible() {
		t.Fatal("gilt als sichtbar trotz Fehler")
	}
}

func TestHeightFallback(t *testing.T) {
	if got := height(0); got != 360 {
		t.Fatalf("ohne Bildschirmhöhe %d", got)
	}
	if got := height(1200); got != 400 {
		t.Fatalf("1200 → %d", got)
	}
}

// waitReady wartet auf den Start und lässt die Anlaufzeit ablaufen.
func (g *rig) waitReady(t *testing.T) {
	t.Helper()
	g.waitRunning(t)
	select {
	case d := <-g.clk.afters:
		if d != startGrace {
			t.Fatalf("Anlaufzeit %v, erwartet %v", d, startGrace)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("keine Anlaufzeit geplant")
	}
	g.clk.fire <- time.Now()
	deadline := time.Now().Add(2 * time.Second)
	for !g.k.isReady() {
		if time.Now().After(deadline) {
			t.Fatal("Tastatur wird nicht bereit")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestShowDuringStartGraceIsAppliedAfterwards(t *testing.T) {
	g := newRig(t)
	g.k.SetEnabled(true)
	g.run(t)
	g.nextStart(t)
	g.waitRunning(t)
	// wvkbd hat seine Signalbehandlung vielleicht noch nicht: SIGUSR2 beendete es.
	if err := g.k.Show(); err != nil {
		t.Fatal(err)
	}
	if err := g.k.Hide(); err != nil {
		t.Fatal(err)
	}
	if err := g.k.Show(); err != nil {
		t.Fatal(err)
	}
	if got := g.r.proc(0).got(); len(got) != 0 {
		t.Fatalf("Signale in der Anlaufzeit: %v", got)
	}
	<-g.clk.afters
	g.clk.fire <- time.Now()
	deadline := time.Now().Add(2 * time.Second)
	for len(g.r.proc(0).got()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("gewünschter Zustand nach der Anlaufzeit nicht angewandt")
		}
		time.Sleep(time.Millisecond)
	}
	if got := g.r.proc(0).got(); !slices.Equal(got, []os.Signal{sigShow}) {
		t.Fatalf("Signale %v, erwartet nur zeigen", got)
	}
	if !g.k.Visible() {
		t.Fatal("nicht sichtbar")
	}
}

func TestHideOnlyDuringGraceSendsNothing(t *testing.T) {
	g := newRig(t)
	g.k.SetEnabled(true)
	g.run(t)
	g.nextStart(t)
	g.waitRunning(t)
	_ = g.k.Hide()
	<-g.clk.afters
	g.clk.fire <- time.Now()
	deadline := time.Now().Add(2 * time.Second)
	for !g.k.isReady() {
		if time.Now().After(deadline) {
			t.Fatal("nicht bereit")
		}
		time.Sleep(time.Millisecond)
	}
	if got := g.r.proc(0).got(); len(got) != 0 {
		t.Fatalf("Signale %v", got)
	}
}
