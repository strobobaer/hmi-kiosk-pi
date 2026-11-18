package card

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
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

func signalled(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	case <-time.After(2 * time.Second):
		return false
	}
}

func TestServiceReaderState(t *testing.T) {
	// aus -> none
	s := newTestService(&opLog{}, nil)
	defer s.Close()
	if got := s.ReaderState(); got != StateNone {
		t.Fatalf("vor Apply: %q", got)
	}
	sig, stop := s.WatchState()
	defer stop()
	s.Apply(Settings{})
	if got := s.ReaderState(); got != StateNone {
		t.Fatalf("aus: %q", got)
	}

	// Start -> ok (kein Aufblitzen der Eingabe), Signal beim Wechsel none -> ok
	block := make(chan struct{})
	s.open = func(Settings) (reader, error) { <-block; return nil, errors.New("kaputt") }
	s.Apply(Settings{Type: "usb"})
	if !signalled(sig) {
		t.Fatal("kein Signal beim Start")
	}
	if got := s.ReaderState(); got != StateOK {
		t.Fatalf("Start: %q", got)
	}
	// Öffnen scheitert -> error, Signal
	close(block)
	if !signalled(sig) {
		t.Fatal("kein Signal beim Fehler")
	}
	if got := s.ReaderState(); got != StateError {
		t.Fatalf("Fehler: %q", got)
	}

	// Wieder aus -> none, Signal (Apply schreibt direkt)
	s.Apply(Settings{})
	if !signalled(sig) {
		t.Fatal("kein Signal bei aus")
	}
	if got := s.ReaderState(); got != StateNone {
		t.Fatalf("aus: %q", got)
	}
}

func TestServiceReaderStateStable(t *testing.T) {
	l := &opLog{}
	s := newTestService(l, nil)
	defer s.Close()
	s.stable = 30 * time.Millisecond
	s.Apply(Settings{Type: "usb"})
	waitFor(t, "aktiv", func() bool { return s.Status().Active })
	if got := s.ReaderState(); got != StateOK {
		t.Fatalf("stabil: %q", got)
	}
}

func TestServiceWatchStateStop(t *testing.T) {
	s := newTestService(&opLog{}, nil)
	defer s.Close()
	sig, stop := s.WatchState()
	stop()
	s.Apply(Settings{Type: "usb"})
	select {
	case <-sig:
		t.Error("Signal nach stop")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestServiceUnsupportedIsErrorWithoutRetry(t *testing.T) {
	l := &opLog{}
	var logged []string
	var mu sync.Mutex
	s := newTestService(l, nil)
	s.logf = func(f string, a ...any) { mu.Lock(); logged = append(logged, f); mu.Unlock() }
	defer s.Close()
	const reason = "Lesertyp „pn7150“ wird von dieser Version nicht unterstützt"
	s.Apply(Settings{Type: "pn7150", Unsupported: reason})
	if st := s.Status(); st != (Status{Detail: reason, State: StateError}) {
		t.Errorf("Status %+v", st)
	}
	if got := s.ReaderState(); got != StateError {
		t.Errorf("ReaderState %q", got)
	}
	if _, err := s.Probe(context.Background()); err == nil || err.Error() != reason {
		t.Errorf("Probe: %v", err)
	}
	time.Sleep(30 * time.Millisecond) // mehrere retry-Zeiten
	if ops := l.get(); len(ops) != 0 {
		t.Errorf("open darf nie aufgerufen werden: %v", ops)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(logged) != 1 {
		t.Errorf("Logzeilen %d, erwartet 1", len(logged))
	}
}

func TestOpenReaderMissingSPIDevice(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("nur unter Linux")
	}
	dev := filepath.Join(t.TempDir(), "spidev9.9")
	_, err := openReader(Settings{Type: TypePN532I2C, Bus: BusSPI, SPIDev: dev})
	want := "SPI ist im System nicht eingeschaltet (" + dev + " fehlt): neues Abbild oder dtparam=spi=on in config.txt"
	if err == nil || err.Error() != want {
		t.Errorf("Fehler %v, erwartet %q", err, want)
	}
}

func TestOpenReaderMissingUARTDevice(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("nur unter Linux")
	}
	dev := filepath.Join(t.TempDir(), "ttyAMA9")
	_, err := openReader(Settings{Type: TypePN532I2C, Bus: BusUART, UARTDev: dev})
	want := "UART ist im System nicht eingeschaltet (" + dev + " fehlt): neues Abbild oder dtparam=uart0=on in config.txt"
	if err == nil || err.Error() != want {
		t.Errorf("Fehler %v, erwartet %q", err, want)
	}
}

func TestOpenReaderNoPermission(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("nur unter Linux")
	}
	if os.Geteuid() == 0 {
		t.Skip("als root gibt es keine Zugriffsverweigerung")
	}
	dev := filepath.Join(t.TempDir(), "gerät")
	if err := os.WriteFile(dev, nil, 0); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		st    Settings
		group string
	}{
		{Settings{Type: TypePN532I2C, Bus: BusSPI, SPIDev: dev}, "spi"},
		{Settings{Type: TypePN532I2C, Bus: BusUART, UARTDev: dev}, "dialout"},
	} {
		_, err := openReader(tc.st)
		want := "Kein Zugriff auf " + dev + ": Dienstbenutzer nicht in der Gruppe " + tc.group + " (neues Abbild oder „usermod -aG " + tc.group + " hmikiosk“, danach Dienst neu starten)"
		if err == nil || err.Error() != want {
			t.Errorf("%s: Fehler %v, erwartet %q", tc.group, err, want)
		}
	}
}
