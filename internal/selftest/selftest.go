// Package selftest prüft am Gerät, was sich ohne Hardware nicht testen lässt:
// Client, Fensterplatz, Touch, Rückkehr, Prozessende, Abstimmung und Schreibschutz
// (Selbstprüfung, Spec Abschnitt 12) sowie das Verhalten über viele Sitzungen
// (Dauerprüfung). Alles läuft gegen den eingebauten Test-VNC-Server auf 127.0.0.1.
package selftest

import (
	"context"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"hmikiosk/internal/client"
	"hmikiosk/internal/config"
	"hmikiosk/internal/coord"
	"hmikiosk/internal/session"
)

// Feste Namen der Schritte in der Reihenfolge der Spec.
const (
	StepClient     = "client-bild"
	StepWindow     = "fenster-leiste"
	StepTouch      = "touch"
	StepReturn     = "rueckkehr"
	StepProcessEnd = "prozessende"
	StepTerminals  = "terminals"
	StepProtection = "schutz"

	// StepPrecondition meldet, dass die Prüfung gar nicht erst läuft.
	StepPrecondition = "voraussetzung"
)

var StepNames = []string{StepClient, StepWindow, StepTouch, StepReturn, StepProcessEnd, StepTerminals, StepProtection}

// StepResult: OK=false heißt Fehler oder nicht prüfbar. OK=true mit NeedsHuman
// heißt: nichts Falsches gemessen, aber ein Mensch muss etwas bestätigen.
type StepResult struct {
	Name       string `json:"name"`
	OK         bool   `json:"ok"`
	Detail     string `json:"detail"`
	NeedsHuman bool   `json:"needsHuman"`
}

// Timing sind die Fristen; Nullwerte gelten als Standard. Die Fristen für Stopp und
// Verlust sind die der Sitzung (session.StopGrace, session.LostAfter).
type Timing struct {
	Poll          time.Duration // Abfrage von Verbindung und Prozess
	Connect       time.Duration // bis der Client verbunden ist
	ViewHold      time.Duration // das Bild bleibt stehen, damit ein Mensch hinsieht
	WrongPassword time.Duration // Beobachtung des Clients mit falschem Passwort
	TouchWait     time.Duration
	PointerWait   time.Duration
	StopWithin    time.Duration // Prozess nach Zurück oder Absturz weg
	DropWithin    time.Duration // Prozess nach Trennung durch den Server weg
	ExitWithin    time.Duration // Prozess endet nach Trennung von selbst
	LostAfter     time.Duration
	StopGrace     time.Duration
	Window        time.Duration // bis das Clientfenster messbar ist
	SoakHold      time.Duration
	Now           func() time.Time
}

func (t Timing) withDefaults() Timing {
	def := func(v *time.Duration, d time.Duration) {
		if *v <= 0 {
			*v = d
		}
	}
	def(&t.Poll, 200*time.Millisecond)
	def(&t.Connect, 20*time.Second)
	def(&t.ViewHold, 8*time.Second)
	def(&t.WrongPassword, 3*time.Second)
	def(&t.TouchWait, 60*time.Second)
	def(&t.PointerWait, 60*time.Second)
	def(&t.StopWithin, 5*time.Second)
	def(&t.DropWithin, 15*time.Second)
	def(&t.ExitWithin, 10*time.Second)
	def(&t.LostAfter, session.LostAfter)
	def(&t.StopGrace, session.StopGrace)
	def(&t.Window, 5*time.Second)
	def(&t.SoakHold, 30*time.Second)
	if t.Now == nil {
		t.Now = time.Now
	}
	return t
}

// Deps sind alle Abhängigkeiten, damit Tests ohne Gerät auskommen. Fehlt eine, die
// ein Schritt braucht, scheitert dieser Schritt mit dem Grund.
type Deps struct {
	Runner  client.Runner
	Profile func() client.Profile
	Config  func() config.Config
	Screen  func() (w, h int)
	// Established ist dieselbe Prüfung wie in der Sitzung (platform.HasEstablished).
	Established func(ip net.IP, port int) (bool, error)
	// RAMDir legt einen Ordner im RAM für ein Client-Profil an.
	RAMDir func() (dir string, cleanup func(), err error)
	// Peer prüft ein anderes Terminal (coord.CheckPeer).
	Peer func(ctx context.Context, p config.Peer) coord.PeerState
	// Mounts liefert /proc/mounts, BootConfig den Text von config.txt.
	Mounts     func() ([]byte, error)
	BootConfig func() (string, error)
	// Exec führt ein Programm aus und liefert die Standardausgabe.
	Exec func(ctx context.Context, name string, args ...string) (string, error)
	// Busy liefert eine Meldung, wenn eine echte Sitzung oder eine Daueranzeige läuft.
	Busy func() string
	// Show wählt, was die Anzeige des Terminals zeigt: "touch" die Touch-Seite,
	// "bar" die Sitzungsleiste, "" den Normalzustand.
	Show  func(page string)
	Touch *TouchBoard
	// Kill beendet einen Client hart wie ein Absturz; ohne Angabe SIGKILL auf die Gruppe.
	Kill func(client.Process) error
	// GroupRSS summiert den Speicher der Prozessgruppe von pid; ServicePID nennt den Dienst.
	GroupRSS   func(pid int) (int64, error)
	ServicePID func() int
	Timing     Timing
}

