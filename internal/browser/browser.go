// Package browser überwacht Chromium: Kachelseite oder schmale Leiste, nie beides.
package browser

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"sync"
	"time"

	"hmikiosk/internal/client"
	"hmikiosk/internal/session"
)

type Target int

const (
	TargetNone Target = iota
	TargetTiles
	TargetBar
	TargetTouchTest // Touch-Seite der Selbstprüfung
	TargetStart     // Startseite des Produkts (SetStartURL), etwa PDH-Terminal
	TargetAdmin     // lokale Admin-Seite (127.0.0.1:8081)
)

const (
	stopGrace   = 3 * time.Second
	backoffMin  = time.Second
	backoffMax  = 30 * time.Second
	stableAfter = 60 * time.Second
	jobPoll     = time.Second      // Edge: so oft fragt der Überwacher nach Job und Fenster
	windowGrace = 30 * time.Second // Edge: so lange darf das erste Fenster auf sich warten lassen
	barHeight   = 48
	baseURL     = "http://127.0.0.1:8080/"
	adminURL    = "http://127.0.0.1:8081/admin/" // gleich web.LocalAdminURL; browser importiert web nicht
)

// TargetFor: Die Kachelseite endet erst, wenn die Verbindung steht. Ohne Leiste
// läuft nur die Daueranzeige, für die der Sitzungsautomat das festgelegt hat.
func TargetFor(s session.Snapshot) Target {
	switch s.State {
	case session.StateConnected, session.StateWarned, session.StateStopping:
		if s.HideBar {
			return TargetNone
		}
		return TargetBar
	}
	return TargetTiles
}

