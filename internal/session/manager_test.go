package session

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hmikiosk/internal/client"
	"hmikiosk/internal/config"
	"hmikiosk/internal/coord"
	"hmikiosk/internal/rfb"
)

// guard begrenzt nur das Warten auf einen Fehlschlag; die Abläufe selbst
// laufen auf der simulierten Uhr.
const guard = 10 * time.Second

// fakeClock kennt wie der Manager genau einen aktiven Wecker.
type fakeClock struct {
	mu    sync.Mutex
	now   time.Time
	ch    chan time.Time
	at    time.Time
	arms  int
	armed chan struct{}
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 11, 5, 8, 0, 0, 0, time.UTC), armed: make(chan struct{}, 1)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ch = make(chan time.Time, 1)
	c.at = c.now.Add(d)
	c.arms++
	select {
	case c.armed <- struct{}{}:
	default:
	}
	return c.ch
}

type eventLog struct {
	mu sync.Mutex
	l  []string
}

func (e *eventLog) add(s string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.l = append(e.l, s)
}

func (e *eventLog) all() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.l)
}

func (e *eventLog) count(s string) int {
	n := 0
	for _, x := range e.all() {
		if x == s {
			n++
		}
	}
	return n
}

// before prüft, dass das letzte a vor dem letzten b liegt.
func (e *eventLog) before(a, b string) bool {
	l := e.all()
	ia, ib := -1, -1
	for i, x := range l {
		if x == a {
			ia = i
		}
		if x == b {
			ib = i
		}
	}
	return ia >= 0 && ib >= 0 && ia < ib
}

type fakeCoord struct {
	log    *eventLog
	events chan coord.Event

	mu          sync.Mutex
	results     []coord.Result
	gate        chan struct{}
	takeoverErr error
	taken       []coord.Decision
}

func (c *fakeCoord) Request(ctx context.Context, hmi string) coord.Result {
	c.log.add("request:" + hmi)
	c.mu.Lock()
	gate := c.gate
	var res coord.Result
	if len(c.results) > 0 {
		res, c.results = c.results[0], c.results[1:]
	}
	c.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
		}
	}
	return res
}

func (c *fakeCoord) Takeover(_ context.Context, hmi string, d coord.Decision) error {
	c.log.add("takeover:" + hmi)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.taken = append(c.taken, d)
	return c.takeoverErr
}

func (c *fakeCoord) Hold(hmi, _ string)         { c.log.add("hold:" + hmi) }
func (c *fakeCoord) Release(hmi string)         { c.log.add("release:" + hmi) }
func (c *fakeCoord) Events() <-chan coord.Event { return c.events }

func (c *fakeCoord) queue(r ...coord.Result) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.results = append(c.results, r...)
}

type fakeProc struct {
	done     chan error
	once     sync.Once
	stopGate chan struct{}
	stops    atomic.Int32
	grace    atomic.Int64
}

func (p *fakeProc) Done() <-chan error { return p.done }

func (p *fakeProc) exit(err error) {
	p.once.Do(func() {
		p.done <- err
		close(p.done)
	})
}

func (p *fakeProc) Stop(g time.Duration) error {
	p.stops.Add(1)
	p.grace.Store(int64(g))
	if p.stopGate != nil {
		<-p.stopGate
	}
	p.exit(nil)
	return nil
}

type fakeRunner struct {
	log *eventLog

	mu       sync.Mutex
	procs    []*fakeProc
	err      error
	stopGate chan struct{}
}

func (r *fakeRunner) Start(client.Command) (client.Process, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return nil, r.err
	}
	r.log.add("start")
	p := &fakeProc{done: make(chan error, 1), stopGate: r.stopGate}
	r.procs = append(r.procs, p)
	return p, nil
}

func (r *fakeRunner) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.procs)
}

func (r *fakeRunner) last() *fakeProc {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.procs[len(r.procs)-1]
}

type fakeProfile struct {
	mu    sync.Mutex
	specs []client.Spec
	dirs  []string
	err   error
}

func (p *fakeProfile) Name() string { return "fake" }

func (p *fakeProfile) Prepare(dir string, s client.Spec) (client.Command, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.specs = append(p.specs, s)
	p.dirs = append(p.dirs, dir)
	return client.Command{Path: "fake"}, p.err
}

func (p *fakeProfile) last() client.Spec {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.specs[len(p.specs)-1]
}

type harness struct {
	t       *testing.T
	m       *Manager
	clk     *fakeClock
	coord   *fakeCoord
	runner  *fakeRunner
	profile *fakeProfile
	log     *eventLog
	web     *fakeProfile // Web-Kacheln; nil heißt: nicht verfügbar
	sub     <-chan Snapshot
	tcp     atomic.Bool

	mu          sync.Mutex
	cfg         config.Config
	probe       rfb.ProbeResult
	passwordErr error
	password    string
	ramErr      error
	probes      int
	marked      []string
	markedEnc   []string
	cleanups    int
	cancelRun   context.CancelFunc
	onEnd       func(Ended)
	blocked     atomic.Pointer[string]
	runDone     chan struct{}
}

func baseConfig() config.Config {
	cfg := config.Default()
	cfg.Kiosk.InactivityMinutes = 0
	cfg.HMIs = []config.HMI{
		{ID: "a", Name: "Presse", Host: "10.0.0.1", Port: 5900, PasswordEnc: "enc-a"},
		{ID: "b", Name: "Ofen", Host: "10.0.0.2", Port: 5901, PasswordEnc: "enc-b"},
	}
	return cfg
}

// newHarness baut den Manager; mutate darf Fälschungen vor dem Start belegen.
func newHarness(t *testing.T, mutate func(*harness)) *harness {
	t.Helper()
	log := &eventLog{}
	h := &harness{
		t:        t,
		clk:      newFakeClock(),
		log:      log,
		coord:    &fakeCoord{log: log, events: make(chan coord.Event, 8)},
		runner:   &fakeRunner{log: log},
		profile:  &fakeProfile{},
		web:      &fakeProfile{},
		cfg:      baseConfig(),
		probe:    rfb.ProbeResult{Reachable: true, AuthOK: true},
		password: "geheim",
		runDone:  make(chan struct{}),
	}
	if mutate != nil {
		mutate(h)
	}
	h.m = New(Deps{
		Coord:   h.coord,
		Runner:  h.runner,
		Profile: func() client.Profile { return h.profile },
		Web: func() client.Profile {
			if h.web == nil {
				return nil
			}
			return h.web
		},
		TCP: func(string, int) bool { return h.tcp.Load() },
		RAMDir: func() (string, func(), error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			if h.ramErr != nil {
				return "", nil, h.ramErr
			}
			return "/ram/x", func() {
				h.mu.Lock()
				defer h.mu.Unlock()
				h.cleanups++
				h.log.add("cleanup")
			}, nil
		},
		Clock: h.clk,
		Config: func() config.Config {
			h.mu.Lock()
			defer h.mu.Unlock()
			return h.cfg
		},
		Password: func(config.HMI) (string, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			return h.password, h.passwordErr
		},
		Screen: func() (int, int) { return 1920, 1080 },
		OnEnd:  h.onEnd,
		MarkAuthFailed: func(id, passwordEnc string) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.marked = append(h.marked, id)
			h.markedEnc = append(h.markedEnc, passwordEnc)
			h.cfg.HMIs = slices.Clone(h.cfg.HMIs)
			for i := range h.cfg.HMIs {
				if h.cfg.HMIs[i].ID == id {
					h.cfg.HMIs[i].AuthFailed = true
				}
			}
		},
		Blocked: func() string {
			if s := h.blocked.Load(); s != nil {
				return *s
			}
			return ""
		},
		Probe: func(context.Context, config.HMI, string) rfb.ProbeResult {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.probes++
			return h.probe
		},
	})
	sub, unsub := h.m.Subscribe()
	h.sub = sub
	ctx, cancel := context.WithCancel(context.Background())
	h.cancelRun = cancel
	go func() {
		defer close(h.runDone)
		h.m.Run(ctx)
	}()
	t.Cleanup(func() {
		h.shutdown()
		unsub()
	})
	h.sync()
	return h
}

