package pdhterminal

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"log"
	"math"
	"net"
	"strings"
	"time"

	"hmikiosk/internal/browser"
)

// Rückgabewerte des Programms. Der Starter (Task 8) wertet sie aus: 3 heißt, der Benutzer hat über das
// Tray-Symbol beendet (nicht neu starten), 4 heißt, ein Update bittet um einen Neustart.
const (
	ExitQuit    = 3
	ExitRestart = 4
)

var (
	// ErrQuit liefert Run, wenn der Benutzer im Tray-Menü „Beenden“ gewählt hat.
	ErrQuit = errors.New("vom Benutzer beendet")
	// ErrRestart liefert Run, wenn das Programm mit neuem Stand neu gestartet werden soll.
	ErrRestart = errors.New("Neustart angefordert")
)

// trayActions sind die Menüpunkte des Tray-Symbols. Die Rückrufe laufen nie im Fensterthread des
// Symbols und dürfen blockieren. Quit wird erst nach der Rückfrage gerufen.
type trayActions struct {
	Open, Admin, Selftest, Quit func()
}

// tray ist das Symbol im Infobereich der Taskleiste (Windows); an anderen Plattformen gibt es keines.
type tray interface {
	// Start legt das Symbol an (grün). Fehler heißt: kein Symbol, das Terminal läuft trotzdem.
	Start(a trayActions) error
	// SetState färbt das Symbol: leerer Grund grün, sonst rot mit dem Grund als Tooltip. Blockiert nie.
	SetState(reason string)
	// Stop entfernt das Symbol; mehrfaches Rufen ist harmlos.
	Stop()
}

const adminPathSelftest = "/admin/system"

// Problemarten für den Tooltip.
const (
	problemEdge  = "edge"
	problemAdmin = "port-admin"
	problemLocal = "port-local"
)

// setProblem merkt einen Grund für die rote Färbung (leer: erledigt) und färbt das Symbol neu.
func (s *service) setProblem(kind, msg string) {
	s.mu.Lock()
	if s.problems == nil {
		s.problems = map[string]string{}
	}
	if msg == "" {
		delete(s.problems, kind)
	} else {
		s.problems[kind] = msg
	}
	s.mu.Unlock()
	s.refreshTray()
}

// trayReason fasst die Gründe für die rote Färbung zusammen; leer heißt: alles in Ordnung.
func (s *service) trayReason() string {
	s.mu.RLock()
	parts := make([]string, 0, 4)
	for _, k := range []string{problemEdge, problemLocal, problemAdmin} {
		if m := s.problems[k]; m != "" {
			parts = append(parts, m)
		}
	}
	s.mu.RUnlock()
	if s.cards.ReaderState() == "error" {
		parts = append(parts, "Kartenleser getrennt")
	}
	return strings.Join(parts, "; ")
}

func (s *service) refreshTray() {
	if t := s.tray.Load(); t != nil {
		(*t).SetState(s.trayReason())
	}
}

// startTray legt das Symbol an, falls gewünscht und möglich, und liefert die Funktion, die es entfernt.
func (s *service) startTray(ctx context.Context, bound Bound, background func(func())) (stop func()) {
	t := s.o.tray
	if t == nil && s.o.TrayEnabled {
		t = newTray()
	}
	if t == nil {
		return func() {}
	}
	err := t.Start(trayActions{
		Open: func() { s.openTerminal(ctx) },
		Admin: func() {
			if err := openURL(adminPageURL(bound.Admin, "/admin/")); err != nil {
				log.Printf("Admin-Seite nicht geöffnet: %v", err)
			}
		},
		Selftest: func() {
			if err := openURL(adminPageURL(bound.Admin, adminPathSelftest)); err != nil {
				log.Printf("Selbstprüfung nicht geöffnet: %v", err)
			}
		},
		Quit: func() { s.requestExit(ErrQuit) },
	})
	if err != nil {
		log.Printf("Tray-Symbol nicht verfügbar: %v", err)
		return func() {}
	}
	s.tray.Store(&t)
	t.SetState(s.trayReason())
	// Der Zustand des Kartenlesers wechselt von selbst.
	changed, unwatch := s.cards.WatchState()
	background(func() {
		defer unwatch()
		for {
			select {
			case <-ctx.Done():
				return
			case <-changed:
				s.refreshTray()
			}
		}
	})
	return func() {
		s.tray.Store(nil)
		t.Stop()
	}
}

