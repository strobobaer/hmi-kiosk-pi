package session

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"strconv"
	"time"

	"hmikiosk/internal/client"
	"hmikiosk/internal/config"
	"hmikiosk/internal/coord"
	"hmikiosk/internal/rfb"
)

// Ablauf: begin → onRequest → (Rückfrage) → precheck → onProbe → (Übernahme,
// Vorwarnung) → startClient → Überwachung → stopClient → onExit → end.
// Blockierende Schritte laufen in eigenen Goroutinen und melden sich per post.

func (m *Manager) begin(h config.HMI, cfg config.Config, auto bool) {
	a := &attempt{hmi: h, auto: auto, permanent: cfg.Kiosk.PermanentHMI == h.ID, web: h.IsWeb(), since: m.d.Clock.Now()}
	a.ctx, a.cancel = context.WithCancel(m.runCtx)
	if a.permanent {
		m.perm.halted = false
	}
	m.cur = a
	m.state = StateRequesting
	m.hint, m.badKey = nil, nil
	if !auto {
		m.notice = ""
	}
	if a.web {
		// Lokal und frei: keine Anfrage, keine Belegung, keine RFB-Prüfung.
		m.startWeb(a)
		return
	}
	m.request(a)
}

func (m *Manager) request(a *attempt) {
	go func() {
		res := m.d.Coord.Request(a.ctx, a.hmi.ID)
		m.post(func() { m.onRequest(a, res) })
	}()
}

func (m *Manager) onRequest(a *attempt, res coord.Result) {
	if m.cur != a {
		// Die überholte Anfrage kann noch offen stehen. Gilt der neue Versuch
		// demselben HMI, hat er sie ersetzt und darf nicht mitgelöscht werden.
		if m.cur == nil || m.cur.hmi.ID != a.hmi.ID {
			m.d.Coord.Release(a.hmi.ID)
		}
		return
	}
	addNames(&m.hint, res.Unreachable...)
	addNames(&m.badKey, res.BadKey...)
	a.decision = res.Decision
	if a.remote != nil {
		m.onRemoteRequest(a, res)
		return
	}
	d := res.Decision
	switch {
	case res.Rejected || d.Outcome == coord.Reject:
		m.end(a, busyNotice(d.Holder), false)
	case a.auto && (d.Outcome == coord.AskConfirm || displacesPrimary(d)):
		// Ohne Bediener wird weder ewig gewartet noch ein primäres Terminal verdrängt.
		m.end(a, busyNotice(d.Holder), false)
	case d.Outcome == coord.AskConfirm && !a.confirmed:
		m.state = StateConfirming
	default:
		a.watch = d.Outcome == coord.AllowWatch
		m.precheck(a)
	}
}

func displacesPrimary(d coord.Decision) bool {
	return d.Outcome == coord.TakeOver && d.Holder != nil && d.Holder.Role == config.RolePrimary
}

func busyNotice(h *coord.Holder) string {
	return "Belegt durch " + holderName(h)
}

func holderName(h *coord.Holder) string {
	if h == nil || h.Name == "" {
		return "ein anderes Terminal"
	}
	return h.Name
}

func addNames(list *[]string, names ...string) {
	for _, n := range names {
		if !slices.Contains(*list, n) {
			*list = append(*list, n)
		}
	}
}

// precheck läuft vor einer Übernahme: Ein falsches Passwort oder ein totes HMI
// soll niemanden vom HMI verdrängen.
func (m *Manager) precheck(a *attempt) {
	if a.remote != nil {
		m.probe(a, a.remote.password)
		return
	}
	pw, err := m.d.Password(a.hmi)
	if err != nil {
		log.Printf("Sitzung: Passwort von HMI %q nicht lesbar: %v", a.hmi.ID, err)
		if errors.Is(err, ErrPasswordMissing) {
			m.end(a, passwordMissingNotice, false)
			return
		}
		m.end(a, fmt.Sprintf("Passwort von „%s“ lässt sich nicht entschlüsseln", a.hmi.Name), false)
		return
	}
	m.probe(a, pw)
}

func (m *Manager) probe(a *attempt, pw string) {
	a.password, a.checked = pw, true
	h := a.hmi
	go func() {
		ctx, cancel := context.WithTimeout(a.ctx, probeTimeout)
		defer cancel()
		res := m.d.Probe(ctx, h, pw)
		m.post(func() { m.onProbe(a, res) })
	}()
}

