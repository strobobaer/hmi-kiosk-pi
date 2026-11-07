package session

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"hmikiosk/internal/config"
	"hmikiosk/internal/rfb"
)

func (h *harness) markedIDs() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.marked)
}

func (h *harness) probeCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.probes
}

func (h *harness) setProbe(r rfb.ProbeResult) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.probe = r
}

func rejectedProbe() rfb.ProbeResult {
	return rfb.ProbeResult{Reachable: true, AuthRejected: true, Err: "Anmeldung abgelehnt"}
}

func TestAuthFailedNoticeText(t *testing.T) {
	if authFailedNotice != "VNC-Passwort wurde abgelehnt – bitte in der Admin-Seite prüfen." {
		t.Fatalf("Text %q", authFailedNotice)
	}
}

func TestRejectedPasswordMarksHMI(t *testing.T) {
	h := newHarness(t, func(h *harness) { h.probe = rejectedProbe() })
	h.open("a")
	h.waitNotice(StateTiles, authFailedNotice)
	deadline := time.Now().Add(guard)
	for len(h.markedIDs()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := h.markedIDs(); !slices.Equal(got, []string{"a"}) {
		t.Fatalf("markiert %v", got)
	}
}

func TestMarkedHMIStartsNoLogin(t *testing.T) {
	h := newHarness(t, func(h *harness) { h.cfg.HMIs[0].AuthFailed = true })
	err := h.m.Open("a")
	if err == nil || !strings.Contains(err.Error(), authFailedNotice) {
		t.Fatalf("Open: %v", err)
	}
	s := h.waitNotice(StateTiles, authFailedNotice)
	if h.probeCount() != 0 || h.log.count("request:a") != 0 || h.runner.count() != 0 || s.HMI != "" {
		t.Fatalf("Anmeldung gestartet: %v", h.log.all())
	}
	// Ein anderes HMI bleibt bedienbar.
	h.open("b")
	h.waitState(StateStarting)
}

func TestPermanentStopsAfterRejectedPassword(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.cfg.Kiosk.PermanentHMI = "a"
		h.probe = rejectedProbe()
	})
	h.waitNotice(StateTiles, authFailedNotice)
	h.advance(10 * time.Minute)
	if n := h.probeCount(); n != 1 {
		t.Fatalf("%d Anmeldeversuche", n)
	}
	if s := h.m.Snapshot(); s.State != StateTiles || s.Notice != authFailedNotice {
		t.Fatalf("Snapshot %+v", s)
	}
}

func TestPermanentDoesNotStartWhenAlreadyMarked(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.cfg.Kiosk.PermanentHMI = "a"
		h.cfg.HMIs[0].AuthFailed = true
	})
	h.waitNotice(StateTiles, authFailedNotice)
	h.advance(time.Minute)
	if h.probeCount() != 0 {
		t.Fatal("Anmeldung trotz Markierung")
	}
	// Die Markierung fällt: Die Daueranzeige beginnt wieder.
	h.setConfig(func(c *config.Config) { c.HMIs[0].AuthFailed = false })
	h.advance(PollEvery + time.Second)
	h.waitState(StateStarting)
}

func TestPermanentResumesAfterMarkClearedFollowingRejection(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.cfg.Kiosk.PermanentHMI = "a"
		h.probe = rejectedProbe()
	})
	h.waitNotice(StateTiles, authFailedNotice)
	h.advance(PollEvery + time.Second) // Markierung ist in der Konfiguration sichtbar
	h.setProbe(rfb.ProbeResult{Reachable: true, AuthOK: true})
	h.setConfig(func(c *config.Config) { c.HMIs[0].AuthFailed = false })
	h.advance(PollEvery + time.Second)
	h.waitState(StateStarting)
}

func TestPermanentResumesAfterPasswordChange(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.cfg.Kiosk.PermanentHMI = "a"
		h.probe = rejectedProbe()
	})
	h.waitNotice(StateTiles, authFailedNotice)
	h.setProbe(rfb.ProbeResult{Reachable: true, AuthOK: true})
	h.setConfig(func(c *config.Config) { c.HMIs[0].AuthFailed, c.HMIs[0].PasswordEnc = false, "enc-neu" })
	h.advance(PollEvery + time.Second)
	h.waitState(StateStarting)
}

func lockedProbe() rfb.ProbeResult {
	return rfb.ProbeResult{Reachable: true, Locked: true,
		Err: "Der VNC-Server lehnt die Verbindung ab: „Too many security failures“"}
}

func TestLockoutNoticeOnTouch(t *testing.T) {
	h := newHarness(t, func(h *harness) { h.probe = lockedProbe() })
	h.open("a")
	s := h.waitNotice(StateTiles, "vorübergehend gesperrt (zu viele Fehlversuche)")
	for _, want := range []string{
		"Abhilfe: einige Minuten warten oder den VNC-Dienst am HMI neu starten; danach das Passwort prüfen.",
		"Too many security failures",
	} {
		if !strings.Contains(s.Notice, want) {
			t.Fatalf("Hinweis %q ohne %q", s.Notice, want)
		}
	}
	if len(h.markedIDs()) != 0 {
		t.Fatal("Sperre markiert das Passwort als falsch")
	}
}

func TestPermanentWaitsFiveMinutesDuringLockout(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.cfg.Kiosk.PermanentHMI = "a"
		h.probe = lockedProbe()
	})
	s := h.waitState(StateReconnecting)
	if !strings.Contains(s.Notice, "vorübergehend gesperrt") || !strings.Contains(s.Notice, "5 min") {
		t.Fatalf("Hinweis %q", s.Notice)
	}
	h.advance(5*time.Minute - time.Second)
	if n := h.probeCount(); n != 1 {
		t.Fatalf("%d Versuche vor Ablauf der Pause", n)
	}
	h.setProbe(rfb.ProbeResult{Reachable: true, AuthOK: true})
	h.advance(2 * time.Second)
	h.waitState(StateStarting)
	if n := h.probeCount(); n != 2 {
		t.Fatalf("%d Versuche", n)
	}
}

func TestLockoutDoesNotGrowReconnectBackoff(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.cfg.Kiosk.PermanentHMI = "a"
		h.probe = lockedProbe()
	})
	h.waitState(StateReconnecting)
	h.setProbe(rfb.ProbeResult{Reachable: true, AuthOK: true})
	h.advance(5*time.Minute + time.Second)
	h.waitState(StateStarting)
	h.runner.last().exit(errors.New("weg"))
	s := h.waitState(StateReconnecting)
	if !strings.Contains(s.Notice, "in 2 s") {
		t.Fatalf("Wartezeit gewachsen: %q", s.Notice)
	}
}
