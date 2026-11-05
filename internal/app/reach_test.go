package app

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hmikiosk/internal/config"
)

func hmisN(n int) []config.HMI {
	out := make([]config.HMI, n)
	for i := range out {
		out[i] = config.HMI{ID: "h" + string(rune('a'+i)), Host: "10.0.0.1", Port: 5900 + i}
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