func (h *harness) shutdown() {
	h.t.Helper()
	h.cancelRun()
	select {
	case <-h.runDone:
	case <-time.After(guard):
		h.t.Fatal("Run endet nicht")
	}
}

// sync kehrt zurück, wenn die Zustands-Goroutine wieder im select steht und
// ihren Wecker gestellt hat: Der zweite Befehl wird erst danach angenommen.
func (h *harness) sync() {
	_ = h.m.do(func() error { return nil })
	_ = h.m.do(func() error { return nil })
}

// advance lässt die Zeit von Wecker zu Wecker laufen und wartet nach jedem,
// bis er verarbeitet ist; der Manager stellt danach stets einen neuen.
func (h *harness) advance(d time.Duration) {
	h.t.Helper()
	h.sync()
	c := h.clk
	c.mu.Lock()
	target := c.now.Add(d)
	c.mu.Unlock()
	for {
		c.mu.Lock()
		if c.ch == nil || c.at.After(target) {
			c.now = target
			c.mu.Unlock()
			h.sync()
			return
		}
		c.now = c.at
		ch, arms := c.ch, c.arms
		c.ch = nil
		ch <- c.now
		c.mu.Unlock()
		h.awaitArm(arms)
		h.sync()
	}
}

func (h *harness) awaitArm(arms int) {
	h.t.Helper()
	deadline := time.After(guard)
	for {
		h.clk.mu.Lock()
		n := h.clk.arms
		h.clk.mu.Unlock()
		if n > arms {
			return
		}
		select {
		case <-h.clk.armed:
		case <-deadline:
			h.t.Fatal("Manager stellt keinen neuen Wecker")
		}
	}
}

func (h *harness) waitFor(what string, ok func(Snapshot) bool) Snapshot {
	h.t.Helper()
	deadline := time.After(guard)
	var last Snapshot
	for {
		select {
		case s := <-h.sub:
			last = s
			if ok(s) {
				h.sync()
				return s
			}
		case <-deadline:
			h.t.Fatalf("warte auf %s, zuletzt %+v, Ablauf %v", what, last, h.log.all())
		}
	}
}

func (h *harness) waitState(want State) Snapshot {
	h.t.Helper()
	return h.waitFor(string(want), func(s Snapshot) bool { return s.State == want })
}

func (h *harness) waitNotice(state State, part string) Snapshot {
	h.t.Helper()
	return h.waitFor(fmt.Sprintf("%s mit %q", state, part), func(s Snapshot) bool {
		return s.State == state && strings.Contains(s.Notice, part)
	})
}

func (h *harness) open(id string) {
	h.t.Helper()
	if err := h.m.Open(id); err != nil {
		h.t.Fatalf("Open(%s): %v", id, err)
	}
}

func (h *harness) connect(id string) *fakeProc {
	h.t.Helper()
	h.open(id)
	h.waitState(StateStarting)
	h.tcp.Store(true)
	h.advance(PollEvery)
	h.waitState(StateConnected)
	return h.runner.last()
}

func (h *harness) setConfig(f func(*config.Config)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cfg.HMIs = slices.Clone(h.cfg.HMIs)
	f(&h.cfg)
}

func (h *harness) cleanupCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.cleanups
}

func busy(name string) coord.Result {
	return coord.Result{Decision: coord.Decision{Outcome: coord.Reject, Holder: &coord.Holder{Terminal: "t2", Name: name}}}
}

func takeover(name string, warn time.Duration) coord.Result {
	return coord.Result{Decision: coord.Decision{Outcome: coord.TakeOver, Warn: warn, Holder: &coord.Holder{Terminal: "t2", Name: name}}}
}

func askConfirm(name string) coord.Result {
	return coord.Result{Decision: coord.Decision{Outcome: coord.AskConfirm, Warn: coord.TakeoverWarn, Holder: &coord.Holder{Terminal: "t2", Name: name}}}
}

// --- Grundablauf ---

func TestOpenFreeHMIStartsClientAndConnects(t *testing.T) {
	h := newHarness(t, nil)
	h.open("a")
	s := h.waitState(StateStarting)
	if s.HMI != "a" || s.HMIName != "Presse" {
		t.Fatalf("Snapshot %+v", s)
	}
	// Erst die ESTABLISHED-Verbindung macht die Sitzung „verbunden“.
	h.advance(2 * PollEvery)
	if got := h.m.Snapshot().State; got != StateStarting {
		t.Fatalf("Zustand %s ohne TCP", got)
	}
	h.tcp.Store(true)
	h.advance(PollEvery)
	h.waitState(StateConnected)
	if !h.log.before("hold:a", "start") {
		t.Fatalf("Hold nicht vor Start: %v", h.log.all())
	}
	spec := h.profile.last()
	if spec.Host != "10.0.0.1" || spec.Port != 5900 || spec.Password != "geheim" || spec.ViewOnly {
		t.Fatalf("Spec %+v", spec)
	}
}

func TestDoubleOpenStartsOnlyOneClient(t *testing.T) {
	h := newHarness(t, nil)
	h.open("a")
	h.open("a")
	h.open("b")
	h.waitState(StateStarting)
	h.sync()
	if n := h.runner.count(); n != 1 {
		t.Fatalf("%d Starts", n)
	}
	if n := h.log.count("request:a"); n != 1 {
		t.Fatalf("%d Anfragen", n)
	}
}

func TestOpenWhileStartingIsIgnored(t *testing.T) {
	h := newHarness(t, nil)
	h.open("a")
	h.waitState(StateStarting)
	h.open("a")
	h.open("b")
	h.sync()
	if n := h.runner.count(); n != 1 {
		t.Fatalf("%d Starts", n)
	}
	if s := h.m.Snapshot(); s.HMI != "a" || s.State != StateStarting {
		t.Fatalf("Snapshot %+v", s)
	}
}

func TestOpenWhileConnectedDoesNotRequestAgain(t *testing.T) {
	h := newHarness(t, nil)
	h.connect("a")
	h.coord.queue(busy("Terminal 2"))
	h.open("a")
	h.sync()
	if n := h.log.count("request:a"); n != 1 {
		t.Fatalf("%d Anfragen: ein Halter darf sich nicht selbst ablehnen lassen", n)
	}
	if s := h.m.Snapshot(); s.State != StateConnected {
		t.Fatalf("Zustand %s", s.State)
	}
}

func TestOpenUnknownHMIReturnsError(t *testing.T) {
	h := newHarness(t, nil)
	if err := h.m.Open("gibtsnicht"); err == nil {
		t.Fatal("kein Fehler")
	}
	if s := h.m.Snapshot(); s.State != StateTiles || s.Notice == "" {
		t.Fatalf("Snapshot %+v", s)
	}
}

func TestBackDuringRequestingReturnsToTilesWithoutClient(t *testing.T) {
	gate := make(chan struct{})
	defer close(gate)
	h := newHarness(t, func(h *harness) { h.coord.gate = gate })
	h.open("a")
	h.m.Back()
	if s := h.m.Snapshot(); s.State != StateTiles {
		t.Fatalf("Zustand %s", s.State)
	}
	h.sync()
	if n := h.runner.count(); n != 0 {
		t.Fatalf("%d Starts", n)
	}
	if h.log.count("release:a") == 0 {
		t.Fatalf("kein Release: %v", h.log.all())
	}
}

func TestBackDuringStartStopsClientCleanly(t *testing.T) {
	stopGate := make(chan struct{})
	h := newHarness(t, func(h *harness) { h.runner.stopGate = stopGate })
	h.open("a")
	h.waitState(StateStarting)
	h.m.Back()
	h.waitState(StateStopping)
	p := h.runner.last()

	// Solange der Client lebt, gibt es weder Kacheln noch einen zweiten Client.
	h.open("a")
	h.m.Back()
	h.advance(10 * time.Second)
	if s := h.m.Snapshot(); s.State != StateStopping {
		t.Fatalf("Zustand %s vor Prozessende", s.State)
	}
	if n := h.runner.count(); n != 1 {
		t.Fatalf("%d Starts", n)
	}
	if h.cleanupCount() != 0 {
		t.Fatal("RAM-Ordner vor Prozessende gelöscht")
	}
	close(stopGate)
	s := h.waitState(StateTiles)
	if s.Notice != "" {
		t.Fatalf("Hinweis nach Zurück: %q", s.Notice)
	}
	if got := time.Duration(p.grace.Load()); got != StopGrace {
		t.Fatalf("Frist %v", got)
	}
	if h.cleanupCount() != 1 || h.log.count("release:a") != 1 {
		t.Fatalf("Aufräumen: %v", h.log.all())
	}
}