func (m *Manager) onProbe(a *attempt, r rfb.ProbeResult) {
	if m.cur != a {
		return
	}
	if a.remote != nil {
		m.onRemoteProbe(a, r)
		return
	}
	switch {
	case !r.Reachable:
		m.end(a, fmt.Sprintf("„%s“ nicht erreichbar", a.hmi.Name), false)
	case r.Locked:
		a.locked = true
		m.end(a, lockoutNotice(r.Err), false)
	case r.AuthRejected:
		a.authRejected = true
		if m.d.MarkAuthFailed != nil {
			go m.d.MarkAuthFailed(a.hmi.ID, a.hmi.PasswordEnc)
		}
		m.perm.haltForPassword(a.hmi)
		m.end(a, authFailedNotice, false)
	case !r.AuthOK:
		notice := r.Err
		if notice == "" {
			notice = fmt.Sprintf("VNC-Prüfung von „%s“ fehlgeschlagen", a.hmi.Name)
		}
		m.end(a, notice, false)
	case a.decision.Outcome == coord.TakeOver || a.decision.Outcome == coord.AskConfirm:
		d := a.decision
		go func() {
			err := m.d.Coord.Takeover(a.ctx, a.hmi.ID, d)
			m.post(func() { m.onTakeoverSent(a, err) })
		}()
	default:
		m.startClient(a)
	}
}

func (m *Manager) onTakeoverSent(a *attempt, err error) {
	if m.cur != a {
		return
	}
	d := a.decision
	// Wer die Aufforderung nicht bekam, wird auch nicht gewarnt: Warten nützt
	// dann nichts, und die Verbindung ist laut Spec trotzdem erlaubt.
	if err != nil {
		log.Printf("Sitzung: Übernahme von HMI %q: %v", a.hmi.ID, err)
		if errors.Is(err, coord.ErrBadKey) {
			addNames(&m.badKey, holderName(d.Holder))
		} else {
			addNames(&m.hint, holderName(d.Holder))
		}
		m.startClient(a)
		return
	}
	if d.Warn <= 0 {
		m.startClient(a)
		return
	}
	m.notice = fmt.Sprintf("Übernahme von %s läuft", holderName(d.Holder))
	m.setDeadline(m.d.Clock.Now().Add(d.Warn), func() { m.startClient(a) })
}

func (m *Manager) buildSpec(a *attempt, cfg config.Config) client.Spec {
	w, h := m.d.Screen()
	s := client.Spec{
		Host:       a.hmi.Host,
		Port:       a.hmi.Port,
		Password:   a.password,
		ViewOnly:   a.viewOnly(),
		Quality:    cfg.Client.Quality,
		ColorDepth: cfg.Client.ColorDepth,
		Scale:      cfg.Client.Scale,
		W:          w,
		H:          h,
		SessionN:   m.sessionN,
		URL:        a.hmi.URL,
	}
	// Ausblenden gilt nur für die automatisch gestartete Daueranzeige: Wer per
	// Touch beginnt, braucht „Zurück“ (Spec Abschnitt 4).
	a.barHidden = cfg.Kiosk.HideBar && a.permanent && a.auto
	s.FullScreen = a.barHidden
	if !a.barHidden {
		s.Y = barHeight
		s.H = h - barHeight
	}
	return s
}

const barHeight = 48

func (m *Manager) startClient(a *attempt) {
	dir, cleanup, err := m.d.RAMDir()
	if err != nil {
		log.Printf("Sitzung: RAM-Ordner für HMI %q: %v", a.hmi.ID, err)
		m.end(a, "Sitzungsdaten konnten nicht angelegt werden", false)
		return
	}
	a.cleanup = cleanup
	m.sessionN++
	cmd, err := m.d.Profile().Prepare(dir, m.buildSpec(a, m.d.Config()))
	if err != nil {
		log.Printf("Sitzung: Profil für HMI %q: %v", a.hmi.ID, err)
		m.end(a, "Der VNC-Client konnte nicht vorbereitet werden", false)
		return
	}
	// Mitschauen belegt nicht.
	if !a.watch && !a.local() {
		m.d.Coord.Hold(a.hmi.ID, strconv.Itoa(m.sessionN))
	}
	m.launch(a, cmd, "Der VNC-Client konnte nicht gestartet werden")
}

// launch startet den Prozess und überwacht sein Ende.
func (m *Manager) launch(a *attempt, cmd client.Command, failNotice string) {
	proc, err := m.d.Runner.Start(cmd)
	if err != nil {
		log.Printf("Sitzung: Client für HMI %q: %v", a.hmi.ID, err)
		m.end(a, failNotice, false)
		return
	}
	now := m.d.Clock.Now()
	a.proc, a.exited = proc, make(chan struct{})
	a.startedAt, a.lastActivity = now, now
	m.state = StateStarting
	log.Printf("Sitzung %d: Client für HMI %q gestartet", m.sessionN, a.hmi.ID)
	go func() {
		err := <-proc.Done()
		close(a.exited)
		m.post(func() { m.onExit(a, err) })
	}()
}

