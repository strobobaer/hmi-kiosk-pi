package browser

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"hmikiosk/internal/client"
)

func TestEdgeArgs(t *testing.T) {
	got := EdgeArgs("http://127.0.0.1:8080/start", `C:\p\edge`)
	want := []string{
		"--app=http://127.0.0.1:8080/start",
		`--user-data-dir=C:\p\edge`,
		"--no-first-run", "--no-default-browser-check",
		"--disable-features=msEdgeSleepingTabs,LocalNetworkAccessChecks,PrivateNetworkAccessRespectPreflightResults",
		"--disable-background-timer-throttling", "--disable-renderer-backgrounding",
		"--disable-backgrounding-occluded-windows",
	}
	if !slices.Equal(got, want) {
		t.Errorf("EdgeArgs:\n got %q\nwant %q", got, want)
	}
}

func TestFindEdgeMissing(t *testing.T) {
	for name, rd := range map[string]func(string) (string, error){
		"leer":   func(string) (string, error) { return "  ", nil },
		"Fehler": func(string) (string, error) { return "", errors.New("nicht da") },
	} {
		_, err := FindEdge(rd)
		if err == nil || !strings.Contains(err.Error(), "Edge nicht gefunden") {
			t.Errorf("%s: %v", name, err)
		}
	}
	var key string
	p, err := FindEdge(func(k string) (string, error) { key = k; return `C:\E\msedge.exe`, nil })
	if err != nil || p != `C:\E\msedge.exe` {
		t.Fatalf("%q %v", p, err)
	}
	if key != `HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\msedge.exe` {
		t.Errorf("Schlüssel: %q", key)
	}
}

