package session

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"hmikiosk/internal/config"
	"hmikiosk/internal/coord"
	"hmikiosk/internal/rfb"
)

func remoteReq(host string, port int, view bool) RemoteRequest {
	return RemoteRequest{Host: host, Port: port, Password: "pw-pdh", ViewOnly: view, Label: "Linie 3 / Presse 2", User: "M. Muster"}
}

func (h *harness) openRemote(r RemoteRequest) RemoteResult {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), guard)
	defer cancel()
	res, err := h.m.OpenRemote(ctx, r)
	if err != nil {
		h.t.Fatalf("OpenRemote: %v", err)
	}
	return res
}

func (h *harness) hasLog(prefix string) bool {
	for _, x := range h.log.all() {
		if strings.HasPrefix(x, prefix) {
			return true
		}
	}
	return false
}

const remoteBar = "Linie 3 / Presse 2 – von PDH: M. Muster"

// Ohne passende Kachel: nur lokal, ohne Abstimmung, Passwort und Modus aus dem Befehl.
func TestRemoteOpenAdHocStartsLocalSession(t *testing.T) {
	h := newHarness(t, nil)
	res := h.openRemote(remoteReq("10.9.9.9", 5900, true))
	if res.Outcome != RemoteOpened {
		t.Fatalf("Ergebnis %+v", res)
	}
	s := h.waitState(StateStarting)
	if s.HMIName != remoteBar || !s.Remote || s.RemoteLabel != "Linie 3 / Presse 2" || !s.ViewOnly || s.HMI != RemoteAdHocID {
		t.Fatalf("Snapshot %+v", s)
	}
	if !s.Since.Equal(h.clk.Now()) {
		t.Fatalf("seit %v", s.Since)
	}
	spec := h.profile.last()
	if spec.Host != "10.9.9.9" || spec.Port != 5900 || spec.Password != "pw-pdh" || !spec.ViewOnly {
		t.Fatalf("Spec %+v", spec)
	}
	if h.hasLog("request:") || h.hasLog("hold:") {
		t.Fatalf("Abstimmung bei nur lokaler Sitzung: %v", h.log.all())
	}
	// Die Konfiguration kennt das Ziel nicht: Der Takt darf die Sitzung nicht als gelöscht beenden.
	h.tcp.Store(true)
	h.advance(2 * PollEvery)
	h.waitState(StateConnected)
	h.m.Back()
	h.waitState(StateTiles)
	if h.hasLog("release:") {
		t.Fatalf("Freigabe ohne Belegung: %v", h.log.all())
	}
}

// Passende Kachel (Host und Port): diese öffnen, mit Abstimmung; Passwort aus dem Befehl.
func TestRemoteOpenMatchingTileUsesCoordination(t *testing.T) {
	h := newHarness(t, nil)
	res := h.openRemote(remoteReq("10.0.0.1", 5900, false))
	if res.Outcome != RemoteOpened {
		t.Fatalf("Ergebnis %+v", res)
	}
	s := h.waitState(StateStarting)
	if s.HMI != "a" || s.HMIName != remoteBar || s.ViewOnly {
		t.Fatalf("Snapshot %+v", s)
	}
	spec := h.profile.last()
	if spec.Password != "pw-pdh" || spec.ViewOnly {
		t.Fatalf("Spec %+v", spec)
	}
	if h.log.count("request:a") != 1 || h.log.count("hold:a") != 1 {
		t.Fatalf("Abstimmung %v", h.log.all())
	}
	// Ein geändertes Passwort der Kachel betrifft die Sitzung mit dem PDH-Passwort nicht.
	h.setConfig(func(c *config.Config) { c.HMIs[0].PasswordEnc = "enc-neu" })
	h.advance(2 * PollEvery)
	if st := h.m.Snapshot().State; st != StateStarting {
		t.Fatalf("Zustand %s nach Passwortänderung der Kachel", st)
	}
}

func TestRemoteOpenViewOnlyOnTile(t *testing.T) {
	h := newHarness(t, nil)
	if res := h.openRemote(remoteReq("10.0.0.2", 5901, true)); res.Outcome != RemoteOpened {
		t.Fatalf("Ergebnis %+v", res)
	}
	h.waitState(StateStarting)
	if !h.profile.last().ViewOnly {
		t.Fatal("Modus view ohne ViewOnly")
	}
}