// FindChromium sucht zuerst chromium, dann chromium-browser.
func FindChromium(lookPath func(string) (string, error)) (string, error) {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	for _, n := range []string{"chromium", "chromium-browser"} {
		if p, err := lookPath(n); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("kein Chromium gefunden (chromium, chromium-browser)")
}

// Supervisor überwacht nur den Prozess: Navigiert Chromium im selben Fenster weiter (etwa
// über das Zahnrad zur Admin-Seite am Terminal, 127.0.0.1:8081), ist das kein Absturz und
// löst keinen Neustart aus. Ein Neustart (Absturz) zeigt wieder die Kachelseite.
type Supervisor struct {
	runner  client.Runner
	path    string
	ramDir  string
	screenW func() int
	clock   session.Clock
	logf    func(string, ...any)

	mu       sync.Mutex
	profile  string
	startURL string
	want     Target
	wake     chan struct{}

	edgeProfile string         // gesetzt: Edge statt Chromium (SetEdge)
	edgeKiosk   string         // gesetzt: Edge im Kiosk, die Adresse der Admin-Seite für TargetAdmin (SetEdgeKiosk)
	onRestart   func(n int)    // nach jedem Neustart (SetOnRestart)
	cur         client.Process // laufender Prozess (Process)
	windowCheck func() bool    // Edge-Fenster vorhanden (SetWindowCheck)
}

func New(r client.Runner, chromiumPath, ramDir string, screenW func() int) *Supervisor {
	return &Supervisor{
		runner: r, path: chromiumPath, ramDir: ramDir, screenW: screenW,
		clock: session.SystemClock{}, logf: log.Printf,
		wake: make(chan struct{}, 1),
	}
}

// SetProfile wählt die Grafikplattform: „labwc“ (Wayland) oder „x11“.
func (s *Supervisor) SetProfile(p string) {
	s.mu.Lock()
	s.profile = p
	s.mu.Unlock()
}

// SetStartURL legt die Adresse für TargetStart fest. Nur vor Run aufrufen; danach
// wirkt die Änderung erst beim nächsten Start von Chromium.
func (s *Supervisor) SetStartURL(u string) {
	s.mu.Lock()
	s.startURL = u
	s.mu.Unlock()
}

// Show blockiert nie; der letzte Aufruf gewinnt.
func (s *Supervisor) Show(t Target) {
	s.mu.Lock()
	s.want = t
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Supervisor) args(t Target) []string {
	s.mu.Lock()
	profile := s.profile
	startURL := s.startURL
	edgeProfile := s.edgeProfile
	edgeKiosk := s.edgeKiosk
	s.mu.Unlock()
	if edgeProfile != "" {
		if edgeKiosk != "" {
			if t == TargetAdmin {
				return EdgeKioskArgs(edgeKiosk, edgeProfile)
			}
			return EdgeKioskArgs(startURL, edgeProfile)
		}
		return EdgeArgs(startURL, edgeProfile)
	}
	var a []string
	switch profile {
	case "labwc":
		a = append(a, "--ozone-platform=wayland")
	case "x11":
		a = append(a, "--ozone-platform=x11")
	}
	a = append(a,
		"--no-first-run", "--no-default-browser-check", "--disable-session-crashed-bubble",
		"--password-store=basic", "--use-mock-keychain", "--disable-pinch",
		"--overscroll-history-navigation=0", "--disable-features=Translate",
		"--check-for-update-interval=31536000",
		"--disk-cache-dir="+s.ramDir+"/cache",
		"--noerrdialogs", "--disable-infobars",
	)
	switch t {
	case TargetTiles:
		a = append(a, "--kiosk", "--touch-events=enabled",
			"--user-data-dir="+s.ramDir+"/profile-tiles", baseURL)
	case TargetTouchTest:
		a = append(a, "--kiosk", "--touch-events=enabled",
			"--user-data-dir="+s.ramDir+"/profile-tiles", baseURL+"selftest/touch")
	case TargetStart:
		a = append(a, "--kiosk", "--touch-events=enabled",
			"--user-data-dir="+s.ramDir+"/profile-tiles", startURL)
	case TargetAdmin:
		a = append(a, "--kiosk", "--touch-events=enabled",
			"--user-data-dir="+s.ramDir+"/profile-tiles", adminURL)
	case TargetBar:
		a = append(a, "--app="+baseURL+"bar", "--window-position=0,0",
			fmt.Sprintf("--window-size=%d,%d", s.screenW(), barHeight),
			"--user-data-dir="+s.ramDir+"/profile-bar")
	}
	return a
}

func (s *Supervisor) start(t Target) (client.Process, error) {
	if s.path == "" {
		return nil, fmt.Errorf("kein Chromium gefunden")
	}
	return s.runner.Start(client.Command{Path: s.path, Args: s.args(t)})
}

// Run ist die einzige Goroutine, die Zustand und Prozess besitzt.
//
// Edge (Windows) endet nicht mit seinem Startprozess: Der reicht den Start oft weiter, und
// Edge bleibt nach dem Schließen des Fensters im Hintergrund. Hat der Prozess ActiveProcesses
// (Job-Objekt) oder gibt es SetWindowCheck, fragt Run darum jede Sekunde nach: Das Ende gilt, wenn
// der Job leer ist oder das Fenster weg ist (zwei Abfragen in Folge); der Job wird dann beendet.
func (s *Supervisor) Run(ctx context.Context) {
	s.mu.Lock()
	windowCheck := s.windowCheck
	s.mu.Unlock()
	var (
		cur       Target
		proc      client.Process
		done      <-chan error
		startedAt time.Time
		backoff   = backoffMin
		retry     <-chan time.Time
		watch     <-chan time.Time // Job und Fenster beobachten
		exitErr   error
		doneFired bool // der Startprozess ist beendet
		seen      bool // das Fenster war schon da
		missed    int  // aufeinanderfolgende Abfragen ohne Fenster
		restarts  int
		afterEnd  bool // der nächste erfolgreiche Start ist ein Neustart
	)
	setCur := func(p client.Process) {
		s.mu.Lock()
		s.cur = p
		s.mu.Unlock()
	}
	stop := func() {
		if proc != nil {
			_ = proc.Stop(stopGrace)
		}
		proc, done, retry, watch, afterEnd = nil, nil, nil, nil, false
		setCur(nil)
	}
	scheduleRetry := func() {
		retry = s.clock.After(backoff)
		backoff = min(backoff*2, backoffMax)
	}
	// ended: Der Prozess (bei Edge samt Job und Fenster) ist wirklich zu Ende.
	ended := func(err error) {
		if _, ok := proc.(jobCounter); ok || windowCheck != nil {
			_ = proc.Stop(stopGrace) // beendet übrig gebliebene Prozesse, gibt das Job-Objekt frei
		}
		proc, done, watch = nil, nil, nil
		setCur(nil)
		if s.clock.Now().Sub(startedAt) >= stableAfter {
			backoff = backoffMin
		}
		s.logf("Chromium beendet (%v), Neustart in %v", err, backoff)
		afterEnd = true
		scheduleRetry()
	}
	launch := func() {
		at := s.clock.Now()
		p, err := s.start(cur)
		if err != nil {
			s.logf("Chromium startet nicht: %v (neuer Versuch in %v)", err, backoff)
			scheduleRetry()
			return
		}
		proc, done, startedAt, retry = p, p.Done(), at, nil
		doneFired, seen, missed, exitErr = false, false, 0, nil
		if _, ok := p.(jobCounter); ok || windowCheck != nil {
			watch = s.clock.After(jobPoll)
		}
		setCur(p)
		if afterEnd {
			afterEnd = false
			restarts++
			s.mu.Lock()
			f := s.onRestart
			s.mu.Unlock()
			if f != nil {
				f(restarts)
			}
		}
	}
	// alive: Läuft Edge noch (Job nicht leer, Fenster da oder noch im Anlauf)?
	alive := func() bool {
		if jc, ok := proc.(jobCounter); ok {
			if n, err := jc.ActiveProcesses(); err == nil && n == 0 {
				return false
			}
		} else if doneFired {
			return false
		}
		if windowCheck == nil {
			return true
		}
		if windowCheck() {
			seen, missed = true, 0
			return true
		}
		missed++
		if seen {
			exitErr = errors.New("Fenster geschlossen")
			return missed < 2
		}
		if s.clock.Now().Sub(startedAt) > windowGrace {
			exitErr = errors.New("kein Fenster erschienen")
			return false
		}
		return true
	}
	for {
		select {
		case <-ctx.Done():
			stop()
			return
		case <-s.wake:
			s.mu.Lock()
			want := s.want
			s.mu.Unlock()
			if want == cur {
				continue
			}
			stop()
			cur, backoff = want, backoffMin
			if cur != TargetNone {
				launch()
			}
		case err := <-done:
			done, doneFired, exitErr = nil, true, err
			if watch != nil && alive() { // Job lebt weiter: Folgeprozess übernimmt
				continue
			}
			ended(err)
		case <-watch:
			watch = nil
			if alive() {
				watch = s.clock.After(jobPoll)
				continue
			}
			ended(exitErr)
		case <-retry:
			retry = nil
			launch()
		}
	}
}

// NewExec wie New, startet Chromium aber als gewöhnlichen Prozess (client.NewExecRunner).
// Programme, die den Client-Teil nicht kennen sollen (PDH-Terminal), brauchen so keinen Runner.
func NewExec(chromiumPath, ramDir string, screenW func() int) *Supervisor {
	return New(client.NewExecRunner(), chromiumPath, ramDir, screenW)
}

// SetEdge schaltet auf Microsoft Edge im App-Fenster um (EdgeArgs mit der Startadresse, eigenes
// Profil profileDir); die Anzeigeziele spielen dann keine Rolle. Nur vor Run aufrufen.
func (s *Supervisor) SetEdge(profileDir string) {
	s.mu.Lock()
	s.edgeProfile = profileDir
	s.mu.Unlock()
}

// SetEdgeKiosk schaltet Edge (nach SetEdge) auf den Vollbild-Kiosk um: EdgeKioskArgs statt EdgeArgs, und
// TargetAdmin öffnet adminURL (die lokale Admin-Seite) statt der Startseite. Nur vor Run aufrufen.
func (s *Supervisor) SetEdgeKiosk(adminURL string) {
	s.mu.Lock()
	s.edgeKiosk = adminURL
	s.mu.Unlock()
}

// SetOnRestart: f(n) läuft in Run nach dem n-ten erfolgreichen Neustart (ab 1; der erste Start
// zählt nicht), wenn der Prozess nach seinem Ende wieder lief. f darf nicht blockieren.
func (s *Supervisor) SetOnRestart(f func(n int)) {
	s.mu.Lock()
	s.onRestart = f
	s.mu.Unlock()
}

// SetWindowCheck: has meldet, ob das Fenster des Prozesses da ist. Ist es nach dem Erscheinen
// weg (Nutzer schloss Edge, das im Hintergrund weiterläuft), gilt Edge als beendet und wird neu
// gestartet. Nur vor Run aufrufen.
func (s *Supervisor) SetWindowCheck(has func() bool) {
	s.mu.Lock()
	s.windowCheck = has
	s.mu.Unlock()
}

// Process ist der laufende Prozess oder nil.
func (s *Supervisor) Process() client.Process {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur
}

// jobCounter hat der Windows-Runner: Zahl der Prozesse, die im Job noch laufen.
type jobCounter interface{ ActiveProcesses() (int, error) }
