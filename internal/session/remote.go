package session

import (
	"context"
	"strings"
	"time"

	"hmikiosk/internal/config"
	"hmikiosk/internal/coord"
)

// RemoteRequest ist der Fernbefehl „open“ von PDH (Eingaben schon geprüft).
type RemoteRequest struct {
	Host     string
	Port     int
	Password string
	ViewOnly bool
	Label    string
	User     string
}

type RemoteOutcome int

const (
	RemoteOpened   RemoteOutcome = iota
	RemoteBusy                   // eine Sitzung ist offen oder ein anderes Terminal hält das HMI
	RemoteNotReady               // Wartungsmodus, Dauerprüfung, Sitzung startet oder endet gerade
)

// RemoteResult ist die Antwort auf OpenRemote. HMI und Since beschreiben die Belegung
// (Since ist Null, wenn der Beginn unbekannt ist, etwa bei einem anderen Terminal).
type RemoteResult struct {
	Outcome RemoteOutcome
	HMI     string
	Since   time.Time
	Message string
}

// RemoteAdHocID ist die Kennung einer nur lokalen PDH-Sitzung ohne passende Kachel. Der
// Doppelpunkt ist in Kennungen der Konfiguration nicht erlaubt; es gibt also keine Verwechslung.
const RemoteAdHocID = "pdh:"

const (
	remoteStartingMessage = "Am Terminal startet oder endet gerade eine Sitzung. Bitte gleich noch einmal versuchen."
	remoteAuthNotice      = "Das von PDH übergebene VNC-Passwort wurde abgelehnt."
	remoteClosedNotice    = "Von PDH beendet"
)

// remoteInfo hält die Angaben eines PDH-Befehls am Versuch. Das Passwort lebt nur im
// Speicher dieses Versuchs.
type remoteInfo struct {
	label, user string
	viewOnly    bool
	password    string
	reply       chan RemoteResult // bis zur Antwort, danach nil
}

func (r *remoteInfo) display() string { return r.label + " – von PDH: " + r.user }

// OpenRemote öffnet eine Sitzung im Auftrag von PDH. Bei einer Kachel mit gleichem Host
// und Port kehrt der Aufruf nach der Abstimmung mit den anderen Terminals zurück, sonst
// sofort; der Client startet danach asynchron.
func (m *Manager) OpenRemote(ctx context.Context, r RemoteRequest) (RemoteResult, error) {
	reply := make(chan RemoteResult, 1)
	if err := m.do(func() error {
		m.openRemote(r, reply)
		return nil
	}); err != nil {
		return RemoteResult{}, err
	}
	select {
	case res := <-reply:
		return res, nil
	case <-m.stopped:
		return RemoteResult{}, ErrStopped
	case <-ctx.Done():
		return RemoteResult{}, ctx.Err()
	}
}

func (m *Manager) openRemote(r RemoteRequest, reply chan RemoteResult) {
	switch m.state {
	case StateTiles:
	case StateRequesting, StateConfirming, StateStopping:
		reply <- RemoteResult{Outcome: RemoteNotReady, Message: remoteStartingMessage}
		return
	default:
		reply <- m.busyResult()
		return
	}
	if m.d.Blocked != nil {
		if msg := m.d.Blocked(); msg != "" {
			reply <- RemoteResult{Outcome: RemoteNotReady, Message: msg}
			return
		}
	}
	h, tile := matchTile(m.d.Config(), r.Host, r.Port)
	if !tile {
		h = config.HMI{ID: RemoteAdHocID, Name: r.Label, Host: r.Host, Port: r.Port}
	}
	a := &attempt{
		hmi: h, adHoc: !tile, since: m.d.Clock.Now(),
		remote: &remoteInfo{label: r.Label, user: r.User, viewOnly: r.ViewOnly, password: r.Password, reply: reply},
	}
	a.ctx, a.cancel = context.WithCancel(m.runCtx)
	m.cur = a
	m.state = StateRequesting
	m.hint, m.badKey = nil, nil
	m.notice = ""
	if a.adHoc {
		m.answer(a, RemoteResult{Outcome: RemoteOpened})
		m.precheck(a)
		return
	}
	m.request(a)
}

// matchTile sucht eine VNC-Kachel mit gleichem Host (ohne Groß-/Kleinschreibung) und Port.
func matchTile(cfg config.Config, host string, port int) (config.HMI, bool) {
	for _, h := range cfg.HMIs {
		if !h.IsWeb() && h.Port == port && strings.EqualFold(h.Host, host) {
			return h, true
		}
	}
	return config.HMI{}, false
}

// onRemoteRequest wertet die Abstimmung für einen PDH-Befehl aus. Niemand am Terminal kann
// rückfragen, und PDH verdrängt niemanden: Jeder Halter außer einem Mitschauenden heißt belegt.
func (m *Manager) onRemoteRequest(a *attempt, res coord.Result) {
	d := res.Decision
	ok := !res.Rejected && (d.Outcome == coord.Allow || d.Outcome == coord.AllowWatch && a.remote.viewOnly)
	if !ok {
		name := a.hmi.Name
		if d.Holder != nil {
			name += " (an " + holderName(d.Holder) + ")"
		}
		m.answer(a, RemoteResult{Outcome: RemoteBusy, HMI: name})
		m.end(a, "", false)
		return
	}
	a.watch = d.Outcome == coord.AllowWatch
	m.answer(a, RemoteResult{Outcome: RemoteOpened})
	m.precheck(a)
}

// answer beantwortet den wartenden Aufruf von OpenRemote genau einmal.
func (m *Manager) answer(a *attempt, res RemoteResult) {
	if a.remote == nil || a.remote.reply == nil {
		return
	}
	a.remote.reply <- res
	a.remote.reply = nil
}

func (m *Manager) busyResult() RemoteResult {
	s := m.buildSnapshot()
	name := s.HMIName
	if s.Remote {
		name = s.RemoteLabel
	}
	return RemoteResult{Outcome: RemoteBusy, HMI: name, Since: s.Since}
}

// CloseRemote beendet eine von PDH geöffnete Sitzung; false heißt: Es läuft keine.
func (m *Manager) CloseRemote() bool {
	closed := false
	_ = m.do(func() error {
		a := m.cur
		if a == nil || a.remote == nil {
			return nil
		}
		closed = true
		a.operator = true
		switch m.state {
		case StateRequesting, StateConfirming:
			m.end(a, remoteClosedNotice, true)
		case StateStopping:
		default:
			m.stopClient(a, remoteClosedNotice)
		}
		return nil
	})
	return closed
}
