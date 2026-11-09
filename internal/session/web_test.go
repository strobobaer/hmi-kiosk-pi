package session

import (
	"errors"
	"strings"
	"testing"
	"time"

	"hmikiosk/internal/config"
	"hmikiosk/internal/coord"
)

const webURL = "http://grafana:3000/d/abc?kiosk"

func withWeb(mutate func(*harness)) func(*harness) {
	return func(h *harness) {
		h.cfg.HMIs = append(h.cfg.HMIs, config.HMI{ID: "w", Name: "Grafana", Kind: config.KindWeb, URL: webURL})
		if mutate != nil {
			mutate(h)
		}
	}
}

func (h *harness) connectWeb() *fakeProc {
	h.t.Helper()
	h.open("w")
	h.waitState(StateStarting)
	h.advance(WebShowAfter)
	h.waitState(StateConnected)
	return h.runner.last()
}

func (h *harness) noCoordination(id string) {
	h.t.Helper()
	for _, e := range h.log.all() {
		if strings.HasSuffix(e, ":"+id) {
			h.t.Fatalf("Abstimmung für Web-Kachel: %v", h.log.all())
		}
	}
}

func TestWebOpenStartsChromiumWithoutCoordination(t *testing.T) {
	h := newHarness(t, withWeb(nil))
	h.open("w")
	s := h.waitState(StateStarting)
	if !s.Web || s.HMIName != "Grafana" || s.ViewOnly {
		t.Fatalf("Stand %+v", s)
	}
	h.web.mu.Lock()
	n, spec := len(h.web.specs), h.web.specs[0]
	h.web.mu.Unlock()
	if n != 1 || len(h.profile.specs) != 0 {
		t.Fatalf("Web-Profil %d, VNC-Profil %d", n, len(h.profile.specs))
	}
	if spec.URL != webURL || spec.Y != 48 || spec.W != 1920 || spec.H != 1080-48 || spec.Password != "" {
		t.Fatalf("Spec %+v", spec)
	}
	h.mu.Lock()
	probes, cleanups := h.probes, h.cleanups
	h.mu.Unlock()
	if probes != 0 {
		t.Fatal("RFB-Prüfung für eine Web-Kachel")
	}
	// Ohne TCP-Verbindung zum HMI gilt die Seite nach kurzer Zeit als angezeigt.
	h.advance(WebShowAfter)
	h.waitState(StateConnected)
	h.advance(LostAfter + StartTimeout)
	if got := h.m.Snapshot().State; got != StateConnected {
		t.Fatalf("Zustand %s", got)
	}
	h.noCoordination("w")
	if cleanups != 0 || h.runner.count() != 1 {
		t.Fatalf("RAM-Ordner %d, Starts %d", cleanups, h.runner.count())
	}
}

func TestWebBackStopsChromium(t *testing.T) {
	var ended []Ended
	h := newHarness(t, withWeb(func(h *harness) { h.onEnd = func(e Ended) { ended = append(ended, e) } }))
	p := h.connectWeb()
	h.m.Back()
	s := h.waitState(StateTiles)
	if p.stops.Load() != 1 || s.Notice != "" {
		t.Fatalf("Stopps %d, Hinweis %q", p.stops.Load(), s.Notice)
	}
	h.noCoordination("w")
	h.sync()
	if len(ended) != 1 || !ended[0].Web || ended[0].Reason != "Zurück durch Bediener" {
		t.Fatalf("Ende %+v", ended)
	}
}

func TestWebCrashShowsNotice(t *testing.T) {
	h := newHarness(t, withWeb(nil))
	p := h.connectWeb()
	p.exit(errors.New("signal: segmentation fault"))
	h.waitNotice(StateTiles, "Webseite „Grafana“ unerwartet beendet")
}

func TestWebClosedByPage(t *testing.T) {
	h := newHarness(t, withWeb(nil))
	p := h.connectWeb()
	p.exit(nil)
	h.waitNotice(StateTiles, "Webseite „Grafana“ wurde geschlossen")
}

func TestWebPermanentRestartsWithBackoff(t *testing.T) {
	h := newHarness(t, withWeb(func(h *harness) { h.cfg.Kiosk.PermanentHMI = "w" }))
	h.waitState(StateStarting)
	h.advance(WebShowAfter)
	h.waitState(StateConnected)
	h.runner.last().exit(errors.New("signal: killed"))
	h.waitNotice(StateReconnecting, "Neuer Versuch in 2 s")
	h.advance(ReconnectMin)
	h.waitState(StateStarting)
	h.advance(WebShowAfter)
	h.waitState(StateConnected)
	h.runner.last().exit(errors.New("signal: killed"))
	h.waitNotice(StateReconnecting, "Neuer Versuch in 4 s")
	if h.runner.count() != 2 {
		t.Fatalf("Starts %d", h.runner.count())
	}
	h.noCoordination("w")
}