// stopClient wartet nicht: Stop läuft nebenher, onExit schließt den Ablauf ab.
func (m *Manager) stopClient(a *attempt, notice string) {
	a.endNotice = notice
	m.notice = notice
	if m.state == StateStopping {
		return
	}
	m.state = StateStopping
	m.clearDeadline()
	proc := a.proc
	go func() { _ = proc.Stop(StopGrace) }()
}

// endOrStop beendet einen Versuch, ob sein Client schon läuft oder nicht.
func (m *Manager) endOrStop(a *attempt, notice string) {
	if a.proc != nil {
		m.stopClient(a, notice)
		return
	}
	m.end(a, notice, false)
}

func (m *Manager) onExit(a *attempt, err error) {
	if m.cur != a {
		return
	}
	switch {
	case m.state == StateStopping:
		m.end(a, a.endNotice, a.operator)
	case a.takenNotice != "":
		m.end(a, a.takenNotice, false)
	case a.web && err != nil:
		log.Printf("Sitzung: Chromium der Web-Kachel %q beendet: %v", a.hmi.ID, err)
		m.end(a, fmt.Sprintf("Webseite „%s“ unerwartet beendet", a.hmi.Name), false)
	case a.web:
		m.end(a, fmt.Sprintf("Webseite „%s“ wurde geschlossen", a.hmi.Name), false)
	case err != nil:
		log.Printf("Sitzung: Client von HMI %q beendet: %v", a.hmi.ID, err)
		m.end(a, fmt.Sprintf("Client für „%s“ unerwartet beendet", a.hmi.Name), false)
	default:
		m.end(a, fmt.Sprintf("Verbindung zu „%s“ beendet", a.hmi.Name), false)
	}
}

func (m *Manager) cleanupRAM(a *attempt) {
	if a.cleanup != nil {
		a.cleanup()
		a.cleanup = nil
	}
}

// end schließt einen Versuch ab. Ein Client läuft dann nicht mehr. Gemeinsamer
// Ausgang aller Wege: Aufräumen, Freigeben, Kacheln oder Neuverbinden.
func (m *Manager) end(a *attempt, notice string, operator bool) {
	now := m.d.Clock.Now()
	a.cancel()
	if a.remote != nil {
		msg := notice
		if msg == "" {
			msg = remoteStartingMessage
		}
		m.answer(a, RemoteResult{Outcome: RemoteNotReady, Message: msg})
		a.remote.password = ""
	}
	a.password = ""
	m.cleanupRAM(a)
	m.release(a)
	m.reportEnd(a, notice, operator)
	m.cur = nil
	m.clearDeadline()
	if a.proc != nil {
		m.hint, m.badKey = nil, nil
	}
	if m.afterPermanentEnd(a, notice, operator, now) {
		return
	}
	m.state = StateTiles
	m.notice = notice
}

// reportEnd meldet nur Versuche, die bis zum Client gekommen sind.
func (m *Manager) reportEnd(a *attempt, notice string, operator bool) {
	if m.d.OnEnd == nil || a.proc == nil {
		return
	}
	reason := notice
	switch {
	case reason != "":
	case operator:
		reason = "Zurück durch Bediener"
	default:
		reason = "Beendet"
	}
	e := Ended{
		HMI: a.hmi.ID, HMIName: a.hmi.Name,
		Web:      a.web,
		ViewOnly: a.hmi.ViewOnly, Watch: a.watch, Permanent: a.permanent,
		Connected: !a.connectedAt.IsZero(),
		Duration:  m.d.Clock.Now().Sub(a.startedAt),
		Reason:    reason,
	}
	if r := a.remote; r != nil {
		e.Remote, e.HMIName, e.ViewOnly = true, r.display(), r.viewOnly || a.hmi.ViewOnly
	}
	m.d.OnEnd(e)
}

func (m *Manager) onBack() {
	switch m.state {
	case StateRequesting, StateConfirming:
		m.end(m.cur, "", true)
	case StateStarting, StateConnected, StateWarned:
		a := m.cur
		a.operator = true
		m.stopClient(a, a.takenNotice)
	case StateStopping:
		// Die Ursache des laufenden Stopps bleibt sichtbar.
		m.cur.operator = true
	case StateReconnecting:
		m.perm.halted = true
		m.perm.backoff = 0
		m.clearDeadline()
		m.state = StateTiles
		m.notice = ""
	}
}