func TestClientCrashReturnsToTilesWithNotice(t *testing.T) {
	h := newHarness(t, nil)
	p := h.connect("a")
	p.exit(errors.New("exit status 1"))
	s := h.waitState(StateTiles)
	if !strings.Contains(s.Notice, "Presse") {
		t.Fatalf("Hinweis %q", s.Notice)
	}
	if h.cleanupCount() != 1 || h.log.count("release:a") != 1 {
		t.Fatalf("Aufräumen: %v", h.log.all())
	}
	if p.stops.Load() != 0 {
		t.Fatal("beendeter Client gestoppt")
	}
}

func TestSessionNumberIncrementsPerStart(t *testing.T) {
	h := newHarness(t, nil)
	p := h.connect("a")
	first := h.profile.last().SessionN
	p.exit(nil)
	h.waitState(StateTiles)
	h.open("a")
	h.waitState(StateStarting)
	if second := h.profile.last().SessionN; second != first+1 {
		t.Fatalf("SessionN %d, dann %d", first, second)
	}
}

func TestGeometryKeepsBarForTouchSessionsEvenWithHideBar(t *testing.T) {
	h := newHarness(t, nil)
	h.open("a")
	s := h.waitState(StateStarting)
	if p := h.profile.last(); p.X != 0 || p.Y != 48 || p.W != 1920 || p.H != 1080-48 || s.HideBar {
		t.Fatalf("mit Leiste %+v, Snapshot %+v", p, s)
	}
	h.m.Back()
	h.waitState(StateTiles)
	// Validate lässt HideBar nur mit Daueranzeige zu; ein anderes HMI per Touch zeigt die Leiste trotzdem.
	h.setConfig(func(c *config.Config) { c.Kiosk.HideBar = true })
	h.open("b")
	s = h.waitState(StateStarting)
	if p := h.profile.last(); p.Y != 48 || p.H != 1080-48 || p.FullScreen || s.HideBar {
		t.Fatalf("Touch-Sitzung bei HideBar ohne Leiste: %+v, Snapshot %+v", p, s)
	}
}

func TestGeometryHidesBarOnlyForAutomaticPermanentDisplay(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.cfg.Kiosk.PermanentHMI = "a"
		h.cfg.Kiosk.HideBar = true
	})
	s := h.waitState(StateStarting)
	if p := h.profile.last(); p.X != 0 || p.Y != 0 || p.W != 1920 || p.H != 1080 || !p.FullScreen || !s.HideBar {
		t.Fatalf("Daueranzeige ohne Leiste: %+v, Snapshot %+v", p, s)
	}
	h.m.Back()
	h.waitState(StateTiles)
	h.open("a")
	s = h.waitState(StateStarting)
	if p := h.profile.last(); p.Y != 48 || p.H != 1080-48 || p.FullScreen || s.HideBar {
		t.Fatalf("Touch auf die Kachel der Daueranzeige ohne Leiste: %+v, Snapshot %+v", p, s)
	}
}

func TestShutdownStopsClientAndCleansUp(t *testing.T) {
	h := newHarness(t, nil)
	p := h.connect("a")
	h.shutdown()
	if p.stops.Load() == 0 || h.cleanupCount() != 1 || h.log.count("release:a") != 1 {
		t.Fatalf("Aufräumen: %v", h.log.all())
	}
}

// --- Vorabprüfung ---

func TestProbeUnreachableShowsNoticeNoClient(t *testing.T) {
	h := newHarness(t, func(h *harness) { h.probe = rfb.ProbeResult{Err: "Keine Verbindung"} })
	h.open("a")
	h.waitNotice(StateTiles, "nicht erreichbar")
	if h.runner.count() != 0 || h.log.count("hold:a") != 0 {
		t.Fatalf("Ablauf %v", h.log.all())
	}
	if h.log.count("release:a") != 1 {
		t.Fatalf("Anfrage nicht freigegeben: %v", h.log.all())
	}
}

func TestProbeWrongPasswordShowsNoticeNoClient(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.probe = rfb.ProbeResult{Reachable: true, AuthRejected: true, Err: "Anmeldung abgelehnt"}
	})
	h.open("a")
	s := h.waitNotice(StateTiles, authFailedNotice)
	if s.Notice != authFailedNotice {
		t.Fatalf("Hinweis %q", s.Notice)
	}
	if h.runner.count() != 0 {
		t.Fatal("Client gestartet")
	}
}

func TestProbeOtherFailureShowsItsError(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.probe = rfb.ProbeResult{Reachable: true, Err: "Das Gegenüber ist kein VNC-Server"}
	})
	h.open("a")
	h.waitNotice(StateTiles, "kein VNC-Server")
	if h.runner.count() != 0 {
		t.Fatal("Client gestartet")
	}
}

func TestPasswordDecryptFailureShowsNoticeNoClient(t *testing.T) {
	h := newHarness(t, func(h *harness) { h.passwordErr = errors.New("kaputt") })
	h.open("a")
	s := h.waitFor("Hinweis", func(s Snapshot) bool { return s.State == StateTiles && s.Notice != "" })
	if !strings.Contains(s.Notice, "Passwort") {
		t.Fatalf("Hinweis %q", s.Notice)
	}
	h.mu.Lock()
	probes := h.probes
	h.mu.Unlock()
	if h.runner.count() != 0 || probes != 0 {
		t.Fatal("Client oder Prüfung trotz Fehler")
	}
}

// --- Koordination ---

func TestRejectedShowsBusyNoticeNoClient(t *testing.T) {
	for name, res := range map[string]coord.Result{
		"Reject":   busy("Terminal 2"),
		"Rejected": {Rejected: true, Decision: coord.Decision{Outcome: coord.Reject, Holder: &coord.Holder{Name: "Terminal 2"}}},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, func(h *harness) { h.coord.queue(res) })
			h.open("a")
			h.waitNotice(StateTiles, "Terminal 2")
			if h.runner.count() != 0 || h.log.count("hold:a") != 0 {
				t.Fatalf("Ablauf %v", h.log.all())
			}
		})
	}
}

func TestAllowWatchForcesViewOnlySpec(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.coord.queue(coord.Result{Decision: coord.Decision{Outcome: coord.AllowWatch, Holder: &coord.Holder{Name: "Anzeige"}}})
	})
	h.open("a")
	h.waitState(StateStarting)
	if !h.profile.last().ViewOnly {
		t.Fatal("ViewOnly nicht erzwungen")
	}
	if h.log.count("hold:a") != 0 {
		t.Fatalf("Hold beim Mitschauen: %v", h.log.all())
	}
}

func TestHMIViewOnlySettingIsPassedOn(t *testing.T) {
	h := newHarness(t, func(h *harness) { h.cfg.HMIs[0].ViewOnly = true })
	h.open("a")
	h.waitState(StateStarting)
	if !h.profile.last().ViewOnly {
		t.Fatal("ViewOnly fehlt")
	}
	if h.log.count("hold:a") != 1 {
		t.Fatalf("Ablauf %v", h.log.all())
	}
}

func TestAskConfirmWaitsThenStartsAfterConfirm(t *testing.T) {
	h := newHarness(t, func(h *harness) { h.coord.queue(askConfirm("Terminal 2"), askConfirm("Terminal 2")) })
	h.open("a")
	s := h.waitState(StateConfirming)
	if s.Confirm == nil || s.Confirm.Name != "Terminal 2" {
		t.Fatalf("Snapshot %+v", s)
	}
	h.advance(time.Minute)
	if h.runner.count() != 0 || h.log.count("takeover:a") != 0 {
		t.Fatalf("ohne Bestätigung: %v", h.log.all())
	}
	if err := h.m.Confirm(); err != nil {
		t.Fatal(err)
	}
	h.waitNotice(StateRequesting, "Terminal 2")
	if h.log.count("takeover:a") != 1 {
		t.Fatalf("Ablauf %v", h.log.all())
	}
	h.advance(coord.TakeoverWarn - 100*time.Millisecond)
	if h.runner.count() != 0 {
		t.Fatal("Start vor Ablauf der Vorwarnung")
	}
	h.advance(200 * time.Millisecond)
	h.waitState(StateStarting)
}

