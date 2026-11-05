// Package session führt den Sitzungsablauf eines Terminals: Eine Goroutine
// besitzt den gesamten Zustand und steuert Abstimmung und VNC-Client.
package session

import (
	"slices"
	"time"

	"hmikiosk/internal/coord"
)

type State string

const (
	StateTiles        State = "tiles"
	StateRequesting   State = "requesting" // Anfrage, Vorabprüfung, Wartezeit nach eigener Übernahme
	StateConfirming   State = "confirming"
	StateWarned       State = "warned" // ein anderes Terminal übernimmt, Client läuft noch
	StateStarting     State = "starting"
	StateConnected    State = "connected"
	StateStopping     State = "stopping"
	StateReconnecting State = "reconnecting"
)

const (
	// StartTimeout begrenzt den Start ohne ESTABLISHED: Remmina zeigt bei
	// falschem Passwort einen Dialog ohne Verbindung und endet nie von selbst.
	StartTimeout = 15 * time.Second
	LostAfter    = 5 * time.Second
	StopGrace    = 3 * time.Second
	PollEvery    = 500 * time.Millisecond

	ReconnectMin        = 2 * time.Second
	ReconnectMax        = 30 * time.Second
	ReconnectResetAfter = 60 * time.Second

	// probeTimeout bleibt unter der Frist, in der eine Anfrage bei den anderen
	// Terminals als laufend gilt.
	probeTimeout = 5 * time.Second
)

// Snapshot ist der Stand für die Oberfläche. Unreachable nennt Terminals, die
// nicht antworten, BadKey solche, die den gemeinsamen Schlüssel ablehnen. Beides
// ist nur ein Hinweis.
type Snapshot struct {
	State        State
	HMI, HMIName string
	ViewOnly     bool // nur ansehen: Einstellung des Terminals oder Mitschauen
	Notice       string
	Confirm      *coord.Holder
	Unreachable  []string
	BadKey       []string
}

func (s Snapshot) clone() Snapshot {
	s.Unreachable = slices.Clone(s.Unreachable)
	s.BadKey = slices.Clone(s.BadKey)
	if s.Confirm != nil {
		c := *s.Confirm
		s.Confirm = &c
	}
	return s
}
