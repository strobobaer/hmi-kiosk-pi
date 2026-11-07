package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hmikiosk/internal/config"
)

func hmisN(n int) []config.HMI {
	out := make([]config.HMI, n)
	for i := range out {
		out[i] = config.HMI{ID: "h" + string(rune('a'+i)), Host: "10.0.0.1", Port: 5900 + i, Reach: config.ReachPort}
	}
	return out
}

func TestReachabilitySkipsHMIInSession(t *testing.T) {
	var mu sync.Mutex
	checked := map[int]int{}
	r := newReachability(func() []config.HMI { return hmisN(2) }, func() string { return "ha" },
		func(_ context.Context, _ string, port int) bool {
			mu.Lock()
			defer mu.Unlock()
			checked[port]++
			return true
		})
	r.round(context.Background())
	if checked[5900] != 0 || checked[5901] != 1 {
		t.Fatalf("Prüfungen %v", checked)
	}
}

func TestReachabilityKeepsLastValueDuringSession(t *testing.T) {
	var session atomic.Value
	session.Store("")
	var answer atomic.Bool
	answer.Store(true)
	r := newReachability(func() []config.HMI { return hmisN(1) }, func() string { return session.Load().(string) },
		func(context.Context, string, int) bool { return answer.Load() })
	r.round(context.Background())
	if !r.reachable("ha") {
		t.Fatal("nicht erreichbar")
	}
	session.Store("ha")
	answer.Store(false)
	r.round(context.Background())
	if !r.reachable("ha") {
		t.Fatal("Wert in der Sitzung verloren")
	}
	session.Store("")
	r.round(context.Background())
	if r.reachable("ha") {
		t.Fatal("Änderung nach der Sitzung nicht übernommen")
	}
}

func TestReachabilityBoundedConcurrencyAndLimit(t *testing.T) {
	var cur, peak atomic.Int32
	var noLimit atomic.Bool
	r := newReachability(func() []config.HMI { return hmisN(26) }, func() string { return "" },
		func(ctx context.Context, _ string, _ int) bool {
			if dl, ok := ctx.Deadline(); !ok || time.Until(dl) > reachLimit {
				noLimit.Store(true)
			}
			n := cur.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			cur.Add(-1)
			return true
		})
	r.round(context.Background())
	if p := peak.Load(); p > reachConcurrency || p < 2 {
		t.Fatalf("gleichzeitig %d", p)
	}
	if noLimit.Load() {
		t.Fatal("Prüfung ohne 2-s-Frist")
	}
	if reachEvery != 10*time.Second || reachLimit != 2*time.Second || reachConcurrency != 8 {
		t.Fatal("Werte weichen von der Vorgabe ab")
	}
}

func TestReachabilityForgetsDeletedHMI(t *testing.T) {
	var list atomic.Value
	list.Store(hmisN(2))
	r := newReachability(func() []config.HMI { return list.Load().([]config.HMI) }, func() string { return "" },
		func(context.Context, string, int) bool { return true })
	r.round(context.Background())
	list.Store(hmisN(1))
	r.round(context.Background())
	if r.reachable("hb") {
		t.Fatal("gelöschtes HMI bleibt")
	}
}

func TestHostCacheNoDNSInTCP(t *testing.T) {
	var lookups atomic.Int32
	c := newHostCache(
		func(ctx context.Context, host string) ([]net.IP, error) {
			lookups.Add(1)
			if host == "weg" {
				return nil, errors.New("nicht auflösbar")
			}
			return []net.IP{net.ParseIP("10.0.0.9"), net.ParseIP("10.0.0.8")}, nil
		},
		func(ip net.IP, port int) (bool, error) {
			return ip.String() == "10.0.0.8" && port == 5900, nil
		})
	if c.tcp("hmi.local", 5900) {
		t.Fatal("ohne Auflösung verbunden")
	}
	if lookups.Load() != 0 {
		t.Fatal("TCP fragt DNS")
	}
	if err := c.resolve(context.Background(), "hmi.local"); err != nil {
		t.Fatal(err)
	}
	if !c.tcp("hmi.local", 5900) || c.tcp("hmi.local", 5901) {
		t.Fatal("TCP-Ergebnis falsch")
	}
	if lookups.Load() != 1 {
		t.Fatalf("%d DNS-Anfragen", lookups.Load())
	}
	if err := c.resolve(context.Background(), "weg"); err == nil {
		t.Fatal("kein Fehler")
	}
	if c.tcp("weg", 5900) {
		t.Fatal("verbunden trotz fehlgeschlagener Auflösung")
	}
}