func TestSupervisorOnRestart(t *testing.T) {
	g := newRig(t)
	restarts := make(chan int, 16)
	g.s.SetOnRestart(func(n int) { restarts <- n })
	g.s.SetEdge(`C:\p\edge`)
	g.s.SetStartURL("http://127.0.0.1:8080/start")
	g.run(t)
	g.s.Show(TargetStart)
	c := g.nextStart(t)
	if c.Args[0] != "--app=http://127.0.0.1:8080/start" {
		t.Fatalf("Edge-Argumente: %q", c.Args)
	}
	select {
	case n := <-restarts:
		t.Fatalf("OnRestart beim ersten Start: %d", n)
	case <-time.After(50 * time.Millisecond):
	}
	var waits []time.Duration
	for i := 0; i < 7; i++ {
		g.r.proc(i).done <- errors.New("beendet")
		waits = append(waits, g.nextAfter(t))
		g.clk.fire <- g.clk.Now()
		g.nextStart(t)
		select {
		case n := <-restarts:
			if n != i+1 {
				t.Fatalf("OnRestart: %d, erwartet %d", n, i+1)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("kein OnRestart nach Ende %d", i+1)
		}
	}
	for i := 1; i < len(waits); i++ {
		if waits[i] < waits[i-1] || waits[i] > backoffMax {
			t.Errorf("Pausen: %v", waits)
		}
	}
	if waits[0] != backoffMin || waits[len(waits)-1] != backoffMax {
		t.Errorf("Pausen müssen von %v bis %v wachsen: %v", backoffMin, backoffMax, waits)
	}
}

// jobProc meldet Folgeprozesse im Job wie der Windows-Runner.
type jobProc struct {
	fakeProc
	active atomic.Int32
}

func (p *jobProc) ActiveProcesses() (int, error) { return int(p.active.Load()), nil }

type oneRunner struct {
	p       client.Process
	started chan client.Command
}

func (r *oneRunner) Start(c client.Command) (client.Process, error) {
	r.started <- c
	return r.p, nil
}

func TestSupervisorWaitsForEmptyJob(t *testing.T) {
	jp := &jobProc{fakeProc: fakeProc{done: make(chan error, 1)}}
	jp.active.Store(1)
	r := &oneRunner{p: jp, started: make(chan client.Command, 4)}
	clk := &fakeClock{now: time.Unix(1000, 0), afters: make(chan time.Duration, 16), fire: make(chan time.Time, 1)}
	s := New(r, `C:\E\msedge.exe`, "", func() int { return 1920 })
	s.clock = clk
	s.logf = func(string, ...any) {}
	s.SetEdge(`C:\p\edge`)
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() { s.Run(ctx); close(finished) }()
	t.Cleanup(func() { cancel(); <-finished })
	after := func() time.Duration {
		select {
		case d := <-clk.afters:
			return d
		case <-time.After(2 * time.Second):
			t.Fatal("kein Wecker")
		}
		return 0
	}
	s.Show(TargetStart)
	<-r.started
	if d := after(); d != jobPoll {
		t.Fatalf("Job-Abfrage nach dem Start: %v", d)
	}
	jp.done <- nil // Startprozess reicht weiter und endet, der Job lebt
	clk.fire <- clk.Now()
	if d := after(); d != jobPoll { // Job noch nicht leer: nächste Abfrage, kein Neustart
		t.Fatalf("Abfrage: %v", d)
	}
	select {
	case <-r.started:
		t.Fatal("Neustart bei lebendem Job")
	case <-time.After(50 * time.Millisecond):
	}
	jp.active.Store(0)
	clk.fire <- clk.Now()
	if d := after(); d != backoffMin {
		t.Fatalf("Neustartpause: %v", d)
	}
	if jp.stopCount() != 1 {
		t.Errorf("Job-Handle nicht freigegeben: %d", jp.stopCount())
	}
	clk.fire <- clk.Now()
	select {
	case <-r.started:
	case <-time.After(2 * time.Second):
		t.Fatal("kein Neustart")
	}
}

// Edge bleibt nach dem Schließen des Fensters im Hintergrund: Ist das Fenster weg, gilt Edge
// als beendet, der Job wird beendet und neu gestartet.
func TestSupervisorRestartsWhenWindowGone(t *testing.T) {
	jp := &jobProc{fakeProc: fakeProc{done: make(chan error, 1)}}
	jp.active.Store(6) // Hintergrundprozesse leben weiter
	r := &oneRunner{p: jp, started: make(chan client.Command, 4)}
	clk := &fakeClock{now: time.Unix(1000, 0), afters: make(chan time.Duration, 16), fire: make(chan time.Time, 1)}
	s := New(r, `C:\E\msedge.exe`, "", func() int { return 1920 })
	s.clock = clk
	s.logf = func(string, ...any) {}
	s.SetEdge(`C:\p\edge`)
	var has atomic.Bool
	has.Store(true)
	s.SetWindowCheck(has.Load)
	restarts := make(chan int, 4)
	s.SetOnRestart(func(n int) { restarts <- n })
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() { s.Run(ctx); close(finished) }()
	t.Cleanup(func() { cancel(); <-finished })
	tick := func() time.Duration {
		clk.fire <- clk.Now()
		select {
		case d := <-clk.afters:
			return d
		case <-time.After(2 * time.Second):
			t.Fatal("kein Wecker")
		}
		return 0
	}
	s.Show(TargetStart)
	<-r.started
	<-clk.afters // erste Abfrage nach dem Start
	if d := tick(); d != jobPoll {
		t.Fatalf("Fenster da: %v", d)
	}
	has.Store(false) // Nutzer schließt das Fenster
	if d := tick(); d != jobPoll {
		t.Fatalf("einmal ohne Fenster reicht nicht: %v", d)
	}
	if d := tick(); d != backoffMin {
		t.Fatalf("zweimal ohne Fenster: Neustartpause erwartet, %v", d)
	}
	if jp.stopCount() != 1 {
		t.Errorf("Job nicht beendet: %d", jp.stopCount())
	}
	has.Store(true)
	clk.fire <- clk.Now()
	<-r.started
	select {
	case n := <-restarts:
		if n != 1 {
			t.Errorf("OnRestart: %d", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("kein OnRestart")
	}
}

func TestEdgeKioskArgs(t *testing.T) {
	got := EdgeKioskArgs("http://127.0.0.1:8080/start", `C:\p\edge`)
	want := []string{
		"--kiosk", "http://127.0.0.1:8080/start", "--edge-kiosk-type=fullscreen",
		"--no-first-run", `--user-data-dir=C:\p\edge`,
		"--no-default-browser-check",
		"--disable-features=msEdgeSleepingTabs,LocalNetworkAccessChecks,PrivateNetworkAccessRespectPreflightResults",
		"--disable-background-timer-throttling", "--disable-renderer-backgrounding",
		"--disable-backgrounding-occluded-windows",
	}
	if !slices.Equal(got, want) {
		t.Errorf("EdgeKioskArgs:\n got %q\nwant %q", got, want)
	}
	// EdgeArgs bleibt unverändert und teilt die Hintergrund-Schalter.
	if a := EdgeArgs("u", "p"); !slices.Equal(a[len(a)-4:], got[len(got)-4:]) {
		t.Errorf("Hintergrund-Schalter weichen ab: %q", a)
	}
}

func TestSupervisorEdgeKioskAdminAndBack(t *testing.T) {
	g := newRig(t)
	g.s.SetEdge(`C:\p\edge`)
	g.s.SetEdgeKiosk("http://127.0.0.1:8081/admin/")
	g.s.SetStartURL("http://127.0.0.1:8080/start")
	g.run(t)
	g.s.Show(TargetStart)
	c := g.nextStart(t)
	if c.Args[0] != "--kiosk" || c.Args[1] != "http://127.0.0.1:8080/start" {
		t.Fatalf("Start im Kiosk: %q", c.Args)
	}
	g.s.Show(TargetAdmin)
	c = g.nextStart(t)
	if c.Args[0] != "--kiosk" || c.Args[1] != "http://127.0.0.1:8081/admin/" {
		t.Fatalf("Admin im Kiosk: %q", c.Args)
	}
	g.s.Show(TargetStart) // „Zurück“
	c = g.nextStart(t)
	if c.Args[1] != "http://127.0.0.1:8080/start" {
		t.Fatalf("Zurück: %q", c.Args)
	}
}

func TestSupervisorEdgeAppIgnoresAdminTarget(t *testing.T) {
	g := newRig(t)
	g.s.SetEdge(`C:\p\edge`)
	g.s.SetStartURL("http://127.0.0.1:8080/start")
	if a := g.s.args(TargetAdmin); a[0] != "--app=http://127.0.0.1:8080/start" {
		t.Fatalf("Zusatz-Modus unverändert: %q", a)
	}
}
