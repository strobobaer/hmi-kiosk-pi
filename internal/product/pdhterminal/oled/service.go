package oled

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
)

// Settings beschreibt das gewählte Display (das Paket kennt config nicht).
type Settings struct {
	Type        string // "" (kein Display) oder "sh1106"
	Bus         string // Gerätepfad, z. B. /dev/i2c-1
	Addr        int
	Unsupported string // Grund, wenn der Typ dieser Version unbekannt ist (Display bleibt aus)
}

func (s Settings) off() bool { return s.Type == "" || s.Unsupported != "" }

const (
	defaultInterval = time.Second     // Bildwechsel
	defaultRetry    = 5 * time.Second // Neustart nach einem Fehler
)

// Service betreibt höchstens ein Display und aktualisiert es jede Sekunde.
type Service struct {
	logf func(string, ...any)

	open     func(dev string, addr int) (bus, error) // von Tests ersetzbar
	interval time.Duration
	retry    time.Duration
	procStat string
	meminfo  string
	tempPath string

	applyMu sync.Mutex // serialisiert Apply/Close

	mu      sync.Mutex // schützt die folgenden Felder
	cur     Settings
	started bool
	closed  bool
	cancel  context.CancelFunc
	done    chan struct{}
	state   string
	detail  string
}

// NewService erzeugt einen Dienst ohne Display; Apply startet ihn.
func NewService(logf func(string, ...any)) *Service {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Service{logf: logf, open: openBus, interval: defaultInterval, retry: defaultRetry,
		procStat: "/proc/stat", meminfo: "/proc/meminfo", tempPath: "/sys/class/thermal/thermal_zone0/temp",
		state: "none", detail: "aus"}
}

// Status liefert "ok", "none" oder "error" samt Text.
func (s *Service) Status() (state, detail string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state, s.detail
}

// setStatus setzt den Status, solange der Lauf (ctx) nicht beendet ist.
func (s *Service) setStatus(ctx context.Context, state, detail string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx.Err() == nil {
		s.state, s.detail = state, detail
	}
}

// Apply stoppt das laufende Display (schreibt vorher Off) und startet das neue.
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
	if st.Unsupported != "" {
		s.state, s.detail = "error", st.Unsupported
		s.mu.Unlock()
		s.logf("statusdisplay: %s", st.Unsupported)
		return
	}
	if st.off() {
		s.state, s.detail = "none", "aus"
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.cancel, s.done = cancel, done
	s.state, s.detail = "ok", "Display startet"
	s.mu.Unlock()
	go s.loop(ctx, st, done)
}

// stop beendet den Lauf und wartet darauf.
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

// Close stoppt den Lauf (Off wird vorher geschrieben); danach ist Apply wirkungslos.
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
			err = errors.New("Display beendet")
		}
		s.logf("statusdisplay: %v", err)
		s.setStatus(ctx, "error", err.Error())
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.retry):
		}
	}
}

// runOnce öffnet das Display und zeigt jede Sekunde die Messwerte, bis ctx endet (dann
// Off) oder ein Fehler auftritt.
func (s *Service) runOnce(ctx context.Context, st Settings) error {
	b, err := s.open(st.Bus, st.Addr)
	if err != nil {
		return err
	}
	defer b.Close()
	d := &sh1106{b: b}
	if err := d.Init(); err != nil {
		return err
	}
	s.setStatus(ctx, "ok", fmt.Sprintf("%s 0x%02x", st.Bus, st.Addr))

	var prev cpuTimes
	var havePrev bool
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		var m Metrics
		if raw, err := os.ReadFile(s.procStat); err == nil {
			if cur, err := readCPU(string(raw)); err == nil {
				if havePrev { // die erste Messung zeigt 0 %
					m.CPU = cpuPercent(prev, cur)
				}
				prev, havePrev = cur, true
			}
		}
		if raw, err := os.ReadFile(s.meminfo); err == nil {
			m.RAM, _ = readRAM(string(raw))
		}
		m.TempC, m.TempOK = readTemp(s.tempPath)
		m.IP = ipAddress()
		if err := d.Show(Render(m)); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			if err := d.Off(); err != nil {
				s.logf("statusdisplay: Abschalten: %v", err)
			}
			return nil
		case <-t.C:
		}
	}
}