func TestRemoteOpenTileHeldByPeerIsBusy(t *testing.T) {
	for name, r := range map[string]coord.Result{
		"gesperrt":  busy("Halle 3"),
		"übernahme": takeover("Halle 3", coord.TakeoverWarn),
		"rückfrage": askConfirm("Halle 3"),
		"verloren":  {Rejected: true, Decision: coord.Decision{Outcome: coord.Allow}},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, func(h *harness) { h.coord.queue(r) })
			res := h.openRemote(remoteReq("10.0.0.1", 5900, false))
			if res.Outcome != RemoteBusy || !strings.Contains(res.HMI, "Presse") {
				t.Fatalf("Ergebnis %+v", res)
			}
			if r.Decision.Holder != nil && !strings.Contains(res.HMI, "Halle 3") {
				t.Fatalf("Halter fehlt: %+v", res)
			}
			s := h.waitState(StateTiles)
			if s.Notice != "" {
				t.Fatalf("Hinweis am Terminal %q", s.Notice)
			}
			if h.runner.count() != 0 || h.hasLog("takeover:") {
				t.Fatalf("Client oder Übernahme: %v", h.log.all())
			}
		})
	}
}

func TestRemoteOpenWatchOnlyForView(t *testing.T) {
	watch := coord.Result{Decision: coord.Decision{Outcome: coord.AllowWatch, Holder: &coord.Holder{Name: "Anzeige"}}}
	h := newHarness(t, func(h *harness) { h.coord.queue(watch, watch) })
	if res := h.openRemote(remoteReq("10.0.0.1", 5900, false)); res.Outcome != RemoteBusy {
		t.Fatalf("Bedienen bei Mitschauen: %+v", res)
	}
	h.waitState(StateTiles)
	if res := h.openRemote(remoteReq("10.0.0.1", 5900, true)); res.Outcome != RemoteOpened {
		t.Fatalf("Ansehen bei Mitschauen: %+v", res)
	}
	h.waitState(StateStarting)
	if !h.profile.last().ViewOnly || h.hasLog("hold:") {
		t.Fatalf("Mitschauen: %+v %v", h.profile.last(), h.log.all())
	}
}

func TestRemoteOpenWhileSessionRunsIsBusy(t *testing.T) {
	h := newHarness(t, nil)
	started := h.clk.Now()
	h.connect("b")
	h.advance(10 * time.Second)
	res := h.openRemote(remoteReq("10.9.9.9", 5900, true))
	if res.Outcome != RemoteBusy || res.HMI != "Ofen" || !res.Since.Equal(started) {
		t.Fatalf("Ergebnis %+v", res)
	}
	if h.runner.count() != 1 {
		t.Fatal("zweiter Client")
	}
}

func TestRemoteOpenDuringPermanentReconnectIsBusy(t *testing.T) {
	h := newHarness(t, func(h *harness) { h.cfg.Kiosk.PermanentHMI = "a"; h.probe = rfb.ProbeResult{} })
	h.waitState(StateReconnecting)
	res := h.openRemote(remoteReq("10.9.9.9", 5900, true))
	if res.Outcome != RemoteBusy || res.HMI != "Presse" || res.Since.IsZero() {
		t.Fatalf("Ergebnis %+v", res)
	}
}

func TestRemoteOpenWhileStartingIsNotReady(t *testing.T) {
	h := newHarness(t, func(h *harness) { h.coord.gate = make(chan struct{}) })
	h.open("a")
	h.waitState(StateRequesting)
	res := h.openRemote(remoteReq("10.9.9.9", 5900, true))
	if res.Outcome != RemoteNotReady || res.Message == "" {
		t.Fatalf("Ergebnis %+v", res)
	}
	close(h.coord.gate)
}

func TestRemoteOpenBlockedIsNotReady(t *testing.T) {
	h := newHarness(t, nil)
	msg := "Wartungsmodus"
	h.blocked.Store(&msg)
	res := h.openRemote(remoteReq("10.9.9.9", 5900, true))
	if res.Outcome != RemoteNotReady || res.Message != msg {
		t.Fatalf("Ergebnis %+v", res)
	}
	if h.runner.count() != 0 {
		t.Fatal("Client gestartet")
	}
}

func TestRemoteCloseOnlyClosesPDHSession(t *testing.T) {
	h := newHarness(t, nil)
	if h.m.CloseRemote() {
		t.Fatal("ohne Sitzung geschlossen")
	}
	h.connect("a")
	if h.m.CloseRemote() {
		t.Fatal("lokale Sitzung geschlossen")
	}
	if st := h.m.Snapshot().State; st != StateConnected {
		t.Fatalf("Zustand %s", st)
	}
	h.m.Back()
	h.waitState(StateTiles)

	if res := h.openRemote(remoteReq("10.9.9.9", 5900, true)); res.Outcome != RemoteOpened {
		t.Fatalf("Ergebnis %+v", res)
	}
	h.waitState(StateStarting)
	if !h.m.CloseRemote() {
		t.Fatal("PDH-Sitzung nicht geschlossen")
	}
	h.waitNotice(StateTiles, "PDH")
}

