package winstart

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"hmikiosk/internal/appcore"
	"hmikiosk/internal/update"
)

const (
	// DefaultHealthTimeout ist die Frist für die Gesundmeldung nach dem Start.
	DefaultHealthTimeout = 60 * time.Second
	// DefaultPoll ist der Takt, in dem die Gesundmarke gelesen wird.
	DefaultPoll = 500 * time.Millisecond

	// resetAfter: Ein Lauf über diese Dauer setzt die Neustartpausen zurück.
	resetAfter = 10 * time.Minute
	// maxStrikes: So viele Abstürze in Folge ohne je gesund gewesen zu sein machen eine Version schlecht.
	maxStrikes = 3
)

// backoff sind die Pausen vor einem Neustart nach einem Absturz; die letzte gilt weiter.
var backoff = []time.Duration{5 * time.Second, 30 * time.Second, 5 * time.Minute}

// Clock ist die Uhr des Überwachers (im Test eine falsche).
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// RealClock ist die Uhr des Systems.
func RealClock() Clock { return realClock{} }

// Proc ist ein laufendes Programm.
type Proc interface {
	// Done liefert genau einmal den Rückgabewert, wenn der Hauptprozess endet.
	Done() <-chan int
	// Kill beendet den ganzen Prozessbaum und kehrt zurück, wenn er beendet ist; es ist mehrfach aufrufbar.
	Kill()
}

// Config beschreibt, was Supervise wählt, startet und beobachtet.
type Config struct {
	// Choose wählt die zu startende Version (vor jedem Start neu: Ordner und Markierungen ändern sich).
	// isBad meldet Versionen, die dieser Starter als schlecht kennt, auch wenn die Markierung nicht schreibbar war;
	// Choose muss sie überspringen (MarkKnownBad).
	Choose func(isBad func(version string) bool) (Candidate, error)
	// Start startet die gewählte Version.
	Start func(Candidate) (Proc, error)
	// RunDir ist der Laufzeitordner mit der Gesundmarke (appcore.HealthyFile).
	RunDir string
	Clock  Clock
	// MarkBad kennzeichnet eine Version als schlecht; nil: MarkBad.
	MarkBad func(Candidate) error
	// OnHealthy wird gerufen, sobald eine gestartete Version sich gesund gemeldet hat (Aufräumen); darf nil sein.
	OnHealthy func(Candidate)

	HealthTimeout time.Duration // 0: DefaultHealthTimeout
	Poll          time.Duration // 0: DefaultPoll
}

// Supervise wählt, startet und überwacht das Programm, bis es mit ExitQuit endet oder ctx abläuft:
//   - Nach dem Start wartet es bis zu 60 s, dass die Gesundmarke die gestartete Version nennt. Bleibt das aus,
//     wird die Version als schlecht markiert, der Prozessbaum beendet und neu gewählt (die Grundversion
//     kann nicht markiert werden; für sie gilt die Pause wie nach einem Absturz).
//   - ExitQuit beendet den Starter, ExitRestart startet sofort neu (nach einem gesunden Lauf).
//   - Andere Enden führen zu Pausen von 5 s, 30 s, dann 5 min. Ein Lauf über 10 min setzt sie zurück.
//   - Eine Version, die dreimal in Folge endet, ohne je gesund gewesen zu sein, gilt als schlecht: Ein Absturz vor der
//     ersten Gesundmeldung zählt als „nicht innerhalb von 60 s gesund“.
//
// Bei ctx.Done wird der Prozessbaum beendet; Supervise liefert dann nil.
func Supervise(ctx context.Context, cfg Config) error {
	if cfg.Clock == nil {
		cfg.Clock = RealClock()
	}
	if cfg.MarkBad == nil {
		cfg.MarkBad = MarkBad
	}
	if cfg.HealthTimeout == 0 {
		cfg.HealthTimeout = DefaultHealthTimeout
	}
	if cfg.Poll == 0 {
		cfg.Poll = DefaultPoll
	}
	s := &supervisor{cfg: cfg, strikes: map[string]int{}, badMem: map[string]bool{}}
	for ctx.Err() == nil {
		if quit := s.cycle(ctx); quit {
			return nil
		}
	}
	return nil
}

type supervisor struct {
	cfg     Config
	idx     int             // nächste Pause in backoff
	strikes map[string]int  // Fehlstarts in Folge je Version
	badMem  map[string]bool // als schlecht erkannt, auch wenn die Markierung nicht schreibbar war
}

func (s *supervisor) isBad(v string) bool { return s.badMem[v] }

