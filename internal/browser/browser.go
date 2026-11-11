// Package browser überwacht Chromium: Kachelseite oder schmale Leiste, nie beides.
package browser

import (
	"context"
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
	s.mu.Unlock()
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
func (s *Supervisor) Run(ctx context.Context) {
	var (
		cur       Target
		proc      client.Process
		done      <-chan error
		startedAt time.Time
		backoff   = backoffMin
		retry     <-chan time.Time
	)
	stop := func() {
		if proc != nil {
			_ = proc.Stop(stopGrace)
		}
		proc, done, retry = nil, nil, nil
	}
	scheduleRetry := func() {
		retry = s.clock.After(backoff)
		backoff = min(backoff*2, backoffMax)
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
			proc, done = nil, nil
			if s.clock.Now().Sub(startedAt) >= stableAfter {
				backoff = backoffMin
			}
			s.logf("Chromium beendet (%v), Neustart in %v", err, backoff)
			scheduleRetry()
		case <-retry:
			retry = nil
			launch()
		}
	}
}
