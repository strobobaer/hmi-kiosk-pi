package session

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"hmikiosk/internal/client"
	"hmikiosk/internal/config"
	"hmikiosk/internal/coord"
	"hmikiosk/internal/rfb"
)

// ErrStopped meldet einen Aufruf, nachdem Run geendet hat.
var ErrStopped = errors.New("Sitzungsverwaltung ist beendet")

// Coordinator ist der von session genutzte Teil von coord.Coordinator.
type Coordinator interface {
	Request(ctx context.Context, hmi string) coord.Result
	Takeover(ctx context.Context, hmi string, d coord.Decision) error
	Hold(hmi, session string)
	Release(hmi string)
	Events() <-chan coord.Event
}

// Deps sind die Abhängigkeiten des Managers. TCP meldet eine ESTABLISHED-
// Verbindung zum HMI (platform.HasEstablished), nicht die Erreichbarkeit.
type Deps struct {
	Coord    Coordinator
	Runner   client.Runner
	Profile  func() client.Profile
	TCP      func(host string, port int) bool
	RAMDir   func() (dir string, cleanup func(), err error)
	Clock    Clock
	Config   func() config.Config
	Password func(config.HMI) (string, error)
	Screen   func() (w, h int)
	Probe    func(ctx context.Context, h config.HMI, password string) rfb.ProbeResult
	// OnEnd meldet jedes Ende einer Sitzung mit gestartetem Client; nil heißt niemand.
	// Läuft in der Zustands-Goroutine und darf nicht blockieren.
	OnEnd func(Ended)
	// Blocked liefert eine Meldung, solange kein Sitzungsstart erlaubt ist (Dauerprüfung); nil heißt immer erlaubt.
	Blocked func() string
}

// attempt ist ein Sitzungsversuch vom Touch bis zum Aufräumen. Ein laufender
// Hintergrundschritt erkennt am Vergleich mit Manager.cur, ob er noch zählt.
type attempt struct {
	hmi       config.HMI
	password  string
	checked   bool // Passwort ist gelesen, a.password also gültig
	permanent bool
	auto      bool // ohne Touch gestartet, es kann also niemand rückfragen
	confirmed bool // Rückfrage ist bejaht, nicht erneut fragen
	watch     bool
	decision  coord.Decision

	ctx    context.Context
	cancel context.CancelFunc

	proc         client.Process
	exited       chan struct{}
	cleanup      func()
	startedAt    time.Time
	connectedAt  time.Time
	lostSince    time.Time
	lastActivity time.Time

	takenNotice string
	endNotice   string
	operator    bool
}

// Manager: Öffentliche Methoden schicken nur Befehle an die Goroutine von Run.
type Manager struct {
	d       Deps
	cmds    chan func()
	stopped chan struct{}
	touched atomic.Bool

	// Ab hier gehört alles der Goroutine von Run.
	runCtx   context.Context
	state    State
	notice   string
	hint     []string
	badKey   []string
	cur      *attempt
	sessionN int
	timers   timers
	perm     permanentState

	mu   sync.Mutex
	snap Snapshot
	subs map[chan Snapshot]struct{}
}

func New(d Deps) *Manager {
	return &Manager{
		d:       d,
		cmds:    make(chan func()),
		stopped: make(chan struct{}),
		state:   StateTiles,
		snap:    Snapshot{State: StateTiles},
		subs:    map[chan Snapshot]struct{}{},
	}
}

// Run führt die Zustandsmaschine aus, bis ctx endet; ein laufender Client wird
// dann noch beendet. Die öffentlichen Methoden warten, bis Run läuft.
func (m *Manager) Run(ctx context.Context) {
	m.runCtx = ctx
	m.timers.tickAt = m.d.Clock.Now().Add(PollEvery)
	m.checkPermanent(m.d.Config())
	m.publish()
	events := m.d.Coord.Events()
	for {
		m.armWake()
		select {
		case <-ctx.Done():
			m.shutdown()
			return
		case f := <-m.cmds:
			f()
		case ev := <-events:
			m.onTakeoverEvent(ev)
		case <-m.timers.wake:
			m.onWake()
		}
		m.publish()
	}
}

func (m *Manager) shutdown() {
	close(m.stopped)
	a := m.cur
	if a == nil {
		return
	}
	a.cancel()
	if a.proc != nil {
		if m.state != StateStopping {
			_ = a.proc.Stop(StopGrace)
		}
		<-a.exited
	}
	m.cleanupRAM(a)
	m.d.Coord.Release(a.hmi.ID)
	m.reportEnd(a, "Dienst beendet", false)
	m.cur = nil
	m.state = StateTiles
	m.publish()
}

// post stellt f der Zustands-Goroutine zu; aus ihr selbst nie aufrufen.
func (m *Manager) post(f func()) bool {
	select {
	case m.cmds <- f:
		return true
	case <-m.stopped:
		return false
	}
}

