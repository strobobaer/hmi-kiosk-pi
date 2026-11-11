package appcore

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestListenLoopbackOnly(t *testing.T) {
	for _, addr := range []string{":0", "0.0.0.0:0", "[::]:0", "192.0.2.1:0", "localhost:0", "hmi.example:0", "127.0.0.1"} {
		if l, err := ListenLoopback(addr); err == nil {
			l.Close()
			t.Errorf("%q gebunden, erwartet Ablehnung", addr)
		}
	}
	l, err := ListenLoopback("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if a := l.Addr().(*net.TCPAddr); !a.IP.IsLoopback() {
		t.Fatalf("gebunden an %v", a)
	}
}

type stepClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *stepClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *stepClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func TestLocalAdminPresenceExpiresAfterIdle(t *testing.T) {
	clk := &stepClock{now: time.Unix(1000, 0)}
	p := &AdminPresence{Clock: clk}
	if p.Active() {
		t.Fatal("ohne Aufruf aktiv")
	}
	p.Touch()
	clk.advance(LocalAdminIdle - time.Second)
	if !p.Active() {
		t.Fatal("vor Ablauf nicht aktiv")
	}
	clk.advance(time.Second)
	if p.Active() {
		t.Fatal("nach 5 Minuten noch aktiv")
	}
	p.Touch()
	p.Left()
	if p.Active() {
		t.Fatal("nach Rückkehr noch aktiv")
	}
}

func TestServeLocalAdminRejectsNonLoopback(t *testing.T) {
	if stop, err := ServeLocalAdmin(context.Background(), "0.0.0.0:0", http.NotFoundHandler(), nil); err == nil {
		stop()
		t.Fatal("Nicht-Loopback-Adresse gebunden")
	}
}

func TestServeLocalAdminServesAndStops(t *testing.T) {
	// Freien Port ermitteln: ServeLocalAdmin meldet die Adresse nicht zurück.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	p := &AdminPresence{}
	p.Touch()
	stop, err := ServeLocalAdmin(context.Background(), addr, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "ok")
	}), p)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get("http://" + addr + "/")
	if err != nil {
		stop()
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), "ok") {
		t.Fatalf("Antwort %q", b)
	}
	stop()
	if p.Active() {
		t.Fatal("Präsenz nach stop noch aktiv")
	}
	if _, err := http.Get("http://" + addr + "/"); err == nil {
		t.Fatal("Server läuft nach stop")
	}
}