func TestConfirmWithoutQuestionIsAnError(t *testing.T) {
	h := newHarness(t, nil)
	if err := h.m.Confirm(); err == nil {
		t.Fatal("kein Fehler")
	}
}

func TestCancelFromConfirmReturnsToTiles(t *testing.T) {
	h := newHarness(t, func(h *harness) { h.coord.queue(askConfirm("Terminal 2")) })
	h.open("a")
	h.waitState(StateConfirming)
	h.m.Cancel()
	s := h.waitState(StateTiles)
	if s.Confirm != nil {
		t.Fatalf("Snapshot %+v", s)
	}
	if h.runner.count() != 0 || h.log.count("takeover:a") != 0 {
		t.Fatalf("Ablauf %v", h.log.all())
	}
	if h.log.count("release:a") != 1 {
		t.Fatalf("Anfrage nicht freigegeben: %v", h.log.all())
	}
}

func TestTakeOverWaitsWarningBeforeStart(t *testing.T) {
	h := newHarness(t, func(h *harness) { h.coord.queue(takeover("Terminal 2", 5*time.Second)) })
	h.open("a")
	h.waitNotice(StateRequesting, "Terminal 2")
	h.advance(4900 * time.Millisecond)
	if h.runner.count() != 0 {
		t.Fatal("beide wären gleichzeitig am HMI")
	}
	h.advance(200 * time.Millisecond)
	h.waitState(StateStarting)
	if !h.log.before("takeover:a", "hold:a") || !h.log.before("hold:a", "start") {
		t.Fatalf("Reihenfolge %v", h.log.all())
	}
}

func TestTakeOverWithoutWarningStartsAtOnce(t *testing.T) {
	h := newHarness(t, func(h *harness) { h.coord.queue(takeover("Terminal 2", 0)) })
	h.open("a")
	h.waitState(StateStarting)
	if h.log.count("takeover:a") != 1 {
		t.Fatalf("Ablauf %v", h.log.all())
	}
}

func TestTakeOverDoesNotKickHolderWhenProbeFails(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.coord.queue(takeover("Terminal 2", 5*time.Second))
		h.probe = rfb.ProbeResult{Reachable: true, AuthRejected: true}
	})
	h.open("a")
	h.waitNotice(StateTiles, authFailedNotice)
	if h.log.count("takeover:a") != 0 {
		t.Fatalf("Halter verdrängt: %v", h.log.all())
	}
}

func TestTakeoverFailureDoesNotBlockAndShowsHint(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.coord.queue(takeover("Terminal 2", 5*time.Second))
		h.coord.takeoverErr = errors.New("Terminal „Terminal 2“ ist nicht erreichbar")
	})
	h.open("a")
	s := h.waitState(StateStarting)
	if !slices.Contains(s.Unreachable, "Terminal 2") {
		t.Fatalf("Snapshot %+v", s)
	}
}

func TestUnreachableAndBadKeyAreHintsOnly(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.coord.queue(coord.Result{Unreachable: []string{"T2"}, BadKey: []string{"T3"}})
	})
	h.open("a")
	s := h.waitState(StateStarting)
	if !slices.Equal(s.Unreachable, []string{"T2"}) || !slices.Equal(s.BadKey, []string{"T3"}) {
		t.Fatalf("Snapshot %+v", s)
	}
	p := h.runner.last()
	p.exit(nil)
	s = h.waitState(StateTiles)
	if len(s.Unreachable) != 0 || len(s.BadKey) != 0 {
		t.Fatalf("Hinweis bleibt nach Sitzungsende: %+v", s)
	}
}

// --- Eingehende Übernahme ---

func TestIncomingTakeoverWarns5sThenStops(t *testing.T) {
	h := newHarness(t, nil)
	p := h.connect("a")
	h.coord.events <- coord.Event{HMI: "a", By: coord.Holder{Name: "Terminal 2"}, After: 5 * time.Second}
	s := h.waitState(StateWarned)
	if !strings.Contains(s.Notice, "Terminal 2") {
		t.Fatalf("Hinweis %q", s.Notice)
	}
	h.advance(4900 * time.Millisecond)
	if p.stops.Load() != 0 {
		t.Fatal("Stop vor Ablauf")
	}
	h.advance(200 * time.Millisecond)
	s = h.waitNotice(StateTiles, "Terminal 2")
	if p.stops.Load() != 1 {
		t.Fatalf("%d Stops", p.stops.Load())
	}
	if h.log.count("release:a") != 1 || h.cleanupCount() != 1 {
		t.Fatalf("Aufräumen: %v", h.log.all())
	}
	if !strings.Contains(s.Notice, "bernommen") {
		t.Fatalf("Hinweis %q", s.Notice)
	}
}

func TestImmediateTakeoverStopsNow(t *testing.T) {
	h := newHarness(t, nil)
	p := h.connect("a")
	h.coord.events <- coord.Event{HMI: "a", By: coord.Holder{Name: "Terminal 2"}}
	h.waitNotice(StateTiles, "Terminal 2")
	if p.stops.Load() != 1 {
		t.Fatalf("%d Stops", p.stops.Load())
	}
}

func TestTakeoverForOtherHMIIsIgnored(t *testing.T) {
	h := newHarness(t, nil)
	p := h.connect("a")
	h.coord.events <- coord.Event{HMI: "b", By: coord.Holder{Name: "Terminal 2"}}
	h.advance(time.Second)
	if s := h.m.Snapshot(); s.State != StateConnected || p.stops.Load() != 0 {
		t.Fatalf("Snapshot %+v", s)
	}
}

func TestTakeoverInTilesIsIgnored(t *testing.T) {
	h := newHarness(t, nil)
	h.coord.events <- coord.Event{HMI: "a", By: coord.Holder{Name: "Terminal 2"}}
	h.advance(time.Second)
	if s := h.m.Snapshot(); s.State != StateTiles || s.Notice != "" {
		t.Fatalf("Snapshot %+v", s)
	}
}

func TestLostSimultaneousClaimAbortsBeforeClientStart(t *testing.T) {
	// Die Niederlage kommt als Ereignis, solange noch geprüft oder gewartet wird.
	h := newHarness(t, func(h *harness) { h.coord.queue(takeover("Terminal 2", 5*time.Second)) })
	h.open("a")
	h.waitNotice(StateRequesting, "Terminal 2")
	h.coord.events <- coord.Event{HMI: "a", By: coord.Holder{Name: "Terminal 3"}}
	h.waitNotice(StateTiles, "Terminal 3")
	h.advance(6 * time.Second)
	if h.runner.count() != 0 {
		t.Fatal("Client trotz verlorener Anfrage")
	}
}

// --- Überwachung ---

func TestTCPLostFor5sStopsClient(t *testing.T) {
	h := newHarness(t, nil)
	p := h.connect("a")
	h.tcp.Store(false)
	h.advance(PollEvery) // erste Abfrage ohne Verbindung
	h.advance(4900 * time.Millisecond)
	if p.stops.Load() != 0 {
		t.Fatal("Stop nach 4,9 s")
	}
	h.advance(200 * time.Millisecond)
	s := h.waitFor("Hinweis", func(s Snapshot) bool { return s.State == StateTiles })
	if p.stops.Load() != 1 || s.Notice == "" {
		t.Fatalf("Stops %d, Snapshot %+v", p.stops.Load(), s)
	}
}

func TestTCPReturningResetsLostTimer(t *testing.T) {
	h := newHarness(t, nil)
	p := h.connect("a")
	h.tcp.Store(false)
	h.advance(4 * time.Second)
	h.tcp.Store(true)
	h.advance(PollEvery)
	h.tcp.Store(false)
	h.advance(4 * time.Second)
	if p.stops.Load() != 0 || h.m.Snapshot().State != StateConnected {
		t.Fatal("Zeit nicht zurückgesetzt")
	}
}

