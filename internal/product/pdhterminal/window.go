package pdhterminal

import (
	"context"
	"log"
	"os"
	"time"

	"hmikiosk/internal/browser"
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

// startEdge zeigt die Startseite im Edge-App-Fenster und startet Edge nach dem Schließen
// neu, dann minimiert und ohne Fokus. Der Kanal schließt, sobald Edge beendet ist. Fehlt Edge,
// meldet das Terminal es einmal (Selbstprüfung „edge“ rot) und versucht es alle 30 s neu.
func (s *service) startEdge(ctx context.Context, startURL string) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		path, ok := waitForEdge(ctx, edgeRetry, readRegistry)
		if !ok {
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
		sup.SetOnRestart(func(n int) {
			log.Printf("Edge neu gestartet (%d), Fenster wird minimiert", n)
			go minimizeWhenShown(ctx, win)
		})
		sup.Show(browser.TargetStart)
		s.sup.Store(sup)
		sup.Run(ctx)
	}()
	return done
}

// waitForEdge sucht Edge, bis es da ist oder ctx endet; die erste Meldung steht im Protokoll.
func waitForEdge(ctx context.Context, every time.Duration, read func(string) (string, error)) (string, bool) {
	warned := false
	for {
		p, err := browser.FindEdge(read)
		if err == nil {
			return p, true
		}
		if !warned {
			log.Printf("Warnung: %v, das Terminal läuft ohne Anzeige, neuer Versuch alle %v", err, every)
			warned = true
		}
		select {
		case <-ctx.Done():
			return "", false
		case <-time.After(every):
		}
	}
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
