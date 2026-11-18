package oled

import (
	"bytes"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type busFactory struct {
	mu    sync.Mutex
	buses []*fakeBus
	opens int
	// failFirstAt: Schreibung, bei der der erste Bus scheitert (0 = nie)
	failFirstAt int
}

func (f *busFactory) open(dev string, addr int) (bus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opens++
	b := &fakeBus{}
	if len(f.buses) == 0 {
		b.failAt = f.failFirstAt
	}
	f.buses = append(f.buses, b)
	return b, nil
}

func (f *busFactory) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.opens
}

func (f *busFactory) first() *fakeBus {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.buses[0]
}

func newTestService(t *testing.T, f *busFactory) *Service {
	t.Helper()
	dir := t.TempDir()
	s := NewService(t.Logf)
	s.open = f.open
	s.interval = 5 * time.Millisecond
	s.retry = 10 * time.Millisecond
	s.procStat = writeTemp(t, dir, "stat", "cpu  10 0 10 80 0 0 0 0\n")
	s.meminfo = writeTemp(t, dir, "meminfo", "MemTotal: 1000 kB\nMemAvailable: 500 kB\n")
	s.tempPath = writeTemp(t, dir, "temp", "45000\n")
	t.Cleanup(s.Close)
	return s
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 400; i++ {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("Zeitüberschreitung: %s", what)
}

func state(s *Service) string { st, _ := s.Status(); return st }

func TestServiceRetriesAfterWriteError(t *testing.T) {
	// Init = 1 Schreibung, 1. Show = 16 -> 18 ist die erste Schreibung des 2. Show.
	f := &busFactory{failFirstAt: 18}
	s := newTestService(t, f)
	s.Apply(Settings{Type: "sh1106", Bus: "/dev/i2c-1", Addr: 0x3C})
	waitFor(t, "Fehlerstatus", func() bool { return state(s) == "error" })
	_, detail := s.Status()
	if detail == "" {
		t.Error("Fehlermeldung fehlt")
	}
	waitFor(t, "Neustart", func() bool { return f.count() >= 2 && state(s) == "ok" })
	if st, d := s.Status(); st != "ok" || d != "/dev/i2c-1 0x3c" {
		t.Errorf("Status %q %q", st, d)
	}
}

func TestServiceOffClearsDisplay(t *testing.T) {
	f := &busFactory{}
	s := newTestService(t, f)
	s.Apply(Settings{Type: "sh1106", Bus: "/dev/i2c-1", Addr: 0x3C})
	waitFor(t, "läuft", func() bool { return state(s) == "ok" && f.count() == 1 && len(f.first().snapshot()) > 17 })
	s.Apply(Settings{})
	if st, d := s.Status(); st != "none" || d != "aus" {
		t.Errorf("Status %q %q", st, d)
	}
	w := f.first().snapshot()
	if !bytes.Equal(w[len(w)-1], []byte{0x00, 0xAE}) {
		t.Errorf("letzte Schreibung %x, erwartet Off", w[len(w)-1])
	}
	if !f.first().closed {
		t.Error("Bus nicht geschlossen")
	}
}

func TestServiceCloseWritesOff(t *testing.T) {
	f := &busFactory{}
	s := newTestService(t, f)
	s.Apply(Settings{Type: "sh1106", Bus: "/dev/i2c-1", Addr: 0x3C})
	waitFor(t, "läuft", func() bool { return f.count() == 1 && len(f.first().snapshot()) > 17 })
	s.Close()
	w := f.first().snapshot()
	if !bytes.Equal(w[len(w)-1], []byte{0x00, 0xAE}) {
		t.Errorf("letzte Schreibung %x, erwartet Off", w[len(w)-1])
	}
}

func TestServiceUnsupported(t *testing.T) {
	f := &busFactory{}
	s := newTestService(t, f)
	s.Apply(Settings{Type: "ssd9999", Unsupported: "Displaytyp nicht unterstützt"})
	time.Sleep(30 * time.Millisecond)
	if st, d := s.Status(); st != "error" || d != "Displaytyp nicht unterstützt" {
		t.Errorf("Status %q %q", st, d)
	}
	if f.count() != 0 {
		t.Error("openBus wurde gerufen")
	}
}

func TestServiceOpenErrorRetries(t *testing.T) {
	var n atomic.Int32
	s := newTestService(t, &busFactory{})
	s.open = func(string, int) (bus, error) { n.Add(1); return nil, errors.New("kein Gerät") }
	s.Apply(Settings{Type: "sh1106", Bus: "/dev/i2c-1", Addr: 0x3C})
	waitFor(t, "zweiter Versuch", func() bool { return n.Load() >= 2 })
	if st, d := s.Status(); st != "error" || d != "kein Gerät" {
		t.Errorf("Status %q %q", st, d)
	}
}

func writeTemp(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := dir + "/" + name
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}
