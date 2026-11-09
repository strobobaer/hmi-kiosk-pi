package session

import (
	"context"
	"fmt"
	"strings"
	"time"

	"hmikiosk/internal/config"
	"hmikiosk/internal/coord"
	"hmikiosk/internal/rfb"
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
	RemoteNotReady               // Wartung, Dauerprüfung, Sitzung startet oder endet gerade, HMI nicht erreichbar
	RemoteInvalid                // Ziel ist das Terminal selbst oder die VNC-Anmeldung scheitert
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
	remoteClosedNotice    = "Von PDH beendet"
	remoteAuthMessage     = "VNC-Anmeldung am HMI fehlgeschlagen: Das von PDH übergebene Passwort wurde abgelehnt."
	remoteSelfMessage     = "Das Ziel ist dieses Terminal selbst."
	// remoteResolveTimeout begrenzt die Namensauflösung für den Kachelabgleich insgesamt.
	remoteResolveTimeout = time.Second
)

// remoteInfo hält die Angaben eines PDH-Befehls am Versuch. Das Passwort lebt nur im
// Speicher dieses Versuchs.
type remoteInfo struct {
	label, user string
	viewOnly    bool
	password    string
	reply       chan RemoteResult // bis zur Antwort, danach nil
	owner       chan RemoteResult // Kanal des Aufrufs; bleibt, damit ein Abbruch den Versuch findet
}

func (r *remoteInfo) display() string { return r.label + " – von PDH: " + r.user }

// OpenRemote öffnet eine Sitzung im Auftrag von PDH. Der Aufruf kehrt nach der Abstimmung
// (bei passender Kachel) und der RFB-Prüfung zurück; erst dann startet der Client. Endet ctx
// vorher (PDH hat aufgegeben), wird der Versuch beendet: Ohne Antwort entsteht keine Sitzung.
func (m *Manager) OpenRemote(ctx context.Context, r RemoteRequest) (RemoteResult, error) {
	matchID, self := m.resolveMatch(ctx, r)
	if self {
		return RemoteResult{Outcome: RemoteInvalid, Message: remoteSelfMessage}, nil
	}
	reply := make(chan RemoteResult, 1)
	if err := m.do(func() error {
		m.openRemote(r, matchID, reply)
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
		m.abandonRemote(reply)
		return RemoteResult{}, ctx.Err()
	}
}

// resolveMatch läuft außerhalb der Zustands-Goroutine. Es sucht eine VNC-Kachel mit gleichem
// Port, deren Host dieselbe Adresse ergibt wie der des Befehls (andere Schreibweise, Name statt
// IP); nicht auflösbar heißt keine Übereinstimmung. self meldet ein Ziel auf diesem Terminal.
func (m *Manager) resolveMatch(ctx context.Context, r RemoteRequest) (id string, self bool) {
	if m.d.Resolve == nil {
		return "", false
	}
	ctx, cancel := context.WithTimeout(ctx, remoteResolveTimeout)
	defer cancel()
	want, err := m.d.Resolve(ctx, r.Host)
	if err != nil || len(want) == 0 {
		return "", false
	}
	for _, ip := range want {
		if ip.IsLoopback() || ip.IsUnspecified() {
			return "", true
		}
	}
	for _, h := range m.d.Config().HMIs {
		if h.IsWeb() || h.Port != r.Port {
			continue
		}
		if strings.EqualFold(h.Host, r.Host) {
			return h.ID, false
		}
		got, err := m.d.Resolve(ctx, h.Host)
		if err != nil {
			continue
		}
		for _, a := range got {
			for _, b := range want {
				if a.Equal(b) {
					return h.ID, false
				}
			}
		}
	}
	return "", false
}

func (m *Manager) openRemote(r RemoteRequest, matchID string, reply chan RemoteResult) {
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
	cfg := m.d.Config()
	h, tile := matchTile(cfg, r.Host, r.Port)
	if !tile && matchID != "" {
		// Die Konfiguration kann sich seit der Auflösung geändert haben.
		if t, ok := findHMI(cfg, matchID); ok && !t.IsWeb() && t.Port == r.Port {
			h, tile = t, true
		}
	}
	if !tile {
		h = config.HMI{ID: RemoteAdHocID, Name: r.Label, Host: r.Host, Port: r.Port}
	}
	a := &attempt{
		hmi: h, adHoc: !tile, since: m.d.Clock.Now(),
		remote: &remoteInfo{label: r.Label, user: r.User, viewOnly: r.ViewOnly, password: r.Password, reply: reply, owner: reply},
	}
	a.ctx, a.cancel = context.WithCancel(m.runCtx)
	m.cur = a
	m.state = StateRequesting
	m.hint, m.badKey = nil, nil
	m.notice = ""
	if a.adHoc {
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
	m.precheck(a)
}

// onRemoteProbe beantwortet den Befehl nach der RFB-Prüfung. Scheitert sie, startet kein
// Client und PDH erfährt den Grund; ein abgelehntes Passwort markiert keine Kachel (es kam
// von PDH). Es gibt so auch keine wiederholten Fehlversuche, die der VNC-Server sperrt.
func (m *Manager) onRemoteProbe(a *attempt, r rfb.ProbeResult) {
	fail := func(outcome RemoteOutcome, msg string) {
		m.answer(a, RemoteResult{Outcome: outcome, Message: msg})
		m.end(a, "", false)
	}
	switch {
	case !r.Reachable:
		msg := fmt.Sprintf("„%s“ nicht erreichbar", a.hmi.Name)
		if r.Err != "" {
			msg += ": " + r.Err
		}
		fail(RemoteNotReady, msg)
	case r.Locked:
		fail(RemoteNotReady, lockoutNotice(r.Err))
	case r.AuthRejected:
		fail(RemoteInvalid, remoteAuthMessage)
	case !r.AuthOK:
		msg := r.Err
		if msg == "" {
			msg = fmt.Sprintf("VNC-Prüfung von „%s“ fehlgeschlagen", a.hmi.Name)
		}
		fail(RemoteNotReady, msg)
	default:
		m.answer(a, RemoteResult{Outcome: RemoteOpened})
		m.startClient(a)
	}
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

// abandonRemote beendet den Versuch zum Aufruf mit reply, dessen Aufrufer nicht mehr wartet.
func (m *Manager) abandonRemote(reply chan RemoteResult) {
	_ = m.do(func() error {
		if a := m.cur; a != nil && a.remote != nil && a.remote.owner == reply {
			a.remote.reply = nil
			m.stopRemote(a, "")
		}
		return nil
	})
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
		m.stopRemote(a, remoteClosedNotice)
		return nil
	})
	return closed
}

func (m *Manager) stopRemote(a *attempt, notice string) {
	a.operator = true
	switch m.state {
	case StateRequesting, StateConfirming:
		m.end(a, notice, true)
	case StateStopping:
	default:
		m.stopClient(a, notice)
	}
}