func TestStartTimeoutStopsClientWithoutEstablished(t *testing.T) {
	h := newHarness(t, nil)
	h.open("a")
	h.waitState(StateStarting)
	p := h.runner.last()
	h.advance(StartTimeout - 100*time.Millisecond)
	if p.stops.Load() != 0 {
		t.Fatal("Stop vor Ablauf der Startfrist")
	}
	h.advance(200 * time.Millisecond)
	s := h.waitFor("Kacheln", func(s Snapshot) bool { return s.State == StateTiles })
	if p.stops.Load() != 1 || !strings.Contains(s.Notice, "Presse") {
		t.Fatalf("Stops %d, Snapshot %+v", p.stops.Load(), s)
	}
	if h.cleanupCount() != 1 {
		t.Fatal("RAM-Ordner bleibt")
	}
}

func TestInactivity15MinReturnsToTiles(t *testing.T) {
	const limit = 15 * time.Minute
	h := newHarness(t, func(h *harness) { h.cfg.Kiosk.InactivityMinutes = 15 })
	p := h.connect("a")
	h.advance(limit - 2*time.Second)
	h.m.TouchActivity()
	h.advance(limit - 2*time.Second)
	if p.stops.Load() != 0 {
		t.Fatal("TouchActivity setzt nicht zurück")
	}
	h.advance(3 * time.Second)
	h.waitFor("Kacheln", func(s Snapshot) bool { return s.State == StateTiles })
	if p.stops.Load() != 1 {
		t.Fatalf("%d Stops", p.stops.Load())
	}
}

func TestInactivityZeroIsOff(t *testing.T) {
	h := newHarness(t, nil)
	p := h.connect("a")
	h.advance(2 * time.Hour)
	if p.stops.Load() != 0 || h.m.Snapshot().State != StateConnected {
		t.Fatal("Sitzung beendet")
	}
}

// --- Aufräumen ---

func TestRAMDirRemovedAfterSessionAndOnStartFailure(t *testing.T) {
	t.Run("Sitzungsende", func(t *testing.T) {
		h := newHarness(t, nil)
		h.connect("a")
		if h.cleanupCount() != 0 {
			t.Fatal("zu früh gelöscht")
		}
		h.m.Back()
		h.waitState(StateTiles)
		if h.cleanupCount() != 1 {
			t.Fatalf("%d Löschungen", h.cleanupCount())
		}
	})
	t.Run("Profilfehler", func(t *testing.T) {
		h := newHarness(t, func(h *harness) { h.profile.err = errors.New("Profil kaputt") })
		h.open("a")
		s := h.waitFor("Hinweis", func(s Snapshot) bool { return s.State == StateTiles && s.Notice != "" })
		if strings.Contains(s.Notice, "Profil kaputt") {
			t.Fatalf("Rohfehler in der Kachel: %q", s.Notice)
		}
		if h.cleanupCount() != 1 || h.runner.count() != 0 || h.log.count("hold:a") != 0 {
			t.Fatalf("Ablauf %v", h.log.all())
		}
	})
	t.Run("Startfehler", func(t *testing.T) {
		h := newHarness(t, func(h *harness) { h.runner.err = errors.New("nicht installiert") })
		h.open("a")
		s := h.waitFor("Hinweis", func(s Snapshot) bool { return s.State == StateTiles && s.Notice != "" })
		if strings.Contains(s.Notice, "nicht installiert") {
			t.Fatalf("Rohfehler in der Kachel: %q", s.Notice)
		}
		if h.cleanupCount() != 1 || h.log.count("release:a") != 1 {
			t.Fatalf("Ablauf %v", h.log.all())
		}
	})
	t.Run("RAM-Ordner-Fehler", func(t *testing.T) {
		h := newHarness(t, func(h *harness) { h.ramErr = errors.New("voll") })
		h.open("a")
		s := h.waitFor("Hinweis", func(s Snapshot) bool { return s.State == StateTiles && s.Notice != "" })
		if strings.Contains(s.Notice, "voll") {
			t.Fatalf("Rohfehler in der Kachel: %q", s.Notice)
		}
		if h.runner.count() != 0 {
			t.Fatal("Client gestartet")
		}
	})
}

// --- Konfiguration ---

func TestHMIDeletedDuringSessionEndsWithNotice(t *testing.T) {
	h := newHarness(t, nil)
	p := h.connect("a")
	h.setConfig(func(c *config.Config) { c.HMIs = c.HMIs[1:] })
	h.advance(PollEvery)
	h.waitNotice(StateTiles, "Presse")
	if p.stops.Load() != 1 {
		t.Fatalf("%d Stops", p.stops.Load())
	}
}

func TestHMIConnectionSettingsChangedEndsSession(t *testing.T) {
	for name, change := range map[string]func(*config.HMI){
		"Host": func(h *config.HMI) { h.Host = "10.0.0.9" },
		"Port": func(h *config.HMI) { h.Port = 5999 },
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, nil)
			p := h.connect("a")
			h.setConfig(func(c *config.Config) { change(&c.HMIs[0]) })
			h.advance(PollEvery)
			h.waitNotice(StateTiles, "geändert")
			if p.stops.Load() != 1 {
				t.Fatalf("%d Stops", p.stops.Load())
			}
		})
	}
}

func TestPasswordChangeEndsSessionButReencryptionDoesNot(t *testing.T) {
	h := newHarness(t, nil)
	p := h.connect("a")
	h.setConfig(func(c *config.Config) { c.HMIs[0].PasswordEnc = "neu verschlüsselt" })
	h.advance(PollEvery)
	if p.stops.Load() != 0 {
		t.Fatal("gleiches Passwort beendet die Sitzung")
	}
	h.mu.Lock()
	h.password = "anders"
	h.mu.Unlock()
	h.setConfig(func(c *config.Config) { c.HMIs[0].PasswordEnc = "neues Passwort" })
	h.advance(PollEvery)
	h.waitNotice(StateTiles, "geändert")
}

func TestOtherConfigChangesLeaveSessionAlone(t *testing.T) {
	h := newHarness(t, nil)
	p := h.connect("a")
	h.setConfig(func(c *config.Config) {
		c.HMIs[0].Name = "Neue Presse"
		c.HMIs[0].Icon = "x"
		c.Kiosk.Layout = "grid"
		c.HMIs = c.HMIs[:1]
	})
	h.advance(time.Second)
	if p.stops.Load() != 0 || h.m.Snapshot().State != StateConnected {
		t.Fatal("Sitzung beendet")
	}
}

func TestInactivityReadFromCurrentConfig(t *testing.T) {
	h := newHarness(t, nil)
	p := h.connect("a")
	h.setConfig(func(c *config.Config) { c.Kiosk.InactivityMinutes = 1 })
	h.advance(61 * time.Second)
	h.waitFor("Kacheln", func(s Snapshot) bool { return s.State == StateTiles })
	if p.stops.Load() != 1 {
		t.Fatalf("%d Stops", p.stops.Load())
	}
}

// --- Daueranzeige ---

func permanentOn(id string) func(*harness) {
	return func(h *harness) { h.cfg.Kiosk.PermanentHMI = id }
}

func TestPermanentDisplayAutoStartsAfterRun(t *testing.T) {
	h := newHarness(t, permanentOn("a"))
	s := h.waitState(StateStarting)
	if s.HMI != "a" || h.runner.count() != 1 {
		t.Fatalf("Snapshot %+v", s)
	}
}

func TestPermanentDisplayBackoff2_4_8_cap30(t *testing.T) {
	h := newHarness(t, permanentOn("a"))
	h.waitState(StateStarting)
	for i, wait := range []time.Duration{2, 4, 8, 16, 30, 30} {
		wait *= time.Second
		starts := h.runner.count()
		h.runner.last().exit(errors.New("abgestürzt"))
		s := h.waitState(StateReconnecting)
		if s.Notice == "" || s.HMI != "a" {
			t.Fatalf("Runde %d: Snapshot %+v", i, s)
		}
		if h.log.count("release:a") != i+1 {
			t.Fatalf("Runde %d: nicht freigegeben: %v", i, h.log.all())
		}
		h.advance(wait - 100*time.Millisecond)
		if h.runner.count() != starts {
			t.Fatalf("Runde %d: Start vor %v", i, wait)
		}
		h.advance(200 * time.Millisecond)
		h.waitState(StateStarting)
		if h.runner.count() != starts+1 {
			t.Fatalf("Runde %d: %d Starts", i, h.runner.count())
		}
	}
}

