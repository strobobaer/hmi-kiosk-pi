// Package keyboard überwacht die Bildschirmtastatur wvkbd und blendet sie ein und aus.
//
// wvkbd läuft unter labwc (Wayland, layer-shell) verborgen und wird per Signal gesteuert
// (Handbuchseite wvkbd(1): SIGUSR2 zeigen, SIGUSR1 verbergen). Unter X11 gibt es keine
// Bildschirmtastatur; dann meldet jeder Aufruf ErrUnavailable und es läuft nichts.
package keyboard

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"hmikiosk/internal/client"
	"hmikiosk/internal/session"
)

// Program ist das Programm aus dem Debian-Paket wvkbd.
const Program = "wvkbd-mobintl"

const (
	stopGrace   = 2 * time.Second
	backoffMin  = time.Second
	backoffMax  = 30 * time.Second
	stableAfter = 60 * time.Second
	// startGrace: So lange nach dem Start schickt der Steuerer keine Signale. wvkbd richtet
	// seine Signalbehandlung erst beim Start ein; vorher beendete SIGUSR1/2 das Programm.
	// Wünsche aus dieser Zeit gelten danach (der letzte gewinnt).
	startGrace    = 300 * time.Millisecond
	defaultHeight = 360 // ein Drittel von 1080
	// layers: wvkbd hat kein deutsches Layout. „full“ ist QWERTY mit Ziffernreihe, Umlaute
	// und ß über die Taste „Cmp“ und den Grundbuchstaben (a, o, u, s); „special“ für Zeichen.
	layers = "full,special"
)

// ErrUnavailable: Es gibt keine Bildschirmtastatur (X11, kein wvkbd, nicht gestartet).
var ErrUnavailable = errors.New("Bildschirmtastatur nicht verfügbar")

// Controller ist das, was Seiten und Leiste brauchen; die Web-Kacheln nutzen dasselbe.
type Controller interface {
	Show() error
	Hide() error
	Toggle() error
}

// signaler ist ein Prozess, dem man Signale schicken kann (client.execProcess unter Linux).
type signaler interface {
	Signal(os.Signal) error
}

// Find sucht wvkbd im Pfad.
func Find(lookPath func(string) (string, error)) (string, error) {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	p, err := lookPath(Program)
	if err != nil {
		return "", fmt.Errorf("%s nicht gefunden", Program)
	}
	return p, nil
}

// Supervisor startet wvkbd, startet es nach einem Absturz neu und schickt die Signale.
type Supervisor struct {
	runner  client.Runner
	find    func() (string, error)
	screenH func() int
	clock   session.Clock
	logf    func(string, ...any)

	mu      sync.Mutex
	enabled bool
	proc    client.Process
	visible bool // zuletzt gesendet
	want    bool // gewünscht; weicht nur in der Anlaufzeit von visible ab
	ready   bool // Anlaufzeit vorbei
	wake    chan struct{}
}

func (k *Supervisor) isReady() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.ready
}

// New baut den Steuerer. find sucht das Programm bei jedem Start (nil: Find im Pfad),
// screenH liefert die Bildschirmhöhe (0: unbekannt).
func New(r client.Runner, find func() (string, error), screenH func() int) *Supervisor {
	if find == nil {
		find = func() (string, error) { return Find(nil) }
	}
	return &Supervisor{
		runner: r, find: find, screenH: screenH,
		clock: session.SystemClock{}, logf: log.Printf,
		wake: make(chan struct{}, 1),
	}
}

// SetEnabled schaltet die Tastatur ein (labwc) oder aus (X11). Blockiert nie.
func (k *Supervisor) SetEnabled(on bool) {
	k.mu.Lock()
	k.enabled = on
	k.mu.Unlock()
	select {
	case k.wake <- struct{}{}:
	default:
	}
}

func height(screen int) int {
	if screen <= 0 {
		return defaultHeight
	}
	return screen / 3
}

func (k *Supervisor) args() []string {
	h := 0
	if k.screenH != nil {
		h = k.screenH()
	}
	px := strconv.Itoa(height(h))
	return []string{"--hidden", "-H", px, "-L", px, "-l", layers, "--landscape-layers", layers}
}

