package session

import (
	"fmt"
	"time"

	"hmikiosk/internal/config"
	"hmikiosk/internal/rfb"
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
	waitingWeb     bool
	waitingSince   time.Time

	// Nach abgelehntem Passwort bleibt die Daueranzeige stehen, bis die Konfiguration
	// ein anderes Passwort oder eine aufgehobene Markierung zeigt (das Speichern der
	// Markierung läuft nebenher).
	authHalt    bool
	authEnc     string
	authSeen    bool
	authNoticed bool
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
		case m.passwordHalted(h):
			if !p.authNoticed {
				p.authNoticed = true
				m.notice = authFailedNotice
			}
		default:
			p.missingNoticed = false
			if m.blocked() {
				return
			}
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
	// Gesperrt (Wartungsmodus, Dauerprüfung): zu den Kacheln statt neu verbinden.
	if m.blocked() {
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
	if a.authRejected {
		return false
	}
	if a.locked {
		m.scheduleWait(a.hmi, notice, LockoutPause)
		return true
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
	m.scheduleWait(h, reason, wait)
}

// scheduleWait plant den nächsten Versuch der Daueranzeige nach wait.
func (m *Manager) scheduleWait(h config.HMI, reason string, wait time.Duration) {
	p := &m.perm
	p.waitingID, p.waitingName, p.waitingWeb, p.waitingSince = h.ID, h.Name, h.IsWeb(), m.d.Clock.Now()
	m.state = StateReconnecting
	m.notice = "Neuer Versuch in " + waitText(wait)
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
	if h.AuthFailed {
		m.state = StateTiles
		m.notice = authFailedNotice
		return
	}
	// Gesperrt (Dauerprüfung): weiter warten, ohne die Wartezeit wachsen zu lassen.
	if m.blocked() {
		m.setDeadline(m.d.Clock.Now().Add(PollEvery), func() { m.retryPermanent(id) })
		return
	}
	m.begin(h, cfg, true)
}

// blocked meldet, ob gerade kein Sitzungsstart erlaubt ist (Deps.Blocked).
func (m *Manager) blocked() bool {
	return m.d.Blocked != nil && m.d.Blocked() != ""
}

// LockoutPause ist die Wartezeit der Daueranzeige, wenn der VNC-Server dieses
// Terminal wegen zu vieler Fehlversuche sperrt.
const LockoutPause = 5 * time.Minute

func waitText(d time.Duration) string {
	if d >= 2*time.Minute {
		return fmt.Sprintf("%d min", int(d/time.Minute))
	}
	return fmt.Sprintf("%d s", int(d/time.Second))
}

func (p *permanentState) haltForPassword(h config.HMI) {
	if p.target == h.ID {
		p.authHalt, p.authEnc, p.authSeen, p.authNoticed = true, h.PasswordEnc, false, true
	}
}

// passwordHalted meldet, ob die Daueranzeige wegen eines abgelehnten Passworts ruht.
func (m *Manager) passwordHalted(h config.HMI) bool {
	p := &m.perm
	if p.authHalt {
		switch {
		case h.AuthFailed:
			p.authSeen = true
		case p.authSeen || h.PasswordEnc != p.authEnc:
			p.authHalt = false
		}
	}
	halted := h.AuthFailed || p.authHalt
	if !halted {
		p.authNoticed = false
	}
	return halted
}

func lockoutNotice(reason string) string {
	if reason == "" {
		return rfb.LockoutHint
	}
	return rfb.LockoutHint + " " + reason
}