func TestPermanentBackoffResetsAfterLongConnection(t *testing.T) {
	h := newHarness(t, permanentOn("a"))
	h.waitState(StateStarting)
	h.runner.last().exit(nil)
	h.waitState(StateReconnecting)
	h.advance(2 * time.Second)
	h.waitState(StateStarting)
	h.runner.last().exit(nil)
	h.waitState(StateReconnecting)
	h.advance(4 * time.Second)
	h.waitState(StateStarting)

	h.tcp.Store(true)
	h.advance(PollEvery)
	h.waitState(StateConnected)
	h.advance(ReconnectResetAfter)
	h.runner.last().exit(nil)
	h.waitState(StateReconnecting)
	starts := h.runner.count()
	h.advance(ReconnectMin + 100*time.Millisecond)
	h.waitState(StateStarting)
	if h.runner.count() != starts+1 {
		t.Fatal("Wartezeit nicht zurückgesetzt")
	}
}

func TestPermanentShortConnectionKeepsBackoff(t *testing.T) {
	h := newHarness(t, permanentOn("a"))
	h.waitState(StateStarting)
	h.tcp.Store(true)
	h.advance(PollEvery)
	h.waitState(StateConnected)
	h.advance(ReconnectResetAfter - 2*time.Second)
	h.runner.last().exit(nil)
	h.waitState(StateReconnecting)
	h.advance(2 * time.Second)
	h.waitState(StateStarting)
	h.runner.last().exit(nil)
	h.waitState(StateReconnecting)
	starts := h.runner.count()
	h.advance(3900 * time.Millisecond)
	if h.runner.count() != starts {
		t.Fatal("Wartezeit fälschlich zurückgesetzt")
	}
}

func TestPermanentDisplayIgnoresInactivity(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.cfg.Kiosk.PermanentHMI = "a"
		h.cfg.Kiosk.InactivityMinutes = 1
	})
	h.waitState(StateStarting)
	h.tcp.Store(true)
	h.advance(PollEvery)
	h.waitState(StateConnected)
	h.advance(10 * time.Minute)
	if p := h.runner.last(); p.stops.Load() != 0 || h.m.Snapshot().State != StateConnected {
		t.Fatal("Sitzung wegen Inaktivität beendet")
	}
}

func TestPermanentHMIDeletedShowsTilesAndNotice(t *testing.T) {
	t.Run("beim Start", func(t *testing.T) {
		h := newHarness(t, permanentOn("weg"))
		s := h.waitFor("Hinweis", func(s Snapshot) bool { return s.State == StateTiles && s.Notice != "" })
		if strings.Contains(s.Notice, "weg") {
			t.Fatalf("Kennung im Hinweis: %q", s.Notice)
		}
		h.advance(2 * time.Minute)
		if h.log.count("request:weg") != 0 || h.runner.count() != 0 {
			t.Fatalf("Versuche %v", h.log.all())
		}
		// Kiosk bleibt bedienbar.
		h.open("b")
		h.waitState(StateStarting)
	})
	t.Run("während der Sitzung, keine Versuche bis zur Änderung", func(t *testing.T) {
		h := newHarness(t, permanentOn("a"))
		h.waitState(StateStarting)
		saved := h.cfg.HMIs[0]
		h.setConfig(func(c *config.Config) { c.HMIs = c.HMIs[1:] })
		h.advance(PollEvery)
		h.waitNotice(StateTiles, "Presse")
		h.advance(2 * time.Minute)
		if h.runner.count() != 1 {
			t.Fatalf("%d Starts", h.runner.count())
		}
		h.setConfig(func(c *config.Config) { c.HMIs = append(c.HMIs, saved) })
		h.advance(PollEvery)
		h.waitState(StateStarting)
	})
	t.Run("während des Wartens", func(t *testing.T) {
		h := newHarness(t, permanentOn("a"))
		h.waitState(StateStarting)
		h.runner.last().exit(nil)
		h.waitState(StateReconnecting)
		h.setConfig(func(c *config.Config) { c.HMIs = c.HMIs[1:] })
		h.advance(PollEvery)
		h.waitNotice(StateTiles, "Presse")
		h.advance(time.Minute)
		if h.runner.count() != 1 {
			t.Fatalf("%d Starts", h.runner.count())
		}
	})
}

func TestPermanentObeysTakeoverThenRetriesAndMayBeRejected(t *testing.T) {
	h := newHarness(t, permanentOn("a"))
	h.waitState(StateStarting)
	h.tcp.Store(true)
	h.advance(PollEvery)
	h.waitState(StateConnected)
	p := h.runner.last()
	h.coord.events <- coord.Event{HMI: "a", By: coord.Holder{Name: "Leitstand"}, After: 5 * time.Second}
	h.waitState(StateWarned)
	h.coord.queue(busy("Leitstand"), busy("Leitstand"))
	h.advance(5 * time.Second)
	h.waitNotice(StateReconnecting, "Leitstand")
	// Freigegeben, bevor die nächste Anfrage überhaupt möglich ist.
	if p.stops.Load() != 1 || h.log.count("release:a") != 1 || h.log.count("request:a") != 1 {
		t.Fatalf("Ablauf %v", h.log.all())
	}
	// Abgelehnt: gleicher Rhythmus, die Wartezeit wächst weiter.
	h.advance(2 * time.Second)
	h.waitNotice(StateReconnecting, "in 4 s")
	h.advance(3900 * time.Millisecond)
	if n := h.log.count("request:a"); n != 2 {
		t.Fatalf("%d Anfragen vor Ablauf", n)
	}
	h.advance(200 * time.Millisecond)
	h.waitNotice(StateReconnecting, "in 8 s")
	if h.runner.count() != 1 {
		t.Fatalf("%d Starts trotz Ablehnung", h.runner.count())
	}
}

func TestPermanentAutomaticRetryNeverAsksForConfirmation(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.cfg.Kiosk.PermanentHMI = "a"
		h.coord.queue(askConfirm("Terminal 2"))
	})
	h.waitNotice(StateReconnecting, "Terminal 2")
	if h.log.count("takeover:a") != 0 {
		t.Fatalf("Ablauf %v", h.log.all())
	}
}

func TestPermanentBackStopsRetries(t *testing.T) {
	h := newHarness(t, permanentOn("a"))
	h.waitState(StateStarting)
	h.m.Back()
	h.waitState(StateTiles)
	h.advance(2 * time.Minute)
	if h.runner.count() != 1 {
		t.Fatalf("%d Starts nach Zurück", h.runner.count())
	}
	// Ein erneuter Touch auf die Kachel macht es wieder zur Daueranzeige.
	h.open("a")
	h.waitState(StateStarting)
	h.runner.last().exit(nil)
	h.waitState(StateReconnecting)
	h.m.Back()
	h.waitState(StateTiles)
	h.advance(2 * time.Minute)
	if h.runner.count() != 2 {
		t.Fatalf("%d Starts nach Zurück im Warten", h.runner.count())
	}
}

func TestPermanentSetByConfigChangeStarts(t *testing.T) {
	h := newHarness(t, nil)
	h.advance(PollEvery)
	h.setConfig(func(c *config.Config) { c.Kiosk.PermanentHMI = "b" })
	h.advance(PollEvery)
	s := h.waitState(StateStarting)
	if s.HMI != "b" {
		t.Fatalf("Snapshot %+v", s)
	}
}

func TestOtherHMIOnPermanentDeviceDoesNotReconnect(t *testing.T) {
	h := newHarness(t, permanentOn("weg"))
	h.open("b")
	h.waitState(StateStarting)
	h.runner.last().exit(nil)
	h.waitState(StateTiles)
	h.advance(time.Minute)
	if h.runner.count() != 1 {
		t.Fatalf("%d Starts", h.runner.count())
	}
}