func (k *Supervisor) running() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.proc != nil
}

// Visible meldet, ob die Tastatur nach dem letzten Signal sichtbar ist.
func (k *Supervisor) Visible() bool {
	if k == nil {
		return false
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.visible
}

func (k *Supervisor) Show() error { return k.set(func(bool) bool { return true }) }
func (k *Supervisor) Hide() error { return k.set(func(bool) bool { return false }) }

// Toggle wechselt nach dem zuletzt gewünschten Zustand.
func (k *Supervisor) Toggle() error { return k.set(func(v bool) bool { return !v }) }

func (k *Supervisor) set(next func(bool) bool) error {
	if k == nil || sigShow == nil {
		return ErrUnavailable
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if !k.enabled {
		return ErrUnavailable
	}
	if k.proc == nil {
		return fmt.Errorf("%w: wvkbd läuft gerade nicht", ErrUnavailable)
	}
	sp, ok := k.proc.(signaler)
	if !ok {
		return fmt.Errorf("%w: wvkbd lässt sich nicht steuern", ErrUnavailable)
	}
	k.want = next(k.want)
	if !k.ready {
		return nil // gilt nach der Anlaufzeit
	}
	return k.applyLocked(sp)
}

// applyLocked sendet den gewünschten Zustand, wenn er vom gesendeten abweicht. Der
// Aufrufer hält k.mu.
func (k *Supervisor) applyLocked(sp signaler) error {
	if k.want == k.visible {
		return nil
	}
	sig := sigHide
	if k.want {
		sig = sigShow
	}
	if err := sp.Signal(sig); err != nil {
		k.want = k.visible
		return fmt.Errorf("Bildschirmtastatur: %w", err)
	}
	k.visible = k.want
	return nil
}

// Run besitzt den Prozess; es endet mit ctx.
func (k *Supervisor) Run(ctx context.Context) {
	var (
		done      <-chan error
		startedAt time.Time
		backoff   = backoffMin
		retry     <-chan time.Time
		grace     <-chan time.Time
		active    bool
	)
	setProc := func(p client.Process) {
		k.mu.Lock()
		k.proc, k.visible, k.want, k.ready = p, false, false, false
		k.mu.Unlock()
	}
	stop := func() {
		k.mu.Lock()
		p := k.proc
		k.mu.Unlock()
		if p != nil {
			_ = p.Stop(stopGrace)
		}
		setProc(nil)
		done, retry, grace = nil, nil, nil
	}
	launch := func() {
		at := k.clock.Now()
		var p client.Process
		path, err := k.find()
		if err == nil {
			p, err = k.runner.Start(client.Command{Path: path, Args: k.args()})
		}
		if err != nil {
			k.logf("Bildschirmtastatur startet nicht: %v (neuer Versuch in %v)", err, backoff)
			retry = k.clock.After(backoff)
			backoff = min(backoff*2, backoffMax)
			return
		}
		setProc(p)
		done, startedAt, retry = p.Done(), at, nil
		grace = k.clock.After(startGrace)
	}
	for {
		select {
		case <-ctx.Done():
			stop()
			return
		case <-k.wake:
			k.mu.Lock()
			want := k.enabled
			k.mu.Unlock()
			if want == active {
				continue
			}
			stop()
			active, backoff = want, backoffMin
			if active {
				launch()
			}
		case <-grace:
			grace = nil
			k.mu.Lock()
			k.ready = true
			if sp, ok := k.proc.(signaler); ok {
				if err := k.applyLocked(sp); err != nil {
					k.logf("%v", err)
				}
			}
			k.mu.Unlock()
		case err := <-done:
			setProc(nil)
			done, grace = nil, nil
			if k.clock.Now().Sub(startedAt) >= stableAfter {
				backoff = backoffMin
			}
			k.logf("Bildschirmtastatur beendet (%v), Neustart in %v", err, backoff)
			retry = k.clock.After(backoff)
			backoff = min(backoff*2, backoffMax)
		case <-retry:
			retry = nil
			launch()
		}
	}
}