func (m *Manager) do(f func() error) error {
	var err error
	done := make(chan struct{})
	if !m.post(func() {
		defer close(done)
		err = f()
		m.publish()
	}) {
		return ErrStopped
	}
	<-done
	return err
}

// Open beginnt eine Sitzung. Läuft schon eine, bleibt der Aufruf wirkungslos;
// ein doppelter Touch ergibt so nie einen zweiten Client.
func (m *Manager) Open(hmiID string) error {
	return m.do(func() error {
		if m.state != StateTiles {
			return nil
		}
		if m.d.Blocked != nil {
			if msg := m.d.Blocked(); msg != "" {
				m.notice = msg
				return errors.New(msg)
			}
		}
		cfg := m.d.Config()
		h, ok := findHMI(cfg, hmiID)
		if !ok {
			m.notice = "Dieses HMI gibt es nicht mehr"
			return fmt.Errorf("HMI %q ist nicht bekannt", hmiID)
		}
		m.begin(h, cfg, false)
		return nil
	})
}

// Confirm bestätigt die Rückfrage „Wird genutzt von X – übernehmen?“.
func (m *Manager) Confirm() error {
	return m.do(func() error {
		if m.state != StateConfirming {
			return errors.New("Es steht keine Rückfrage an")
		}
		// Die Antwort kann beliebig alt sein, der Halter längst ein anderer.
		a := m.cur
		a.confirmed = true
		m.state = StateRequesting
		m.hint, m.badKey = nil, nil
		m.request(a)
		return nil
	})
}

// Cancel verneint die Rückfrage; in anderen Zuständen ohne Wirkung.
func (m *Manager) Cancel() {
	_ = m.do(func() error {
		if m.state == StateConfirming {
			m.end(m.cur, "", true)
		}
		return nil
	})
}

// Back ist das „Zurück“ des Bedieners.
func (m *Manager) Back() {
	_ = m.do(func() error {
		m.onBack()
		return nil
	})
}

// Ping kehrt zurück, sobald die Zustands-Goroutine einen Befehl ausgeführt hat.
// Snapshot taugt dafür nicht: Es antwortet auch, wenn sie hängt.
func (m *Manager) Ping(ctx context.Context) error {
	done := make(chan struct{})
	select {
	case m.cmds <- func() { close(done) }:
	case <-m.stopped:
		return ErrStopped
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// TouchActivity wird bei jeder Touch-Eingabe gerufen und blockiert nie. Die
// nächste Abfrage wertet sie aus, das reicht für eine Frist von Minuten.
func (m *Manager) TouchActivity() { m.touched.Store(true) }

// Snapshot liefert den zuletzt veröffentlichten Stand.
func (m *Manager) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snap.clone()
}

// Subscribe liefert sofort den aktuellen und danach jeweils den neuesten Stand.
// Wer langsam liest, überspringt Zwischenstände. Die Funktion beendet das Abo.
func (m *Manager) Subscribe() (<-chan Snapshot, func()) {
	ch := make(chan Snapshot, 1)
	m.mu.Lock()
	defer m.mu.Unlock()
	ch <- m.snap.clone()
	m.subs[ch] = struct{}{}
	return ch, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if _, ok := m.subs[ch]; ok {
			delete(m.subs, ch)
			close(ch)
		}
	}
}

func (m *Manager) publish() {
	s := m.buildSnapshot()
	m.mu.Lock()
	defer m.mu.Unlock()
	if reflect.DeepEqual(s, m.snap) {
		return
	}
	m.snap = s
	for ch := range m.subs {
		// Gesendet wird nur hier unter mu, nach dem Leeren ist also Platz.
		select {
		case <-ch:
		default:
		}
		ch <- s.clone()
	}
}

func (m *Manager) buildSnapshot() Snapshot {
	s := Snapshot{State: m.state, Notice: m.notice}
	s.Unreachable = append(s.Unreachable, m.hint...)
	s.BadKey = append(s.BadKey, m.badKey...)
	switch {
	case m.cur != nil:
		s.HMI, s.HMIName = m.cur.hmi.ID, m.cur.hmi.Name
		s.ViewOnly = m.cur.hmi.ViewOnly || m.cur.watch
		if m.state == StateConfirming && m.cur.decision.Holder != nil {
			h := *m.cur.decision.Holder
			s.Confirm = &h
		}
	case m.state == StateReconnecting:
		s.HMI, s.HMIName = m.perm.waitingID, m.perm.waitingName
	}
	return s
}

func findHMI(cfg config.Config, id string) (config.HMI, bool) {
	for _, h := range cfg.HMIs {
		if h.ID == id {
			return h, true
		}
	}
	return config.HMI{}, false
}

// ClearNotice entfernt den Hinweis, wenn er genau msg ist; ein anderer bleibt.
func (m *Manager) ClearNotice(msg string) {
	_ = m.do(func() error {
		if m.notice == msg {
			m.notice = ""
		}
		return nil
	})
}
