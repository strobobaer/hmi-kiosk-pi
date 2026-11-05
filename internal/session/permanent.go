package session

import (
	"fmt"
	"time"

	"hmikiosk/internal/config"
)

// permanentState führt die Daueranzeige über einzelne Versuche hinweg.
type permanentState struct {
	target         string
	targetName     string // letzter bekannter Name des Ziels
	halted         bool   // der Bediener hat „Zurück“ gedrückt
	missingNoticed bool   // Hinweis auf ein gelöschtes Ziel ist schon erfolgt
	backoff        time.Duration
	waitingID      string
	waitingName    string
}

// checkPermanent startet die Daueranzeige, sobald nichts anderes läuft. Ein
// gelöschtes Ziel erzeugt keine Versuche; es zählt wieder, wenn die Konfiguration
// es erneut enthält oder ein anderes nennt.
func (m *Manager) checkPermanent(cfg config.Config) {
	p := &m.perm
	target := cfg.Kiosk.PermanentHMI
	if target != p.target {
		*p = permanentState{target: target}
		if m.state == StateReconnecting {
			m.clearDeadline()
			m.state = StateTiles
			m.notice = ""
		}
	}
	h, exists := findHMI(cfg, target)
	if exists {
		p.targetName = h.Name
	}
	switch m.state {
	case StateReconnecting:
		if !exists {
			m.clearDeadline()
			m.state = StateTiles
			m.noticeMissing()
		}
	case StateTiles:
		switch {
		case target == "" || p.halted:
		case !exists:
			m.noticeMissing()
		default:
			p.missingNoticed = false
			m.begin(h, cfg, true)
		}
	}
}

func (m *Manager) noticeMissing() {
	if m.perm.missingNoticed {
		return
	}
	m.perm.missingNoticed = true
	m.notice = "Das HMI der Daueranzeige gibt es nicht mehr"
	if name := m.perm.targetName; name != "" {
		m.notice = fmt.Sprintf("Das HMI „%s“ der Daueranzeige gibt es nicht mehr", name)
	}
}

// afterPermanentEnd legt fest, wie es nach einem beendeten Versuch weitergeht.
// Ergebnis true: Das Neuverbinden ist geplant, sonst geht es zu den Kacheln.
func (m *Manager) afterPermanentEnd(a *attempt, notice string, operator bool, now time.Time) bool {
	p := &m.perm
	if !a.permanent {
		return false
	}
	if operator {
		p.halted = true
		p.backoff = 0
		return false
	}
	cfg := m.d.Config()
	if _, ok := findHMI(cfg, a.hmi.ID); !ok {
		p.missingNoticed = true
		return false
	}
	if cfg.Kiosk.PermanentHMI != a.hmi.ID {
		return false
	}
	if heldLongEnough(a, now) {
		p.backoff = 0
	}
	m.scheduleReconnect(a.hmi, notice)
	return true
}

func heldLongEnough(a *attempt, now time.Time) bool {
	if a.connectedAt.IsZero() {
		return false
	}
	until := now
	if !a.lostSince.IsZero() {
		until = a.lostSince
	}
	return until.Sub(a.connectedAt) >= ReconnectResetAfter
}

func (m *Manager) scheduleReconnect(h config.HMI, reason string) {
	p := &m.perm
	wait := max(p.backoff, ReconnectMin)
	p.backoff = min(2*wait, ReconnectMax)
	p.waitingID, p.waitingName = h.ID, h.Name
	m.state = StateReconnecting
	m.notice = fmt.Sprintf("Neuer Versuch in %d s", int(wait/time.Second))
	if reason != "" {
		m.notice = reason + " – " + m.notice
	}
	id := h.ID
	m.setDeadline(m.d.Clock.Now().Add(wait), func() { m.retryPermanent(id) })
}

func (m *Manager) retryPermanent(id string) {
	cfg := m.d.Config()
	h, ok := findHMI(cfg, id)
	if !ok {
		m.state = StateTiles
		m.noticeMissing()
		return
	}
	m.begin(h, cfg, true)
}