// pause wartet die nächste Neustartpause ab; false, wenn ctx vorher endete.
func (s *supervisor) pause(ctx context.Context) bool {
	d := backoff[min(s.idx, len(backoff)-1)]
	s.idx++
	select {
	case <-s.cfg.Clock.After(d):
		return true
	case <-ctx.Done():
		return false
	}
}

// bad markiert c als schlecht (die Grundversion nie) und meldet, ob sofort neu gewählt werden kann.
// Lässt sich die Markierung nicht schreiben, gilt die Version bis zum Ende des Starters trotzdem als schlecht,
// aber es wird nicht sofort neu gewählt: Es gilt die Pause wie nach einem Fehlstart.
func (s *supervisor) bad(c Candidate) bool {
	if c.Base {
		return false
	}
	delete(s.strikes, c.Version)
	s.badMem[c.Version] = true
	if err := s.cfg.MarkBad(c); err != nil {
		log.Printf("Version %s nicht als schlecht markiert (gilt bis zum Ende des Starters): %v", c.Version, err)
		return false
	}
	return true
}

// failed behandelt einen Fehlstart (Ende oder Absturz vor der Gesundmeldung, Startfehler).
func (s *supervisor) failed(ctx context.Context, c Candidate) {
	s.strikes[c.Version]++
	if s.strikes[c.Version] >= maxStrikes && s.bad(c) {
		log.Printf("Version %s endete dreimal ohne Gesundmeldung, sie gilt als schlecht", c.Version)
		return
	}
	s.pause(ctx)
}

// cycle wählt und startet einmal und wartet auf das Ende; true heißt: Supervise endet.
func (s *supervisor) cycle(ctx context.Context) (quit bool) {
	cfg := s.cfg
	c, err := cfg.Choose(s.isBad)
	if err != nil {
		log.Printf("Keine Version startbar: %v", err)
		s.pause(ctx)
		return false
	}
	// Eine alte Marke gälte sonst, bevor dieser Start gelungen ist.
	if err := os.Remove(filepath.Join(cfg.RunDir, appcore.HealthyFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("Alte Gesundmarke nicht entfernt: %v", err)
	}
	p, err := cfg.Start(c)
	if err != nil {
		log.Printf("Version %s nicht startbar: %v", c.Version, err)
		s.failed(ctx, c)
		return false
	}
	started := cfg.Clock.Now()
	log.Printf("Version %s gestartet", c.Version)

	healthy := false
	deadline := cfg.Clock.After(cfg.HealthTimeout)
	tick := cfg.Clock.After(cfg.Poll)
	code, exited := 0, false
	for !healthy && !exited {
		select {
		case <-ctx.Done():
			p.Kill()
			return true
		case code = <-p.Done():
			exited = true
			healthy = s.isHealthy(c) // es kann sich noch gemeldet haben, bevor es endete
		case <-tick:
			healthy = s.isHealthy(c)
			tick = cfg.Clock.After(cfg.Poll)
		case <-deadline:
			if s.isHealthy(c) {
				healthy = true
				break
			}
			log.Printf("Version %s meldet sich nicht gesund innerhalb von %v, sie wird beendet", c.Version, cfg.HealthTimeout)
			p.Kill()
			if !s.bad(c) {
				s.pause(ctx)
			}
			return false
		}
	}
	if healthy {
		delete(s.strikes, c.Version)
		if cfg.OnHealthy != nil {
			cfg.OnHealthy(c)
		}
	}
	if !exited {
		select {
		case <-ctx.Done():
			p.Kill()
			return true
		case code = <-p.Done():
		}
	}
	p.Kill() // übrig gebliebene Kindprozesse (Edge) räumen, bevor neu gestartet wird
	log.Printf("Version %s beendet mit Rückgabewert %d", c.Version, code)

	if healthy && cfg.Clock.Now().Sub(started) > resetAfter {
		s.idx = 0
	}
	switch {
	case code == ExitQuit:
		return true
	case code == ExitRestart && healthy:
		return false
	case !healthy:
		s.failed(ctx, c)
	default:
		s.pause(ctx)
	}
	return false
}

// isHealthy: Die Marke nennt die gestartete Version. Für die Grundversion genügt irgendeine gültige Version,
// weil der Starter ihre Nummer nur aus seiner eigenen kennt.
func (s *supervisor) isHealthy(c Candidate) bool {
	b, err := os.ReadFile(filepath.Join(s.cfg.RunDir, appcore.HealthyFile))
	if err != nil {
		return false
	}
	v := strings.TrimSpace(string(b))
	if c.Base {
		return update.ValidVersion(v)
	}
	return v == c.Version
}