func (d Deps) show(page string) {
	if d.Show != nil {
		d.Show(page)
	}
}

func (d Deps) config() config.Config {
	if d.Config == nil {
		return config.Default()
	}
	return d.Config()
}

// Refuse liefert die Meldung, falls die Prüfung jetzt nicht laufen darf.
func Refuse(d Deps) string {
	if d.Busy == nil {
		return ""
	}
	return d.Busy()
}

type step struct {
	name string
	run  func(ctx context.Context, d Deps) StepResult
}

var steps = []step{
	{StepClient, clientImage},
	{StepWindow, windowAndBar},
	{StepTouch, touch},
	{StepReturn, returnToTiles},
	{StepProcessEnd, processEnd},
	{StepTerminals, terminals},
	{StepProtection, protection},
}

// Run führt alle sieben Schritte nacheinander aus. Ein gescheiterter Schritt
// bricht die anderen nicht ab. Läuft gerade eine echte Sitzung, startet nichts;
// das Ergebnis ist dann ein einzelner Eintrag StepPrecondition.
func Run(ctx context.Context, d Deps) []StepResult {
	if msg := Refuse(d); msg != "" {
		return []StepResult{{Name: StepPrecondition, Detail: msg}}
	}
	d.Timing = d.Timing.withDefaults()
	defer d.show("")
	out := make([]StepResult, 0, len(steps))
	for _, s := range steps {
		r := runStep(ctx, d, s)
		r.Name = s.name
		out = append(out, r)
	}
	return out
}

func runStep(ctx context.Context, d Deps, s step) (r StepResult) {
	if ctx.Err() != nil {
		return StepResult{Detail: "abgebrochen, nicht ausgeführt"}
	}
	defer func() {
		if p := recover(); p != nil {
			log.Printf("Selbstprüfung %s: Absturz: %v", s.name, p)
			r = StepResult{Detail: fmt.Sprintf("interner Fehler: %v", p)}
		}
	}()
	return s.run(ctx, d)
}

// Failed meldet, ob ein Schritt gescheitert ist (OK=false). "Prüfen" zählt nicht.
func Failed(rs []StepResult) bool {
	for _, r := range rs {
		if !r.OK {
			return true
		}
	}
	return false
}

// Label ist die Kennzeichnung für Mensch und Textausgabe.
func (r StepResult) Label() string {
	switch {
	case !r.OK:
		return "FEHLER"
	case r.NeedsHuman:
		return "PRÜFEN"
	}
	return "OK"
}

// Format ist die Textausgabe für Terminal und Download.
func Format(rs []StepResult) string {
	var b strings.Builder
	for _, r := range rs {
		fmt.Fprintf(&b, "[%s] %s", r.Label(), r.Name)
		if r.Detail != "" {
			fmt.Fprintf(&b, ": %s", r.Detail)
		}
		b.WriteString("\n")
	}
	switch {
	case Failed(rs):
		b.WriteString("Ergebnis: mindestens ein Schritt ist gescheitert.\n")
	case needsHuman(rs):
		b.WriteString("Ergebnis: nichts Falsches gemessen; die mit PRÜFEN gekennzeichneten Punkte muss ein Mensch bestätigen.\n")
	default:
		b.WriteString("Ergebnis: alle Schritte bestanden.\n")
	}
	return b.String()
}

func needsHuman(rs []StepResult) bool {
	for _, r := range rs {
		if r.NeedsHuman {
			return true
		}
	}
	return false
}

// TouchTargets ist die Zahl der Ziele auf der Touch-Seite.
const TouchTargets = 5

// TouchBoard sammelt die Treffer der Touch-Seite (web: POST /api/selftest/touch).
type TouchBoard struct {
	mu   sync.Mutex
	hits [TouchTargets]bool
}

func NewTouchBoard() *TouchBoard { return &TouchBoard{} }

// Hit merkt ein Ziel vor; ein ungültiges wird abgelehnt.
func (b *TouchBoard) Hit(i int) bool {
	if i < 0 || i >= TouchTargets {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.hits[i] = true
	return true
}

func (b *TouchBoard) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.hits = [TouchTargets]bool{}
}

func (b *TouchBoard) Count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, h := range b.hits {
		if h {
			n++
		}
	}
	return n
}

func (b *TouchBoard) missing() []int {
	b.mu.Lock()
	defer b.mu.Unlock()
	var m []int
	for i, h := range b.hits {
		if !h {
			m = append(m, i)
		}
	}
	return m
}

// pider ist der Teil von client.Process, den nur der echte Prozess hat.
type pider interface{ Pid() int }
