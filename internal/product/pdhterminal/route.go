package pdhterminal

import (
	"context"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// routeProbeEvery: Abstand, in dem die Hauptadresse geprüft wird, solange die Ausweichadresse aktiv ist.
var routeProbeEvery = 5 * time.Minute

// routeProbeTimeout: Zeitlimit je Prüfung der Hauptadresse.
var routeProbeTimeout = 10 * time.Second

// routeKickMin: Mindestabstand zwischen einer Prüfung und einer weiteren, die ein Netzfehler auf der aktiven
// Ausweichadresse sofort anstößt (sonst prüft nur der Takt routeProbeEvery).
var routeKickMin = 30 * time.Second

// IsNetworkErr: true bei Netzfehlern (Verbindung, DNS, Zeitüberschreitung), nie bei TLS-/Zertifikatsfehlern.
func IsNetworkErr(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || isTLSErr(err) {
		return false
	}
	// *url.Error erfüllt immer net.Error; deshalb nur den inneren Fehler auf Timeout prüfen.
	inner := err
	var ue *url.Error
	if errors.As(err, &ue) {
		inner = ue.Err
	}
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) ||
		(errors.As(inner, &ne) && ne.Timeout()) {
		return true
	}
	var (
		dnsE *net.DNSError
		opE  *net.OpError
	)
	if errors.As(err, &dnsE) || errors.As(err, &opE) {
		return true
	}
	// Gegenseite beendet die Verbindung vor oder im Handshake (z. B. Proxy, Absturz).
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

// RouteStatus beschreibt die aktive Adresse.
type RouteStatus struct {
	Name    string
	Since   time.Time
	LastErr string
}

// Route wählt zwischen Haupt- und Ausweichadresse.
type Route struct {
	mu       sync.Mutex
	primary  string
	fallback string
	useFB    bool
	since    time.Time
	lastErr  string
	now      func() time.Time
	gen      uint64
	kick     chan struct{} // Netzfehler auf der aktiven Ausweichadresse: Hauptadresse bald prüfen (Run)
}

// NewRoute legt eine Wegwahl an; aktiv ist zunächst die Hauptadresse.
func NewRoute(primary, fallback string) *Route {
	return &Route{
		primary:  strings.TrimRight(primary, "/"),
		fallback: strings.TrimRight(fallback, "/"),
		now:      time.Now,
		kick:     make(chan struct{}, 1),
	}
}

// SetURLs setzt die Adressen neu; bei leerer oder geänderter Adresse geht es zurück auf Haupt.
func (r *Route) SetURLs(primary, fallback string) {
	primary, fallback = strings.TrimRight(primary, "/"), strings.TrimRight(fallback, "/")
	r.mu.Lock()
	defer r.mu.Unlock()
	if fallback == "" || fallback != r.fallback || primary != r.primary {
		r.backLocked()
	}
	r.primary, r.fallback = primary, fallback
}

// Primary ist die Hauptadresse.
func (r *Route) Primary() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.primary
}

// Base ist die aktive Basisadresse.
func (r *Route) Base() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.useFB {
		return r.fallback
	}
	return r.primary
}

// Snapshot liefert Hauptadresse, aktive Basisadresse und Generation unter einem Lock, damit eine Anfrage Ziel und
// Wegangabe aus derselben Lage nimmt.
func (r *Route) Snapshot() (primary, base string, gen uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	base = r.primary
	if r.useFB {
		base = r.fallback
	}
	return r.primary, base, r.gen
}

// Name ist "lan" oder "tailnet".
func (r *Route) Name() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.nameLocked()
}

func (r *Route) nameLocked() string {
	if r.useFB {
		return "tailnet"
	}
	return "lan"
}

// backLocked wechselt auf die Hauptadresse zurück und setzt den Status zurück.
func (r *Route) backLocked() {
	if !r.useFB {
		return
	}
	r.gen++
	r.useFB = false
	r.since = r.now()
	r.lastErr = ""
}

// Fail meldet einen Fehler auf base; true, wenn auf die Ausweichadresse gewechselt wurde. Ein Netzfehler auf der
// aktiven Ausweichadresse schaltet nie um, stößt aber eine sofortige Prüfung der Hauptadresse an (Run, gedrosselt
// durch routeKickMin): Ist auch der Rückfall tot, hängt das Terminal so nicht bis zum nächsten Takt dort fest.
func (r *Route) Fail(base string, err error) (switched bool) {
	base = strings.TrimRight(base, "/")
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fallback == "" || !IsNetworkErr(err) {
		return false
	}
	if r.useFB {
		if base == r.fallback {
			select {
			case r.kick <- struct{}{}:
			default: // schon angestoßen
			}
		}
		return false
	}
	if base != r.primary {
		return false
	}
	r.useFB = true
	r.gen++
	r.since = r.now()
	r.lastErr = cleanText(err.Error())
	return true
}

// Run prüft bei aktiver Ausweichadresse regelmäßig die Hauptadresse und wechselt bei Erfolg zurück. Meldet Fail
// einen Netzfehler auf der Ausweichadresse, prüft Run sofort, höchstens aber einmal je routeKickMin.
func (r *Route) Run(ctx context.Context, probe func(ctx context.Context, base string) error) {
	every := routeProbeEvery
	if every <= 0 {
		every = 5 * time.Minute
	}
	t := time.NewTicker(every)
	defer t.Stop()
	var last time.Time // Beginn der letzten Prüfung (Takt oder Anstoß)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-r.kick:
			if !last.IsZero() && time.Since(last) < routeKickMin {
				continue // gedrosselt; der nächste Fehler auf der Ausweichadresse stößt wieder an
			}
		}
		r.mu.Lock()
		active, primary, gen := r.useFB, r.primary, r.gen
		r.mu.Unlock()
		if !active {
			continue
		}
		last = time.Now()
		pctx, cancel := context.WithTimeout(ctx, routeProbeTimeout)
		err := probe(pctx, primary)
		cancel()
		if err != nil {
			continue
		}
		r.mu.Lock()
		if r.useFB && r.primary == primary && r.gen == gen {
			r.backLocked()
		}
		r.mu.Unlock()
	}
}

// Status liefert den aktuellen Weg.
func (r *Route) Status() RouteStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	return RouteStatus{Name: r.nameLocked(), Since: r.since, LastErr: r.lastErr}
}