func TestRemoteEndReportedWithLabel(t *testing.T) {
	h, ends := withEndLog(t)
	h.openRemote(remoteReq("10.9.9.9", 5900, true))
	h.waitState(StateStarting)
	h.m.Back()
	h.waitState(StateTiles)
	got := ends.all()
	if len(got) != 1 || !got[0].Remote || got[0].HMIName != remoteBar || !got[0].ViewOnly {
		t.Fatalf("Ende %+v", got)
	}
}

// Lokale Sitzungen tragen keine PDH-Angaben, aber ihren Beginn.
func TestRemoteSnapshotForLocalSessionHasNoRemote(t *testing.T) {
	h := newHarness(t, nil)
	h.open("a")
	s := h.waitState(StateStarting)
	if s.Remote || s.RemoteLabel != "" || s.HMIName != "Presse" || s.Since.IsZero() {
		t.Fatalf("Snapshot %+v", s)
	}
}

// Ein falsches PDH-Passwort: Antwort erst nach der RFB-Prüfung, kein Client, keine Markierung der Kachel.
func TestRemoteWrongPasswordAnswersInvalidWithoutClient(t *testing.T) {
	for _, host := range []string{"10.0.0.1", "10.9.9.9"} {
		h := newHarness(t, func(h *harness) { h.probe = rfb.ProbeResult{Reachable: true, AuthRejected: true} })
		res := h.openRemote(remoteReq(host, 5900, false))
		if res.Outcome != RemoteInvalid || !strings.Contains(res.Message, "VNC-Anmeldung am HMI fehlgeschlagen") {
			t.Fatalf("%s: Ergebnis %+v", host, res)
		}
		s := h.waitState(StateTiles)
		h.mu.Lock()
		marked := len(h.marked)
		h.mu.Unlock()
		if marked != 0 || h.runner.count() != 0 || s.Notice != "" {
			t.Fatalf("%s: markiert %d, Clients %d, Hinweis %q", host, marked, h.runner.count(), s.Notice)
		}
	}
}

func TestRemoteUnreachableAnswersNotReady(t *testing.T) {
	h := newHarness(t, func(h *harness) { h.probe = rfb.ProbeResult{} })
	res := h.openRemote(remoteReq("10.9.9.9", 5900, true))
	if res.Outcome != RemoteNotReady || !strings.Contains(res.Message, "nicht erreichbar") || h.runner.count() != 0 {
		t.Fatalf("Ergebnis %+v", res)
	}
}

// „Geöffnet“ erst nach erfolgreicher Prüfung.
func TestRemoteOpenedOnlyAfterProbe(t *testing.T) {
	h := newHarness(t, func(h *harness) { h.probeGate = make(chan struct{}) })
	done := make(chan RemoteResult, 1)
	go func() { res, _ := h.m.OpenRemote(context.Background(), remoteReq("10.9.9.9", 5900, true)); done <- res }()
	h.waitState(StateRequesting)
	select {
	case res := <-done:
		t.Fatalf("Antwort vor der Prüfung: %+v", res)
	case <-time.After(50 * time.Millisecond):
	}
	close(h.probeGate)
	if res := <-done; res.Outcome != RemoteOpened {
		t.Fatalf("Ergebnis %+v", res)
	}
}

func TestRemoteOpenMatchesTileByResolvedAddress(t *testing.T) {
	names := map[string][]net.IP{
		"10.0.0.1":    {net.ParseIP("10.0.0.1")},
		"hmi-3.local": {net.ParseIP("10.0.0.1")},
		"presse.werk": {net.ParseIP("10.0.0.1")},
	}
	resolve := func(_ context.Context, host string) ([]net.IP, error) {
		if ips, ok := names[host]; ok {
			return ips, nil
		}
		return nil, errors.New("unbekannt")
	}
	// Anfrage per Name, Kachel per IP.
	h := newHarness(t, func(h *harness) { h.resolve = resolve })
	if res := h.openRemote(remoteReq("hmi-3.local", 5900, true)); res.Outcome != RemoteOpened {
		t.Fatalf("Ergebnis %+v", res)
	}
	if s := h.waitState(StateStarting); s.HMI != "a" || h.log.count("request:a") != 1 {
		t.Fatalf("Snapshot %+v, %v", s, h.log.all())
	}
	// Anfrage per IP, Kachel per Name.
	h = newHarness(t, func(h *harness) {
		h.resolve = resolve
		h.cfg.HMIs[0].Host = "presse.werk"
	})
	h.openRemote(remoteReq("10.0.0.1", 5900, true))
	if s := h.waitState(StateStarting); s.HMI != "a" {
		t.Fatalf("Snapshot %+v", s)
	}
	// Nicht auflösbar: keine Kachel, nur lokal.
	h = newHarness(t, func(h *harness) { h.resolve = resolve })
	h.openRemote(remoteReq("gibtsnicht.werk", 5900, true))
	if s := h.waitState(StateStarting); s.HMI != RemoteAdHocID {
		t.Fatalf("Snapshot %+v", s)
	}
}

