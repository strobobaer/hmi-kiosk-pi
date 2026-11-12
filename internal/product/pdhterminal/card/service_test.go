package card

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeReader struct {
	name string
	log  func(string)
	run  func(ctx context.Context, emit func(string)) error
}

func (f *fakeReader) Run(ctx context.Context, emit func(string)) error {
	defer f.log("close " + f.name)
	if f.run != nil {
		return f.run(ctx, emit)
	}
	<-ctx.Done()
	return nil
}
func (f *fakeReader) Probe(ctx context.Context) (string, error) { return "Probe " + f.name, nil }

type opLog struct {
	mu  sync.Mutex
	ops []string
}

func (l *opLog) add(s string) { l.mu.Lock(); l.ops = append(l.ops, s); l.mu.Unlock() }
func (l *opLog) get() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.ops...)
}

func newTestService(l *opLog, run func(ctx context.Context, emit func(string)) error) *Service {
	s := NewService(NewHub(time.Now), func(string, ...any) {})
	s.retry = 5 * time.Millisecond
	s.stable = 5 * time.Millisecond
	s.open = func(st Settings) (reader, error) {
		l.add("open " + st.Type)
		return &fakeReader{name: st.Type, log: l.add, run: run}, nil
	}
	return s
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("Timeout: %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestServiceApplyStopsOld(t *testing.T) {
	l := &opLog{}
	s := newTestService(l, nil)
	defer s.Close()
	s.Apply(Settings{Type: "a"})
	waitFor(t, "open a", func() bool { return len(l.get()) >= 1 })
	s.Apply(Settings{Type: "a"}) // gleiche Settings: nichts
	s.Apply(Settings{Type: "b"})
	waitFor(t, "open b", func() bool { return len(l.get()) >= 3 })
	if got := strings.Join(l.get(), ","); got != "open a,close a,open b" {
		t.Errorf("Reihenfolge %s", got)
	}
	waitFor(t, "aktiv", func() bool { return s.Status().Active })
}

func TestServiceGrabFails(t *testing.T) {
	l := &opLog{}
	s := newTestService(l, func(ctx context.Context, emit func(string)) error {
		return errors.New("Leser kann nicht exklusiv übernommen werden: busy")
	})
	defer s.Close()
	ch, cancel, _ := s.Hub().Subscribe()
	defer cancel()
	s.Apply(Settings{Type: "usb"})
	waitFor(t, "Fehlerstatus", func() bool {
		st := s.Status()
		return !st.Active && strings.Contains(st.Detail, "exklusiv")
	})
	// Neustart nach retry
	waitFor(t, "Neustart", func() bool { return len(l.get()) >= 4 })
	if st := s.Status(); st.Active {
		t.Errorf("Status %+v", st)
	}
	select {
	case e := <-ch:
		t.Errorf("unerwartetes Ereignis %v", e)
	default:
	}
}

func TestServiceOff(t *testing.T) {
	l := &opLog{}
	s := newTestService(l, nil)
	defer s.Close()
	s.Apply(Settings{})
	if st := s.Status(); st.Active || st.Detail != "aus" {
		t.Errorf("Status %+v", st)
	}
	if len(l.get()) != 0 {
		t.Errorf("open aufgerufen: %v", l.get())
	}
	if _, err := s.Probe(context.Background()); err == nil {
		t.Error("Probe bei aus sollte fehlschlagen")
	}
}

func TestServiceEmitsToHub(t *testing.T) {
	l := &opLog{}
	s := newTestService(l, func(ctx context.Context, emit func(string)) error {
		emit("ABC")
		<-ctx.Done()
		return nil
	})
	defer s.Close()
	ch, cancel, _ := s.Hub().Subscribe()
	defer cancel()
	s.Apply(Settings{Type: "usb"})
	select {
	case e := <-ch:
		if e.UID != "ABC" {
			t.Errorf("uid %q", e.UID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("kein Ereignis")
	}
}

func TestServiceCloseStopsReader(t *testing.T) {
	l := &opLog{}
	s := newTestService(l, nil)
	s.Apply(Settings{Type: "a"})
	waitFor(t, "open", func() bool { return len(l.get()) == 1 })
	s.Close()
	if got := strings.Join(l.get(), ","); got != "open a,close a" {
		t.Errorf("%s", got)
	}
	s.Apply(Settings{Type: "b"}) // nach Close wirkungslos
	if len(l.get()) != 2 {
		t.Errorf("%v", l.get())
	}
}

func TestServiceProbeRunning(t *testing.T) {
	l := &opLog{}
	s := newTestService(l, nil)
	defer s.Close()
	s.Apply(Settings{Type: "a"})
	waitFor(t, "aktiv", func() bool { return s.Status().Active })
	got, err := s.Probe(context.Background())
	if err != nil || got != "Probe a" {
		t.Errorf("%q %v", got, err)
	}
}

func TestOpenReaderKnownTypes(t *testing.T) {
	// Ohne Hardware schlägt das Öffnen fehl, aber die Typen der Konfiguration sind bekannt.
	for _, typ := range []string{TypePN532I2C, TypeUSB} {
		if _, err := openReader(Settings{Type: typ, I2CBus: "/nonexistent"}); err != nil && strings.Contains(err.Error(), "unbekannter") {
			t.Errorf("%s: %v", typ, err)
		}
	}
	if _, err := openReader(Settings{Type: "pn532"}); err == nil || !strings.Contains(err.Error(), "unbekannter") {
		t.Errorf("Alias pn532 darf nicht gelten: %v", err)
	}
}
