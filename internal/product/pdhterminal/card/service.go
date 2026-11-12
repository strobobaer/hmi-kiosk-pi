package card

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// Lesertypen, wie sie die Konfiguration verwendet.
const (
	TypePN532I2C = "pn532-i2c"
	TypeUSB      = "usb"
)

// Settings beschreibt den gewählten Kartenleser (das Paket kennt config nicht).
type Settings struct {
	Type      string // "" oder "aus" (kein Leser), TypePN532I2C oder TypeUSB
	I2CBus    string // Nummer oder Gerätepfad, z. B. "1"
	I2CAddr   int    // 0 = 0x24
	USBDevice string // vvvv:pppp oder Namensteil
	Format    string // nur PN532, siehe FormatUID
}

func (s Settings) off() bool { return s.Type == "" || s.Type == "aus" }

// Status ist der Zustand des Lesers für die Anzeige.
type Status struct {
	Active bool
	Detail string
}

// reader liest Karten bis ctx endet (nil) oder ein Fehler auftritt.
type reader interface {
	Run(ctx context.Context, emit func(uid string)) error
	Probe(ctx context.Context) (string, error)
}

const (
	defaultRetry  = 5 * time.Second // Neustart nach einem Lesefehler
	defaultStable = time.Second     // so lange läuft ein Leser, bevor er als aktiv gilt
)

// Service betreibt höchstens einen Leser und veröffentlicht dessen UIDs im Hub.
type Service struct {
	hub  *Hub
	logf func(string, ...any)

	open   func(Settings) (reader, error) // von Tests ersetzbar
	retry  time.Duration
	stable time.Duration

	applyMu sync.Mutex // serialisiert Apply/Close

	mu      sync.Mutex // schützt die folgenden Felder
	cur     Settings
	started bool
	cancel  context.CancelFunc
	done    chan struct{}
	rd      reader // laufender Leser, sonst nil
	status  Status
	watch   map[int]chan struct{} // Beobachter von ReaderState
	watchID int
	closed  bool
}

// NewService erzeugt einen Dienst ohne Leser; Apply startet ihn.
func NewService(hub *Hub, logf func(string, ...any)) *Service {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Service{hub: hub, logf: logf, open: openReader, retry: defaultRetry, stable: defaultStable,
		status: Status{Detail: "aus"}}
}

// Hub liefert den Ereignisverteiler.
func (s *Service) Hub() *Hub { return s.hub }

// Status liefert den aktuellen Zustand.
func (s *Service) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// setStatus setzt den Status, solange der Lauf (ctx) nicht beendet ist.
func (s *Service) setStatus(ctx context.Context, st Status) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx.Err() == nil {
		s.setStatusLocked(st)
	}
}

// Apply stoppt den laufenden Leser (und wartet auf dessen Ende), dann startet es den neuen.
// Unveränderte Einstellungen bewirken nichts.
func (s *Service) Apply(st Settings) {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	s.mu.Lock()
	if s.closed || (s.started && s.cur == st) {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	s.stop()

	s.mu.Lock()
	s.cur, s.started = st, true
	if st.off() {
		s.setStatusLocked(Status{Detail: "aus"})
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.cancel, s.done = cancel, done
	s.setStatusLocked(Status{Detail: "Leser startet"})
	s.mu.Unlock()
	go s.loop(ctx, st, done)
}

// stop beendet den laufenden Leser und wartet darauf.
func (s *Service) stop() {
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	s.cancel, s.done = nil, nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

// Close beendet den Leser; danach ist Apply wirkungslos.
func (s *Service) Close() {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.stop()
}

func (s *Service) loop(ctx context.Context, st Settings, done chan struct{}) {
	defer close(done)
	for ctx.Err() == nil {
		err := s.runOnce(ctx, st)
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			err = errors.New("Leser beendet")
		}
		s.logf("kartenleser: %v", err)
		s.setStatus(ctx, Status{Detail: err.Error()})
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.retry):
		}
	}
}