// onTakeoverEvent: Für ein HMI, um das es gerade nicht geht, ist das Ereignis
// überholt. Beim Mitschauen belegen wir nichts, also gibt es nichts zu räumen.
func (m *Manager) onTakeoverEvent(ev coord.Event) {
	a := m.cur
	if a == nil || a.hmi.ID != ev.HMI || a.watch || a.local() || m.state == StateStopping {
		return
	}
	by := holderName(&ev.By)
	taken := "Übernommen von " + by
	switch {
	case a.proc == nil:
		// Noch kein Client: Eine gleichzeitige Anfrage wurde verloren.
		m.end(a, taken, false)
	case ev.After <= 0:
		a.takenNotice = taken
		m.stopClient(a, taken)
	default:
		at := m.d.Clock.Now().Add(ev.After)
		if m.state == StateWarned && !at.Before(m.timers.deadline) {
			return
		}
		a.takenNotice = taken
		m.state = StateWarned
		m.notice = fmt.Sprintf("%s übernimmt, Trennung in %d s", by, int(ev.After.Round(time.Second)/time.Second))
		m.setDeadline(at, func() { m.stopClient(a, taken) })
	}
}

// checkConfig beendet den Versuch, wenn sein Ziel weg ist oder anders erreicht
// würde. Alle anderen Änderungen lassen die Sitzung in Ruhe.
func (m *Manager) checkConfig(cfg config.Config) {
	a := m.cur
	if a == nil || a.adHoc || m.state == StateStopping {
		return
	}
	h, ok := findHMI(cfg, a.hmi.ID)
	if !ok {
		m.endOrStop(a, fmt.Sprintf("„%s“ wurde gelöscht", a.hmi.Name))
		return
	}
	if h.Kind != a.hmi.Kind || h.URL != a.hmi.URL || h.Host != a.hmi.Host || h.Port != a.hmi.Port || a.remote == nil && m.passwordChanged(a, h) {
		m.endOrStop(a, fmt.Sprintf("Einstellungen von „%s“ wurden geändert", a.hmi.Name))
		return
	}
	switch {
	case a.remote != nil:
		// Eine PDH-Sitzung wird nie zur Daueranzeige.
	case cfg.Kiosk.PermanentHMI == a.hmi.ID:
		a.permanent = true
	case a.permanent:
		m.endOrStop(a, fmt.Sprintf("„%s“ ist nicht mehr die Daueranzeige", a.hmi.Name))
	}
}

// passwordChanged entschlüsselt nur bei geändertem Geheimtext. Dieser allein
// beweist nichts: Dasselbe Passwort kann neu verschlüsselt gespeichert sein.
func (m *Manager) passwordChanged(a *attempt, h config.HMI) bool {
	if h.PasswordEnc == a.hmi.PasswordEnc {
		return false
	}
	a.hmi.PasswordEnc = h.PasswordEnc
	if !a.checked {
		return false // precheck liest dann schon das neue
	}
	pw, err := m.d.Password(h)
	return err != nil || pw != a.password
}

// startWeb startet das Chromium-Fenster einer Web-Kachel. Ohne VNC gibt es keinen
// RAM-Ordner und kein Passwort; das Profil liegt dauerhaft auf der Datenpartition.
func (m *Manager) startWeb(a *attempt) {
	var p client.Profile
	if m.d.Web != nil {
		p = m.d.Web()
	}
	if p == nil {
		m.end(a, "Webseiten lassen sich auf diesem Terminal nicht anzeigen (kein Chromium)", false)
		return
	}
	if m.d.WebReady == nil {
		m.launchWeb(a, p)
		return
	}
	h := a.hmi
	go func() {
		err := m.d.WebReady(a.ctx, h)
		m.post(func() { m.onWebReady(a, p, err) })
	}()
}

func (m *Manager) onWebReady(a *attempt, p client.Profile, err error) {
	if m.cur != a {
		return
	}
	if err != nil {
		log.Printf("Sitzung: Richtlinie für Web-Kachel %q nicht bereit: %v", a.hmi.ID, err)
		m.end(a, fmt.Sprintf("Die Freigabe für „%s“ steht noch nicht in der Chromium-Richtlinie", a.hmi.Name), false)
		return
	}
	m.launchWeb(a, p)
}

func (m *Manager) launchWeb(a *attempt, p client.Profile) {
	m.sessionN++
	cmd, err := p.Prepare("", m.buildSpec(a, m.d.Config()))
	if err != nil {
		log.Printf("Sitzung: Web-Kachel %q: %v", a.hmi.ID, err)
		m.end(a, "Die Webseite konnte nicht geöffnet werden", false)
		return
	}
	m.launch(a, cmd, "Die Webseite konnte nicht geöffnet werden")
}

// viewOnly: Bei PDH gilt der Modus des Befehls, sonst die Einstellung der Kachel; Mitschauen immer.
func (a *attempt) viewOnly() bool {
	if a.remote != nil {
		// Eine Kachel „nur ansehen“ bleibt es, auch wenn PDH bedienen will.
		return a.remote.viewOnly || a.hmi.ViewOnly || a.watch
	}
	return a.hmi.ViewOnly || a.watch
}