func TestHostCacheFailedResolveDropsOldIPs(t *testing.T) {
	fail := false
	c := newHostCache(func(context.Context, string) ([]net.IP, error) {
		if fail {
			return nil, errors.New("weg")
		}
		return []net.IP{net.ParseIP("10.0.0.1")}, nil
	}, func(net.IP, int) (bool, error) { return true, nil })
	if err := c.resolve(context.Background(), "h"); err != nil {
		t.Fatal(err)
	}
	fail = true
	if err := c.resolve(context.Background(), "h"); err == nil {
		t.Fatal("kein Fehler")
	}
	if c.tcp("h", 1) {
		t.Fatal("alte Adresse gilt noch")
	}
}

func TestHostCacheLiteralIPNeedsNoLookup(t *testing.T) {
	var lookups int
	c := newHostCache(func(context.Context, string) ([]net.IP, error) {
		lookups++
		return nil, errors.New("nie")
	}, func(ip net.IP, _ int) (bool, error) { return ip.Equal(net.ParseIP("127.0.0.1")), nil })
	if err := c.resolve(context.Background(), "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if !c.tcp("127.0.0.1", 5900) || lookups != 0 {
		t.Fatalf("Literal: %d Anfragen", lookups)
	}
}

func TestHostCacheResolveHonoursLimit(t *testing.T) {
	var limited bool
	c := newHostCache(func(ctx context.Context, _ string) ([]net.IP, error) {
		dl, ok := ctx.Deadline()
		limited = ok && time.Until(dl) <= resolveLimit
		return nil, errors.New("egal")
	}, nil)
	c.resolve(context.Background(), "h")
	if !limited || resolveLimit != 2*time.Second {
		t.Fatal("Auflösung ohne 2-s-Frist")
	}
}

func reachWith(h config.HMI, port func(context.Context, string, int) bool, ping func(context.Context, string) (bool, error)) *reachability {
	r := newReachability(func() []config.HMI { return []config.HMI{h} }, func() string { return "" }, port)
	r.ping = ping
	return r
}

func TestReachabilityDefaultsToPingNotPort(t *testing.T) {
	for _, mode := range []string{"", config.ReachPing} {
		var ports, pings atomic.Int32
		r := reachWith(config.HMI{ID: "x", Host: "hmi.local", Port: 5900, Reach: mode},
			func(context.Context, string, int) bool { ports.Add(1); return true },
			func(_ context.Context, host string) (bool, error) { pings.Add(1); return host == "hmi.local", nil })
		r.round(context.Background())
		if ports.Load() != 0 || pings.Load() != 1 || !r.reachable("x") {
			t.Fatalf("Prüfart %q: Port %d, Ping %d, erreichbar %v", mode, ports.Load(), pings.Load(), r.reachable("x"))
		}
	}
}

func TestReachabilityPortModeKeepsTCPCheck(t *testing.T) {
	var ports, pings atomic.Int32
	r := reachWith(config.HMI{ID: "x", Host: "h", Port: 5902, Reach: config.ReachPort},
		func(_ context.Context, _ string, port int) bool { ports.Add(1); return port == 5902 },
		func(context.Context, string) (bool, error) { pings.Add(1); return false, nil })
	r.round(context.Background())
	if ports.Load() != 1 || pings.Load() != 0 || !r.reachable("x") {
		t.Fatalf("Port %d, Ping %d", ports.Load(), pings.Load())
	}
}

func TestReachabilityOffNeverChecks(t *testing.T) {
	var calls atomic.Int32
	r := reachWith(config.HMI{ID: "x", Host: "h", Port: 5900, Reach: config.ReachOff},
		func(context.Context, string, int) bool { calls.Add(1); return false },
		func(context.Context, string) (bool, error) { calls.Add(1); return false, nil })
	r.round(context.Background())
	if calls.Load() != 0 {
		t.Fatalf("%d Prüfungen trotz „aus“", calls.Load())
	}
	if !r.reachable("x") {
		t.Fatal("Kachel zeigt einen Erreichbarkeits-Zustand")
	}
}

