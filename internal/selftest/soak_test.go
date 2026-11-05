//go:build linux

package selftest

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu   sync.Mutex
	t    time.Time
	step time.Duration
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(c.step)
	return c.t
}

func TestSoakCountsFailuresAndDetectsLeak(t *testing.T) {
	f := newFixture(t)
	f.runner.failStart = func(n int) bool { return n%7 == 0 }
	clk := &fakeClock{t: time.Unix(1_000_000, 0), step: 2 * time.Minute}
	f.d.Timing.Now = clk.Now
	cycles := 0
	f.d.ServicePID = func() int { return 4242 }
	f.d.GroupRSS = func(pid int) (int64, error) {
		if pid == 4242 {
			cycles++
			return int64(100<<20) + int64(cycles)*(1<<20), nil // wächst mit jedem Zug um 1 MB
		}
		return 30 << 20, nil
	}
	var samples []SoakSample
	err := Soak(context.Background(), f.d, 3*time.Hour, func(s SoakSample) { samples = append(samples, s) })
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) < 20 {
		t.Fatalf("nur %d Proben", len(samples))
	}
	last := samples[len(samples)-1]
	if last.Failures == 0 {
		t.Errorf("Fehlstarts wurden nicht gezählt: %+v", last)
	}
	if !last.Leak || last.SlopeBytesPerHour < 1<<20 {
		t.Errorf("Leck nicht erkannt: %+v", last)
	}
	if samples[0].Leak {
		t.Error("Leck vor 20 Proben gemeldet")
	}
	for i, s := range samples {
		if s.Cycle != i+1 {
			t.Fatalf("Zyklus %d an Stelle %d", s.Cycle, i)
		}
		if s.RSSBytes <= 0 {
			t.Fatalf("RSS fehlt: %+v", s)
		}
	}
	if n := f.runner.alive(); n != 0 {
		t.Errorf("%d Clients übrig", n)
	}
}

func TestSoakSteadyMemoryIsNoLeak(t *testing.T) {
	f := newFixture(t)
	clk := &fakeClock{t: time.Unix(1_000_000, 0), step: 2 * time.Minute}
	f.d.Timing.Now = clk.Now
	f.d.ServicePID = func() int { return 4242 }
	var samples []SoakSample
	if err := Soak(context.Background(), f.d, time.Hour, func(s SoakSample) { samples = append(samples, s) }); err != nil {
		t.Fatal(err)
	}
	last := samples[len(samples)-1]
	if last.Leak || last.Failures != 0 {
		t.Fatalf("%+v", last)
	}
}

func TestSoakStopsOnContext(t *testing.T) {
	f := newFixture(t)
	f.d.Timing.SoakHold = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	n := 0
	err := Soak(ctx, f.d, 24*time.Hour, func(SoakSample) {
		n++
		if n == 2 {
			cancel()
		}
	})
	if err == nil || ctx.Err() == nil {
		t.Fatalf("err=%v", err)
	}
	if f.runner.alive() != 0 {
		t.Error("Client nach Abbruch noch aktiv")
	}
}

func TestSoakRefusesWhileSessionIsActive(t *testing.T) {
	f := newFixture(t)
	f.d.Busy = func() string { return "Eine Sitzung läuft" }
	err := Soak(context.Background(), f.d, time.Hour, func(SoakSample) { t.Fatal("keine Probe erwartet") })
	if err == nil || !strings.Contains(err.Error(), "Sitzung läuft") {
		t.Fatalf("err=%v", err)
	}
	if f.runner.starts != 0 {
		t.Fatal("Client gestartet")
	}
}

func TestLeakSlope(t *testing.T) {
	base := time.Unix(0, 0)
	mk := func(n int, perHour float64) []point {
		var ps []point
		for i := 0; i < n; i++ {
			at := base.Add(time.Duration(i) * 5 * time.Minute)
			ps = append(ps, point{at: at, rss: 100e6 + perHour*at.Sub(base).Hours()})
		}
		return ps
	}
	if _, leak := leakSlope(mk(19, 5e6)); leak {
		t.Error("unter 20 Proben wird nichts gemeldet")
	}
	if s, leak := leakSlope(mk(20, 5e6)); !leak || s < 4.9e6 || s > 5.1e6 {
		t.Errorf("Steigung %v leak=%v", s, leak)
	}
	if _, leak := leakSlope(mk(40, 0.5e6)); leak {
		t.Error("0,5 MB/h liegt unter der Schwelle")
	}
	if _, leak := leakSlope(mk(40, -3e6)); leak {
		t.Error("fallend ist kein Leck")
	}
	same := []point{}
	for i := 0; i < 25; i++ {
		same = append(same, point{at: base, rss: 1})
	}
	if _, leak := leakSlope(same); leak {
		t.Error("gleiche Zeitpunkte: keine Steigung berechenbar")
	}
}

func TestGroupRSSOfOwnProcess(t *testing.T) {
	rss, err := GroupRSS("/proc", os.Getpid())
	if err != nil || rss <= 0 {
		t.Fatalf("rss=%d err=%v", rss, err)
	}
	if _, err := GroupRSS("/proc", 1<<30); err == nil {
		t.Fatal("unbekannter Prozess muss einen Fehler ergeben")
	}
}
