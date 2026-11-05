package coord

import (
	"time"

	"hmikiosk/internal/config"
)

// TakeoverWarn ist die Vorwarnzeit, bevor das übernommene Terminal getrennt wird.
const TakeoverWarn = 5 * time.Second

// Holder beschreibt ein Terminal, das ein HMI belegt.
type Holder struct {
	Terminal, Name string
	Role           config.Role
	Session        string
	Permanent      bool // Daueranzeige
	Watch          bool // Mitschauen (ViewOnly) belegt nicht
}

type Outcome int

const (
	Allow Outcome = iota
	AllowWatch
	AskConfirm
	TakeOver
	Reject
)

type Decision struct {
	Outcome Outcome
	Warn    time.Duration
	Holder  *Holder // der berücksichtigte Halter, nil wenn frei
}

type Rules struct {
	Mode       config.Mode
	Style      config.TakeoverStyle
	AllowWatch bool
}

// Decide wendet die Regeln aus Spec Abschnitt 5 an. Reine Funktion.
func Decide(requesterRole config.Role, h *Holder, r Rules) Decision {
	if h == nil {
		return Decision{Outcome: Allow}
	}
	d := Decision{Outcome: Reject, Holder: h}
	if h.Watch {
		d.Outcome = Allow
		return d
	}
	warn := TakeoverWarn
	if r.Style == config.StyleImmediate {
		warn = 0
	}
	takeOver := func() Decision {
		d.Outcome, d.Warn = TakeOver, warn
		return d
	}
	reqPrimary := requesterRole == config.RolePrimary

	// Mitschauen gibt es nur bei Daueranzeige und nur für normale Anfragende.
	if h.Permanent && !reqPrimary {
		if r.AllowWatch {
			d.Outcome = AllowWatch
		}
		return d
	}
	switch {
	case h.Role == config.RolePrimary && !reqPrimary:
		return d
	case h.Role == config.RolePrimary:
		if r.Mode == config.ModeTakeover {
			return takeOver()
		}
	case reqPrimary:
		return takeOver()
	case r.Mode == config.ModeTakeover:
		if r.Style == config.StyleImmediate {
			return takeOver()
		}
		d.Outcome, d.Warn = AskConfirm, warn
	}
	return d
}

// Claim ist eine gleichzeitige Anfrage für dasselbe HMI.
type Claim struct {
	Terminal string
	Role     config.Role
	Nonce    string
}

// ResolveSimultaneous: höhere Rolle gewinnt, bei gleicher Rolle die kleinere
// Kennung. Unabhängig von der Reihenfolge der Argumente.
func ResolveSimultaneous(a, b Claim) Claim {
	if a.Role != b.Role {
		if a.Role == config.RolePrimary {
			return a
		}
		if b.Role == config.RolePrimary {
			return b
		}
	}
	if b.Terminal < a.Terminal {
		return b
	}
	return a
}