func TestWebPermanentHideBarFlag(t *testing.T) {
	h := newHarness(t, withWeb(func(h *harness) {
		h.cfg.Kiosk.PermanentHMI = "w"
		h.cfg.Kiosk.HideBar = true
	}))
	s := h.waitState(StateStarting)
	if !s.HideBar {
		t.Fatalf("Stand %+v", s)
	}
}

func TestWebInactivityReturnsToTiles(t *testing.T) {
	h := newHarness(t, withWeb(func(h *harness) { h.cfg.Kiosk.InactivityMinutes = 1 }))
	h.connectWeb()
	h.advance(time.Minute)
	h.waitNotice(StateTiles, "Inaktivität")
}

func TestWebURLChangeEndsSession(t *testing.T) {
	h := newHarness(t, withWeb(nil))
	h.connectWeb()
	h.setConfig(func(c *config.Config) { c.HMIs[2].URL = "http://grafana:3000/d/neu" })
	h.advance(PollEvery)
	h.waitNotice(StateTiles, "Einstellungen von „Grafana“ wurden geändert")
}

func TestKindChangeEndsSession(t *testing.T) {
	h := newHarness(t, withWeb(nil))
	h.connectWeb()
	h.setConfig(func(c *config.Config) {
		c.HMIs[2].Kind, c.HMIs[2].URL, c.HMIs[2].Host, c.HMIs[2].Port = config.KindVNC, "", "10.0.0.9", 5900
	})
	h.advance(PollEvery)
	h.waitNotice(StateTiles, "wurden geändert")
}

func TestWebNameChangeKeepsSession(t *testing.T) {
	h := newHarness(t, withWeb(nil))
	h.connectWeb()
	h.setConfig(func(c *config.Config) { c.HMIs[2].Name = "Grafana Halle" })
	h.advance(PollEvery)
	if s := h.m.Snapshot(); s.State != StateConnected {
		t.Fatalf("Stand %+v", s)
	}
}

func TestWebUnavailable(t *testing.T) {
	h := newHarness(t, withWeb(func(h *harness) { h.web = nil }))
	h.open("w")
	h.waitNotice(StateTiles, "Webseiten")
	if h.runner.count() != 0 {
		t.Fatal("Prozess gestartet")
	}
}

func TestWebPrepareErrorShowsNotice(t *testing.T) {
	h := newHarness(t, withWeb(func(h *harness) { h.web.err = errors.New("kaputt") }))
	h.open("w")
	h.waitNotice(StateTiles, "Webseite konnte nicht geöffnet werden")
}

func TestWebIgnoresTakeoverEvents(t *testing.T) {
	h := newHarness(t, withWeb(nil))
	h.connectWeb()
	h.coord.events <- coord.Event{HMI: "w", By: coord.Holder{Name: "Terminal 2"}}
	h.advance(PollEvery)
	if s := h.m.Snapshot(); s.State != StateConnected {
		t.Fatalf("Stand %+v", s)
	}
}

func TestWebBlockedLikeVNC(t *testing.T) {
	msg := "Die Admin-Seite ist am Terminal geöffnet"
	h := newHarness(t, withWeb(func(h *harness) { h.blocked.Store(&msg) }))
	if err := h.m.Open("w"); err == nil {
		t.Fatal("Start trotz Sperre")
	}
	if h.runner.count() != 0 {
		t.Fatal("Prozess gestartet")
	}
}

func TestWebDoubleTouchSingleProcess(t *testing.T) {
	h := newHarness(t, withWeb(nil))
	h.open("w")
	h.open("w")
	h.open("a")
	h.waitState(StateStarting)
	if h.runner.count() != 1 {
		t.Fatalf("Starts %d", h.runner.count())
	}
}

func TestWebIdle(t *testing.T) {
	h := newHarness(t, withWeb(nil))
	ran := false
	if err := h.m.WhenNoWeb(func() error { ran = true; return nil }); err != nil || !ran {
		t.Fatalf("ohne Sitzung: %v %v", err, ran)
	}
	h.connectWeb()
	ran = false
	if err := h.m.WhenNoWeb(func() error { ran = true; return nil }); !errors.Is(err, ErrWebOpen) || ran {
		t.Fatalf("mit offener Web-Kachel: %v %v", err, ran)
	}
	h.m.Back()
	h.waitState(StateTiles)
	h.connect("a")
	if err := h.m.WhenNoWeb(func() error { ran = true; return nil }); err != nil || !ran {
		t.Fatalf("mit VNC-Sitzung: %v %v", err, ran)
	}
}
