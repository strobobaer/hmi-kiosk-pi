package pdhterminal

import (
	"context"
	"log"
	"os"
	"time"

	"hmikiosk/internal/browser"
	"hmikiosk/internal/product/pdhterminal/notify"
	"hmikiosk/internal/web"
)

// window ist das Browser-Fenster des Terminals. Unter Windows das Edge-Fenster, unter
// Linux ein Platzhalter (Chromium läuft im Kiosk).
type window interface {
	Foreground() bool          // das Fenster hat den Fokus
	Raise() error              // holt es nach vorn (Windows kann das verweigern)
	MinimizeNoActivate() error // minimiert, ohne das Fenster zu aktivieren
}

const (
	edgeRetry      = 30 * time.Second // Edge fehlt: so oft neuer Versuch
	windowWaitMax  = 10 * time.Second // nach einem Neustart: so lange wird auf das Fenster gewartet
	windowWaitStep = 100 * time.Millisecond
)

// Nähte für den dauerhaften Hinweis „Edge nicht gefunden“ (im Test ersetzt).
var (
	showHint = notify.ShowHint
	hideHint = notify.HideHint
)

// edgeMissingHint setzt den Hinweis nach dem Stand von Edge: Im Kiosk steht er, solange Edge fehlt, und verschwindet,
// sobald Edge gefunden ist.
func edgeMissingHint(kiosk, missing bool) {
	if notify.EdgeHint(kiosk, missing) {
		showHint(notify.HintEdgeTitle, notify.HintEdgeDetail)
	} else if kiosk {
		hideHint()
	}
}

// startEdge zeigt die Startseite im Edge-App-Fenster und startet Edge nach dem Schließen
// neu, dann minimiert und ohne Fokus. Der Kanal schließt, sobald Edge beendet ist. Fehlt Edge,
// meldet das Terminal es einmal (Selbstprüfung „edge“ rot) und versucht es alle 30 s neu.
func (s *service) startEdge(ctx context.Context, startURL string) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		path, ok := waitForEdgeNotify(ctx, edgeRetry, readRegistry, func(missing bool) {
			if missing {
				s.setProblem(problemEdge, "Edge fehlt")
			} else {
				s.setProblem(problemEdge, "")
			}
			edgeMissingHint(s.o.Kiosk, missing)
		})
		if !ok {
			edgeMissingHint(s.o.Kiosk, false) // Ende des Programms: den Hinweis nicht stehen lassen
			return
		}
		profile := edgeProfileDir()
		if err := os.MkdirAll(profile, 0o700); err != nil {
			log.Printf("Edge-Profilordner nicht anlegbar: %v", err)
		}
		sup := browser.NewExec(path, "", func() int { w, _ := s.screen(); return w })
		sup.SetEdge(profile)
		sup.SetStartURL(startURL)
		win := newWindow(func() ([]uint32, error) { return jobPIDs(sup.Process()) })
		s.win.Store(&win)
		if h, ok := win.(interface{ Exists() bool }); ok {
			sup.SetWindowCheck(h.Exists) // Edge bleibt nach dem Schließen im Hintergrund
		}
		if s.o.Kiosk {
			// Im Kiosk bleibt Edge nach einem Neustart sichtbar und vorn; Admin-Geste und „Zurück“ laufen über
			// dieselbe Überwachung.
			sup.SetEdgeKiosk(web.LocalAdminURL())
		}
		if f := restartHook(s.o.Kiosk, func(n int) {
			log.Printf("Edge neu gestartet (%d), Fenster wird minimiert", n)
			go minimizeWhenShown(ctx, win)
		}); f != nil {
			sup.SetOnRestart(f)
		}
		sup.Show(browser.TargetStart)
		s.sup.Store(sup)
		sup.Run(ctx)
	}()
	return done
}

// restartHook: Im Kiosk gibt es kein Minimieren nach einem Neustart (nil), sonst minimize.
func restartHook(kiosk bool, minimize func(n int)) func(n int) {
	if kiosk {
		return nil
	}
	return minimize
}

// waitForEdge sucht Edge, bis es da ist oder ctx endet; die erste Meldung steht im Protokoll.
func waitForEdge(ctx context.Context, every time.Duration, read func(string) (string, error)) (string, bool) {
	return waitForEdgeNotify(ctx, every, read, func(bool) {})
}

// jobPIDs: Prozesskennungen des Edge-Jobs; ohne laufenden Prozess keine.
func jobPIDs(p any) ([]uint32, error) {
	if l, ok := p.(interface{ ProcessIDs() ([]uint32, error) }); ok {
		return l.ProcessIDs()
	}
	return nil, nil
}

// minimizeWhenShown wartet höchstens windowWaitMax auf das neue Fenster und minimiert es.
func minimizeWhenShown(ctx context.Context, w window) {
	deadline := time.Now().Add(windowWaitMax)
	for time.Now().Before(deadline) {
		if err := w.MinimizeNoActivate(); err == nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(windowWaitStep):
		}
	}
	log.Print("Edge-Fenster nicht gefunden, bleibt sichtbar")
}

// windowForeground: Liegt das Browser-Fenster vorn? Ohne Edge-Fenster unter Windows nein (die Anmeldung
// soll trotzdem gemeldet werden), ohne Fenster am Pi (Kiosk) immer ja.
func (s *service) windowForeground() bool {
	if w := s.win.Load(); w != nil {
		return (*w).Foreground()
	}
	return !onWindows
}

// raiseWindow holt das Edge-Fenster nach vorn; ohne Fenster nichts.
func (s *service) raiseWindow() error {
	if w := s.win.Load(); w != nil {
		return (*w).Raise()
	}
	return nil
}

// waitForEdgeNotify wie waitForEdge; missing(true) meldet das erste Fehlen, missing(false) den Fund danach.
func waitForEdgeNotify(ctx context.Context, every time.Duration, read func(string) (string, error), missing func(bool)) (string, bool) {
	warned := false
	for {
		p, err := browser.FindEdge(read)
		if err == nil {
			if warned {
				missing(false)
			}
			return p, true
		}
		if !warned {
			log.Printf("Warnung: %v, das Terminal läuft ohne Anzeige, neuer Versuch alle %v", err, every)
			missing(true)
			warned = true
		}
		select {
		case <-ctx.Done():
			return "", false
		case <-time.After(every):
		}
	}
}