// --- Beobachter ---

func TestSlowSubscriberGetsLatestSnapshotAndNeverBlocks(t *testing.T) {
	h := newHarness(t, nil)
	slow, cancel := h.m.Subscribe()
	defer cancel()
	for range 5 {
		h.open("a")
		h.waitState(StateStarting)
		h.m.Back()
		h.waitState(StateTiles)
	}
	h.open("b")
	h.waitState(StateStarting)
	s := <-slow
	if s.State != StateStarting || s.HMI != "b" {
		t.Fatalf("nicht der neueste Stand: %+v", s)
	}
}

func TestSubscribeDeliversCurrentStateAndCancelCloses(t *testing.T) {
	h := newHarness(t, nil)
	ch, cancel := h.m.Subscribe()
	if s := <-ch; s.State != StateTiles {
		t.Fatalf("Snapshot %+v", s)
	}
	cancel()
	cancel()
	if _, ok := <-ch; ok {
		t.Fatal("Kanal offen")
	}
}

// --- Prüfbefunde Runde 1 ---

func primaryTakeover(name string) coord.Result {
	return coord.Result{Decision: coord.Decision{Outcome: coord.TakeOver, Warn: coord.TakeoverWarn, Holder: &coord.Holder{Terminal: "t3", Name: name, Role: config.RolePrimary}}}
}

func TestConfirmAppliesFreshDecision(t *testing.T) {
	confirmed := func(t *testing.T, fresh coord.Result) *harness {
		t.Helper()
		h := newHarness(t, func(h *harness) { h.coord.queue(askConfirm("B"), fresh) })
		h.open("a")
		h.waitState(StateConfirming)
		if err := h.m.Confirm(); err != nil {
			t.Fatal(err)
		}
		return h
	}
	takenFrom := func(h *harness) []string {
		h.coord.mu.Lock()
		defer h.coord.mu.Unlock()
		var names []string
		for _, d := range h.coord.taken {
			names = append(names, d.Holder.Name)
		}
		return names
	}
	t.Run("jetzt primärer Halter lehnt ab", func(t *testing.T) {
		h := confirmed(t, coord.Result{Decision: coord.Decision{Outcome: coord.Reject, Holder: &coord.Holder{Name: "C", Role: config.RolePrimary}}})
		h.waitNotice(StateTiles, "C")
		if h.log.count("takeover:a") != 0 || h.runner.count() != 0 {
			t.Fatalf("Ablauf %v", h.log.all())
		}
		if n := h.log.count("request:a"); n != 2 {
			t.Fatalf("%d Anfragen", n)
		}
	})
	t.Run("Halter weg startet ohne Übernahme", func(t *testing.T) {
		h := confirmed(t, coord.Result{})
		h.waitState(StateStarting)
		if h.log.count("takeover:a") != 0 {
			t.Fatalf("Ablauf %v", h.log.all())
		}
	})
	t.Run("neuer Halter wird übernommen, ohne zweite Rückfrage", func(t *testing.T) {
		h := confirmed(t, askConfirm("D"))
		h.waitNotice(StateRequesting, "D")
		if got := takenFrom(h); !slices.Equal(got, []string{"D"}) {
			t.Fatalf("übernommen von %v", got)
		}
	})
	t.Run("vorrangige Übernahme trifft den aktuellen Halter", func(t *testing.T) {
		h := confirmed(t, takeover("E", 0))
		h.waitState(StateStarting)
		if got := takenFrom(h); !slices.Equal(got, []string{"E"}) {
			t.Fatalf("übernommen von %v", got)
		}
	})
}

func TestPermanentTargetChangeEndsOldSession(t *testing.T) {
	t.Run("anderes Ziel", func(t *testing.T) {
		h := newHarness(t, permanentOn("a"))
		h.waitState(StateStarting)
		h.tcp.Store(true)
		h.advance(PollEvery)
		h.waitState(StateConnected)
		p := h.runner.last()
		h.setConfig(func(c *config.Config) { c.Kiosk.PermanentHMI = "b" })
		h.advance(PollEvery)
		h.waitNotice(StateTiles, "Presse")
		if p.stops.Load() != 1 {
			t.Fatalf("%d Stops", p.stops.Load())
		}
		h.advance(PollEvery)
		if s := h.waitState(StateStarting); s.HMI != "b" {
			t.Fatalf("Snapshot %+v", s)
		}
	})
	t.Run("Ziel entfernt, kein Neuverbinden", func(t *testing.T) {
		h := newHarness(t, permanentOn("a"))
		h.waitState(StateStarting)
		h.setConfig(func(c *config.Config) { c.Kiosk.PermanentHMI = "" })
		h.advance(PollEvery)
		h.waitNotice(StateTiles, "Presse")
		h.advance(time.Minute)
		if h.runner.count() != 1 {
			t.Fatalf("%d Starts", h.runner.count())
		}
	})
	t.Run("manuelle Sitzung bleibt", func(t *testing.T) {
		h := newHarness(t, permanentOn("weg"))
		p := h.connect("b")
		h.setConfig(func(c *config.Config) { c.Kiosk.PermanentHMI = "" })
		h.advance(time.Second)
		if p.stops.Load() != 0 || h.m.Snapshot().State != StateConnected {
			t.Fatalf("Snapshot %+v", h.m.Snapshot())
		}
	})
}

func TestAutomaticStartNeverDisplacesPrimaryHolder(t *testing.T) {
	t.Run("automatisch: belegt, Neuversuch", func(t *testing.T) {
		h := newHarness(t, func(h *harness) {
			h.cfg.Kiosk.PermanentHMI = "a"
			h.coord.queue(primaryTakeover("Leitstand"))
		})
		h.waitNotice(StateReconnecting, "Leitstand")
		if h.log.count("takeover:a") != 0 || h.runner.count() != 0 {
			t.Fatalf("Ablauf %v", h.log.all())
		}
	})
	t.Run("automatisch gegen normalen Halter bleibt erlaubt", func(t *testing.T) {
		h := newHarness(t, func(h *harness) {
			h.cfg.Kiosk.PermanentHMI = "a"
			h.coord.queue(takeover("Terminal 2", 0))
		})
		h.waitState(StateStarting)
		if h.log.count("takeover:a") != 1 {
			t.Fatalf("Ablauf %v", h.log.all())
		}
	})
	t.Run("Touch folgt der Tabelle", func(t *testing.T) {
		h := newHarness(t, func(h *harness) { h.coord.queue(primaryTakeover("Leitstand")) })
		h.open("a")
		h.waitNotice(StateRequesting, "Leitstand")
		if h.log.count("takeover:a") != 1 {
			t.Fatalf("Ablauf %v", h.log.all())
		}
	})
}

func TestTakeoverBadKeyGoesToBadKeyHint(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.coord.queue(takeover("Terminal 2", 5*time.Second))
		h.coord.takeoverErr = fmt.Errorf("abgelehnt: %w", coord.ErrBadKey)
	})
	h.open("a")
	s := h.waitState(StateStarting)
	if !slices.Equal(s.BadKey, []string{"Terminal 2"}) || len(s.Unreachable) != 0 {
		t.Fatalf("Snapshot %+v", s)
	}
}

func TestBackDuringStoppingKeepsCauseNotice(t *testing.T) {
	stopGate := make(chan struct{})
	h := newHarness(t, func(h *harness) { h.runner.stopGate = stopGate })
	h.connect("a")
	h.tcp.Store(false)
	h.advance(PollEvery + LostAfter)
	h.waitNotice(StateStopping, "verloren")
	h.m.Back()
	close(stopGate)
	h.waitNotice(StateTiles, "verloren")
}

func TestPasswordChangeBeforePrecheckDoesNotEndAttempt(t *testing.T) {
	h := newHarness(t, func(h *harness) { h.coord.queue(askConfirm("B")) })
	h.open("a")
	h.waitState(StateConfirming)
	h.mu.Lock()
	h.password = "neu"
	h.mu.Unlock()
	h.setConfig(func(c *config.Config) { c.HMIs[0].PasswordEnc = "neu verschlüsselt" })
	h.advance(PollEvery)
	if s := h.m.Snapshot(); s.State != StateConfirming || s.Notice != "" {
		t.Fatalf("Snapshot %+v", s)
	}
	if err := h.m.Confirm(); err != nil {
		t.Fatal(err)
	}
	h.waitState(StateStarting)
	if got := h.profile.last().Password; got != "neu" {
		t.Fatalf("Passwort %q", got)
	}
}