// adminPageURL ist die Adresse einer Admin-Seite auf diesem Gerät; ohne gebundene Adresse gilt Port 8443.
func adminPageURL(bound, path string) string {
	port := "8443"
	if _, p, err := net.SplitHostPort(bound); err == nil && p != "" {
		port = p
	}
	return "https://127.0.0.1:" + port + path
}

// requestExit bittet serve, mit err zu enden; die erste Bitte gilt.
func (s *service) requestExit(err error) {
	select {
	case s.exit <- err:
	default:
	}
}

// openTerminal holt das Edge-Fenster nach vorn. Ist es gerade weg (der Überwacher startet Edge nach dem
// Schließen sofort neu), wird bis zu 10 s weiter versucht.
func (s *service) openTerminal(ctx context.Context) {
	go func() {
		if sup := s.sup.Load(); sup != nil {
			sup.Show(browser.TargetStart) // Der Überwacher startet Edge, falls es fehlt.
		}
		var last error
		for range 20 {
			if s.win.Load() != nil {
				if last = s.raiseWindow(); last == nil {
					return
				}
			} else {
				last = errors.New("kein Browser-Fenster")
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(500 * time.Millisecond):
			}
		}
		log.Printf("Terminal öffnen: %v", last)
	}()
}

// Symbolfarben: grün im Betrieb, rot bei Warnung; weiß das „P“.
var (
	trayGreen = color.NRGBA{0x2e, 0x9e, 0x4f, 0xff}
	trayRed   = color.NRGBA{0xd0, 0x3a, 0x3a, 0xff}
)

// renderTrayIcon zeichnet das Symbol: Kreis in col mit weißem „P“, size×size Pixel, geglättet
// (4×4 Abtastung). Es entsteht im Programm; es gibt keine Bilddatei zu pflegen.
func renderTrayIcon(size int, col color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	const ss = 4
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var disc, glyph int
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					u := (float64(x) + (float64(sx)+0.5)/ss) / float64(size)
					v := (float64(y) + (float64(sy)+0.5)/ss) / float64(size)
					if math.Hypot(u-0.5, v-0.5) <= 0.48 {
						disc++
						if inGlyphP(u, v) {
							glyph++
						}
					}
				}
			}
			if disc == 0 {
				continue
			}
			w := float64(glyph) / float64(disc) // Anteil Weiß in den Punkten des Kreises
			mix := func(c uint8) uint8 { return uint8(math.Round(float64(c)*(1-w) + 255*w)) }
			img.SetNRGBA(x, y, color.NRGBA{mix(col.R), mix(col.G), mix(col.B), uint8(255 * disc / (ss * ss))})
		}
	}
	return img
}

// inGlyphP: Punkt (u, v) im Einheitsquadrat liegt im Buchstaben „P“ (Stamm, Balken, Bogen).
func inGlyphP(u, v float64) bool {
	if u >= 0.34 && u <= 0.45 && v >= 0.24 && v <= 0.78 { // Stamm
		return true
	}
	if u >= 0.34 && u <= 0.56 && ((v >= 0.24 && v <= 0.34) || (v >= 0.47 && v <= 0.57)) { // Balken oben und Mitte
		return true
	}
	d := math.Hypot(u-0.56, v-0.405) // Bogen rechts
	return u >= 0.56 && d >= 0.065 && d <= 0.165
}

// trayTip ist der Tooltip: der Grund der Warnung oder nur der Name.
func trayTip(reason string) string {
	if reason == "" {
		return "PDH-Terminal"
	}
	return fmt.Sprintf("PDH-Terminal: %s", reason)
}