func (s *Service) runOnce(ctx context.Context, st Settings) error {
	r, err := s.open(st)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.rd = r
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.rd = nil
		s.mu.Unlock()
	}()

	// Als aktiv gilt der Leser erst, wenn er stable lang ohne Fehler läuft; sonst würde
	// ein sofort scheiternder Leser (z. B. Grab) zwischen "bereit" und Fehler flackern.
	t := time.AfterFunc(s.stable, func() {
		s.setStatus(ctx, Status{Active: true, Detail: readyText(st, r)})
	})
	defer t.Stop()
	return r.Run(ctx, s.hub.Publish)
}

func readyText(st Settings, r reader) string {
	if u, ok := r.(*usbReader); ok {
		return "USB-Leser bereit: " + usbNames(u.devs)
	}
	if st.Type == TypePN532I2C {
		return "PN532 bereit"
	}
	return "Leser bereit"
}

// Probe prüft den Leser der aktuellen Einstellungen (laufend oder frisch geöffnet).
func (s *Service) Probe(ctx context.Context) (string, error) {
	s.mu.Lock()
	st, rd := s.cur, s.rd
	s.mu.Unlock()
	if st.off() {
		return "", errors.New("Kartenleser aus")
	}
	if rd != nil {
		return rd.Probe(ctx)
	}
	r, err := s.open(st)
	if err != nil {
		return "", err
	}
	if c, ok := r.(io.Closer); ok {
		defer c.Close()
	}
	return r.Probe(ctx)
}

// openReader baut den Leser zum Typ; die Hardwarezugriffe liegen plattformabhängig in
// i2c_linux.go/usb_linux.go.
func openReader(st Settings) (reader, error) {
	switch st.Type {
	case TypePN532I2C:
		conn, err := openI2C(st.I2CBus, st.I2CAddr)
		if err != nil {
			return nil, err
		}
		return newPN532(conn, st.Format), nil
	case TypeUSB:
		devs := matchDevices(ListInputDevices("/dev/input"), st.USBDevice)
		if len(devs) == 0 {
			return nil, fmt.Errorf("USB-Leser %q nicht gefunden", st.USBDevice)
		}
		return &usbReader{devs: devs}, nil
	}
	return nil, fmt.Errorf("unbekannter Lesertyp %q", st.Type)
}

// Leserzustände für den Kartenstrom (Ereignis reader).
const (
	StateOK    = "ok"    // Leser läuft (oder startet gerade)
	StateNone  = "none"  // kein Leser eingestellt
	StateError = "error" // Leser eingestellt, aber nicht verfügbar
)

func stateOf(st Status) string {
	switch {
	case st.Active, st.Detail == "Leser startet":
		return StateOK
	case st.Detail == "aus":
		return StateNone
	}
	return StateError
}

// ReaderState liefert den Zustand für die PDH-Seite: Typ aus -> none; aktiv oder startend
// -> ok (damit die Eingabe beim Start nicht aufblitzt); sonst error.
func (s *Service) ReaderState() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return stateOf(s.status)
}

// WatchState liefert einen Kanal (Puffer 1), der bei jeder Änderung von ReaderState ein
// Signal bekommt, und eine Funktion zum Abmelden.
func (s *Service) WatchState() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	s.mu.Lock()
	if s.watch == nil {
		s.watch = map[int]chan struct{}{}
	}
	id := s.watchID
	s.watchID++
	s.watch[id] = ch
	s.mu.Unlock()
	return ch, func() {
		s.mu.Lock()
		delete(s.watch, id)
		s.mu.Unlock()
	}
}

// setStatusLocked ist die einzige Stelle, die s.status schreibt (s.mu gehalten). Ändert
// sich dabei ReaderState, werden die Beobachter benachrichtigt.
func (s *Service) setStatusLocked(st Status) {
	before := stateOf(s.status)
	s.status = st
	if stateOf(st) == before {
		return
	}
	for _, ch := range s.watch {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