func TestSnapshotReportsViewOnly(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.coord.queue(coord.Result{Decision: coord.Decision{Outcome: coord.AllowWatch, Holder: &coord.Holder{Name: "Anzeige"}}})
	})
	h.open("a")
	if s := h.waitState(StateStarting); !s.ViewOnly {
		t.Fatal("Mitschauen nicht als ViewOnly gemeldet")
	}
	h2 := newHarness(t, func(h *harness) { h.cfg.HMIs[0].ViewOnly = true })
	h2.open("a")
	if s := h2.waitState(StateStarting); !s.ViewOnly {
		t.Fatal("ViewOnly der Konfiguration fehlt im Snapshot")
	}
	h3 := newHarness(t, nil)
	h3.open("a")
	if s := h3.waitState(StateStarting); s.ViewOnly {
		t.Fatal("ViewOnly ohne Grund gesetzt")
	}
}

func TestOpenIsRefusedWhileBlocked(t *testing.T) {
	h := newHarness(t, nil)
	msg := "Dauerprüfung läuft"
	h.blocked.Store(&msg)
	err := h.m.Open("a")
	if err == nil || err.Error() != msg {
		t.Fatalf("Fehler: %v", err)
	}
	s := h.m.Snapshot()
	if s.State != StateTiles || s.Notice != msg {
		t.Fatalf("Zustand %s, Hinweis %q", s.State, s.Notice)
	}
	if h.runner.count() != 0 {
		t.Fatal("es darf kein Client starten")
	}
	h.blocked.Store(nil)
	h.open("a")
	h.waitState(StateStarting)
}

func TestPermanentDisplayWaitsWhileBlocked(t *testing.T) {
	msg := "Dauerprüfung läuft"
	h := newHarness(t, func(h *harness) {
		h.cfg.Kiosk.PermanentHMI = "a"
		h.blocked.Store(&msg)
	})
	h.advance(10 * PollEvery)
	if h.runner.count() != 0 || h.m.Snapshot().State != StateTiles {
		t.Fatalf("Daueranzeige startete trotz Sperre: %d Starts, %s", h.runner.count(), h.m.Snapshot().State)
	}
	h.blocked.Store(nil)
	h.advance(2 * PollEvery)
	h.waitState(StateStarting)
	if h.runner.count() != 1 {
		t.Fatalf("%d Starts", h.runner.count())
	}
}

func TestPermanentReconnectWaitsWhileBlockedWithoutGrowingBackoff(t *testing.T) {
	msg := "Dauerprüfung läuft"
	h := newHarness(t, permanentOn("a"))
	h.waitState(StateStarting)
	h.runner.last().exit(errors.New("abgestürzt"))
	h.waitState(StateReconnecting)
	h.blocked.Store(&msg)
	starts := h.runner.count()
	h.advance(40 * time.Second) // weit über die Wartezeit hinaus
	if h.runner.count() != starts {
		t.Fatalf("Neuverbinden trotz Sperre: %d Starts", h.runner.count()-starts)
	}
	if got := h.m.Snapshot().State; got != StateReconnecting {
		t.Fatalf("Zustand %s", got)
	}
	h.blocked.Store(nil)
	h.advance(2 * PollEvery)
	h.waitState(StateStarting)
	// Das Wartezeit-Wachstum ruhte: Der nächste Absturz wartet 4 s, nicht mehr.
	h.runner.last().exit(errors.New("abgestürzt"))
	h.waitState(StateReconnecting)
	starts = h.runner.count()
	h.advance(4*time.Second + 100*time.Millisecond)
	h.waitState(StateStarting)
	if h.runner.count() != starts+1 {
		t.Fatalf("Wartezeit länger als 4 s: %d Starts", h.runner.count()-starts)
	}
}

func TestClearNoticeOnlyRemovesThatNotice(t *testing.T) {
	h := newHarness(t, nil)
	msg := "Dauerprüfung läuft"
	h.blocked.Store(&msg)
	_ = h.m.Open("a")
	h.blocked.Store(nil)
	h.m.ClearNotice("etwas anderes")
	if h.m.Snapshot().Notice != msg {
		t.Fatalf("fremder Hinweis entfernte ihn: %q", h.m.Snapshot().Notice)
	}
	h.m.ClearNotice(msg)
	if h.m.Snapshot().Notice != "" {
		t.Fatalf("Hinweis blieb: %q", h.m.Snapshot().Notice)
	}
}

func TestSetNoticeShowsStandingNoticeOnTiles(t *testing.T) {
	h := newHarness(t, nil)
	h.m.SetNotice("Wartungsmodus")
	if got := h.m.Snapshot().Notice; got != "Wartungsmodus" {
		t.Fatalf("Hinweis %q", got)
	}
	h.m.ClearNotice("Wartungsmodus")
	if got := h.m.Snapshot().Notice; got != "" {
		t.Fatalf("Hinweis blieb: %q", got)
	}
}

func TestMissingPasswordShowsRepairNoticeNoClient(t *testing.T) {
	h := newHarness(t, func(h *harness) { h.passwordErr = fmt.Errorf("Schlüssel: %w", ErrPasswordMissing) })
	h.open("a")
	s := h.waitFor("Hinweis", func(s Snapshot) bool { return s.State == StateTiles && s.Notice != "" })
	if s.Notice != "VNC-Passwort fehlt – bitte in der Admin-Seite neu eingeben." {
		t.Fatalf("Hinweis %q", s.Notice)
	}
	h.mu.Lock()
	probes := h.probes
	h.mu.Unlock()
	if h.runner.count() != 0 || probes != 0 {
		t.Fatal("Client oder Prüfung trotz fehlendem Passwort")
	}
}

// --- Wartungsmodus beendet laufende Sitzungen ---

func TestEndForMaintenanceStopsRunningSession(t *testing.T) {
	h := newHarness(t, nil)
	p := h.connect("a")
	msg := "Wartungsmodus: Test"
	h.blocked.Store(&msg)
	h.m.EndForMaintenance(msg)
	s := h.waitFor("Kacheln mit Hinweis", func(s Snapshot) bool { return s.State == StateTiles && s.Notice == msg })
	if p.stops.Load() == 0 || h.log.count("release:a") != 1 {
		t.Fatalf("Client nicht gestoppt oder HMI nicht freigegeben: %v (%+v)", h.log.all(), s)
	}
}

func TestEndForMaintenanceEndsPermanentDisplayWithoutReconnect(t *testing.T) {
	h := newHarness(t, permanentOn("a"))
	h.waitState(StateStarting)
	msg := "Wartungsmodus: Test"
	h.blocked.Store(&msg)
	h.m.EndForMaintenance(msg)
	h.waitFor("Kacheln mit Hinweis", func(s Snapshot) bool { return s.State == StateTiles && s.Notice == msg })
	starts := h.runner.count()
	h.advance(2 * time.Minute)
	if h.runner.count() != starts || h.m.Snapshot().State != StateTiles {
		t.Fatalf("Daueranzeige läuft trotz Wartungsmodus weiter: %d Starts, %s", h.runner.count()-starts, h.m.Snapshot().State)
	}
	// Nach dem Wartungsmodus startet sie wieder.
	h.blocked.Store(nil)
	h.advance(2 * PollEvery)
	h.waitState(StateStarting)
}

func TestEndForMaintenanceEndsWaitingPermanentDisplay(t *testing.T) {
	h := newHarness(t, permanentOn("a"))
	h.waitState(StateStarting)
	h.runner.last().exit(errors.New("abgestürzt"))
	h.waitState(StateReconnecting)
	msg := "Wartungsmodus: Test"
	h.blocked.Store(&msg)
	h.m.EndForMaintenance(msg)
	h.waitFor("Kacheln mit Hinweis", func(s Snapshot) bool { return s.State == StateTiles && s.Notice == msg })
}
