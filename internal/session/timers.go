package session

import (
	"fmt"
	"time"

	"hmikiosk/internal/config"
	"hmikiosk/internal/coord"
)

// Clock ergänzt coord.Clock um den Wecker der Zustands-Goroutine.
type Clock interface {
	coord.Clock
	After(d time.Duration) <-chan time.Time
}

// SystemClock ist die echte Uhr.
type SystemClock struct{ coord.SystemClock }

func (SystemClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// timers hält genau einen Wecker: die regelmäßige Abfrage oder die Frist des
// aktuellen Zustands, je nachdem, was früher kommt. Vorwarnung, Wartezeit nach
// Übernahme und Neuverbinden schließen sich gegenseitig aus.
type timers struct {
	tickAt     time.Time
	deadline   time.Time
	deadlineFn func()
	wake       <-chan time.Time
	wakeAt     time.Time
}

func (m *Manager) setDeadline(at time.Time, fn func()) {
	m.timers.deadline, m.timers.deadlineFn = at, fn
}

func (m *Manager) clearDeadline() {
	m.timers.deadline, m.timers.deadlineFn = time.Time{}, nil
}

func (m *Manager) armWake() {
	t := &m.timers
	next := t.tickAt
	if t.deadlineFn != nil && t.deadline.Before(next) {
		next = t.deadline
	}
	if t.wake == nil || !next.Equal(t.wakeAt) {
		t.wake = m.d.Clock.After(next.Sub(m.d.Clock.Now()))
		t.wakeAt = next
	}
}

func (m *Manager) onWake() {
	t := &m.timers
	t.wake = nil
	now := m.d.Clock.Now()
	if t.deadlineFn != nil && !now.Before(t.deadline) {
		fn := t.deadlineFn
		m.clearDeadline()
		fn()
	}
	if !now.Before(t.tickAt) {
		t.tickAt = now.Add(PollEvery)
		m.tick(now)
	}
}

func (m *Manager) tick(now time.Time) {
	touched := m.touched.Swap(false)
	cfg := m.d.Config()
	m.checkConfig(cfg)
	m.checkPermanent(cfg)
	a := m.cur
	if a == nil {
		return
	}
	if touched {
		a.lastActivity = now
	}
	switch m.state {
	case StateStarting:
		m.pollStarting(a, now)
	case StateConnected:
		m.pollConnected(a, now, cfg)
	}
}

func (m *Manager) pollStarting(a *attempt, now time.Time) {
	if m.d.TCP(a.hmi.Host, a.hmi.Port) {
		m.state = StateConnected
		m.notice = ""
		a.connectedAt, a.lastActivity = now, now
		return
	}
	if now.Sub(a.startedAt) >= StartTimeout {
		m.stopClient(a, fmt.Sprintf("Keine Verbindung zu „%s“ zustande gekommen", a.hmi.Name))
	}
}

func (m *Manager) pollConnected(a *attempt, now time.Time, cfg config.Config) {
	if m.d.TCP(a.hmi.Host, a.hmi.Port) {
		a.lostSince = time.Time{}
	} else {
		if a.lostSince.IsZero() {
			a.lostSince = now
		}
		if now.Sub(a.lostSince) >= LostAfter {
			m.stopClient(a, fmt.Sprintf("Verbindung zu „%s“ verloren", a.hmi.Name))
			return
		}
	}
	limit := time.Duration(cfg.Kiosk.InactivityMinutes) * time.Minute
	if limit > 0 && !a.permanent && now.Sub(a.lastActivity) >= limit {
		m.stopClient(a, "Wegen Inaktivität zu den Kacheln zurückgekehrt")
	}
}