func TestRemoteOpenRejectsLoopbackByResolution(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.resolve = func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP("127.0.1.1")}, nil }
	})
	res := h.openRemote(remoteReq("kiosk.werk", 5900, true))
	if res.Outcome != RemoteInvalid || h.runner.count() != 0 {
		t.Fatalf("Ergebnis %+v", res)
	}
}

// Bricht PDH die Anfrage ab, entsteht keine Sitzung.
func TestRemoteCancelledDuringCoordinationEndsAttempt(t *testing.T) {
	h := newHarness(t, func(h *harness) { h.coord.gate = make(chan struct{}) })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := h.m.OpenRemote(ctx, remoteReq("10.0.0.1", 5900, false)); done <- err }()
	h.waitState(StateRequesting)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Fehler %v", err)
	}
	h.waitState(StateTiles)
	close(h.coord.gate)
	h.advance(2 * PollEvery)
	if h.runner.count() != 0 || h.m.Snapshot().State != StateTiles {
		t.Fatalf("Sitzung trotz Abbruch: %v", h.log.all())
	}
	if h.log.count("release:a") == 0 {
		t.Fatalf("keine Freigabe: %v", h.log.all())
	}
}

func TestRemoteCancelledDuringProbeEndsAttempt(t *testing.T) {
	h := newHarness(t, func(h *harness) { h.probeGate = make(chan struct{}) })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := h.m.OpenRemote(ctx, remoteReq("10.9.9.9", 5900, false)); done <- err }()
	h.waitState(StateRequesting)
	cancel()
	<-done
	h.waitState(StateTiles)
	close(h.probeGate)
	h.advance(2 * PollEvery)
	if h.runner.count() != 0 {
		t.Fatal("Client trotz Abbruch")
	}
}

// Eine Kachel „nur ansehen“ bleibt es, auch wenn PDH bedienen will.
func TestRemoteControlOnViewOnlyTileStaysViewOnly(t *testing.T) {
	h := newHarness(t, func(h *harness) { h.cfg.HMIs[0].ViewOnly = true })
	h.openRemote(remoteReq("10.0.0.1", 5900, false))
	s := h.waitState(StateStarting)
	if !s.ViewOnly || !h.profile.last().ViewOnly {
		t.Fatalf("Snapshot %+v, Spec %+v", s, h.profile.last())
	}
}

func TestRemoteCloseWhileRequesting(t *testing.T) {
	h := newHarness(t, func(h *harness) { h.coord.gate = make(chan struct{}) })
	done := make(chan RemoteResult, 1)
	go func() {
		res, _ := h.m.OpenRemote(context.Background(), remoteReq("10.0.0.1", 5900, false))
		done <- res
	}()
	h.waitState(StateRequesting)
	if !h.m.CloseRemote() {
		t.Fatal("nicht geschlossen")
	}
	if res := <-done; res.Outcome == RemoteOpened {
		t.Fatalf("Ergebnis %+v", res)
	}
	h.waitState(StateTiles)
	close(h.coord.gate)
	h.advance(2 * PollEvery)
	if h.runner.count() != 0 {
		t.Fatal("Client nach Schließen")
	}
}

func TestRemoteSessionIgnoresLocalTap(t *testing.T) {
	h := newHarness(t, nil)
	h.openRemote(remoteReq("10.9.9.9", 5900, true))
	h.waitState(StateStarting)
	h.open("b")
	h.sync()
	if h.runner.count() != 1 || h.m.Snapshot().HMI != RemoteAdHocID {
		t.Fatalf("Tippen während PDH-Sitzung: %v", h.log.all())
	}
}

func TestRemoteTileSessionTakenOverByPeer(t *testing.T) {
	h := newHarness(t, nil)
	h.openRemote(remoteReq("10.0.0.1", 5900, false))
	h.waitState(StateStarting)
	h.coord.events <- coord.Event{HMI: "a", By: coord.Holder{Terminal: "t2", Name: "Nebenan"}}
	h.waitNotice(StateTiles, "Übernommen von Nebenan")
}

func TestRemoteTileSessionEndsWhenTileHostChanges(t *testing.T) {
	h := newHarness(t, nil)
	h.openRemote(remoteReq("10.0.0.1", 5900, false))
	h.waitState(StateStarting)
	h.setConfig(func(c *config.Config) { c.HMIs[0].Host = "10.0.0.99" })
	h.advance(PollEvery)
	h.waitNotice(StateTiles, "geändert")
}