func TestPingerPingsResolvedAddressWithFixedArguments(t *testing.T) {
	var got [][]string
	p := &pinger{
		lookup: func(_ context.Context, host string) ([]net.IP, error) {
			if host == "weg" {
				return nil, errors.New("unbekannt")
			}
			return []net.IP{net.ParseIP("fe80::1"), net.ParseIP("10.0.0.7")}, nil
		},
		run: func(_ context.Context, args []string) error {
			got = append(got, args)
			return nil
		},
	}
	if ok, err := p.ping(context.Background(), "hmi.local"); !ok || err != nil {
		t.Fatal("Ping gescheitert")
	}
	want := []string{"-n", "-c", "1", "-W", "1", "10.0.0.7"}
	if len(got) != 1 || !slices.Equal(got[0], want) {
		t.Fatalf("Argumente %v, erwartet %v", got, want)
	}
	if ok, _ := p.ping(context.Background(), "[2001:db8::5]"); !ok || got[1][5] != "2001:db8::5" {
		t.Fatalf("Literal: %v", got)
	}
	if ok, err := p.ping(context.Background(), "weg"); ok || err != nil || len(got) != 2 {
		t.Fatal("unauflösbarer Name wurde gepingt")
	}
	p.run = func(context.Context, []string) error { return errors.New("exit status 1") }
	if ok, err := p.ping(context.Background(), "10.0.0.7"); ok || err != nil {
		t.Fatal("fehlgeschlagener Ping gilt als erreichbar")
	}
}

// Fehlt ping, zeigt die Kachel keinen Zustand (wie „aus“) statt „nicht erreichbar“.
func TestReachabilityPingUnavailableShowsNoState(t *testing.T) {
	r := reachWith(config.HMI{ID: "x", Host: "10.0.0.7", Port: 5900, Reach: config.ReachPing},
		func(context.Context, string, int) bool { t.Error("Port statt Ping geprüft"); return false },
		func(context.Context, string) (bool, error) { return false, errPingUnavailable })
	r.round(context.Background())
	if !r.reachable("x") {
		t.Fatal("fehlendes ping meldet das HMI als nicht erreichbar")
	}
}

func TestPingerUnavailableLogsOnce(t *testing.T) {
	var logs []string
	p := &pinger{
		lookup: func(context.Context, string) ([]net.IP, error) { return nil, errors.New("nicht gebraucht") },
		logf:   func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) },
	}
	unavailable := func(what string, runErr error) {
		t.Helper()
		p.run = func(context.Context, []string) error { return runErr }
		for range 3 {
			ok, err := p.ping(context.Background(), "10.0.0.7")
			if ok || !errors.Is(err, errPingUnavailable) {
				t.Fatalf("%s: ok=%v err=%v", what, ok, err)
			}
		}
	}
	unavailable("fehlt", &exec.Error{Name: "ping", Err: exec.ErrNotFound})
	unavailable("nicht startbar", &fs.PathError{Op: "fork/exec", Path: "/usr/bin/ping", Err: fs.ErrPermission})
	if len(logs) != 1 || !strings.Contains(logs[0], "ping nicht verfügbar") || !strings.Contains(logs[0], "ausgesetzt") {
		t.Fatalf("Protokoll %q", logs)
	}
	// Antwortet das Ziel nicht, ist ping da: Das ist „nicht erreichbar“, kein Ausfall.
	p.run = func(context.Context, []string) error { return errors.New("exit status 1") }
	if ok, err := p.ping(context.Background(), "10.0.0.7"); ok || err != nil {
		t.Fatalf("keine Antwort: ok=%v err=%v", ok, err)
	}
	if len(logs) != 2 || !strings.Contains(logs[1], "ping wieder verfügbar") {
		t.Fatalf("Protokoll nach Rückkehr %q", logs)
	}
	unavailable("wieder weg", &exec.Error{Name: "ping", Err: exec.ErrNotFound})
	if len(logs) != 3 {
		t.Fatalf("erneuter Ausfall nicht protokolliert: %q", logs)
	}
}

// Der echte Aufruf: ohne ping im Suchpfad meldet newPinger den Ausfall.
func TestNewPingerWithoutBinaryIsUnavailable(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	p := newPinger()
	p.logf = func(string, ...any) {}
	if ok, err := p.ping(context.Background(), "127.0.0.1"); ok || !errors.Is(err, errPingUnavailable) {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}
