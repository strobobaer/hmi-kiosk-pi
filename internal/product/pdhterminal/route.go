package pdhterminal

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
)

// routeProbeEvery: Abstand, in dem die Hauptadresse geprüft wird, solange die Ausweichadresse aktiv ist.
var routeProbeEvery = 5 * time.Minute

// IsNetworkErr: true bei Netzfehlern (Verbindung, DNS, Zeitüberschreitung), nie bei TLS-/Zertifikatsfehlern.
func IsNetworkErr(err error) bool {
	if err == nil || isTLSErr(err) {
		return false
	}
	var ne net.Error
	return errors.As(err, &ne) ||
		errors.Is(err, os.ErrDeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET)
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
}

// NewRoute legt eine Wegwahl an; aktiv ist zunächst die Hauptadresse.
func NewRoute(primary, fallback string) *Route {
	return &Route{primary: strings.TrimRight(primary, "/"), fallback: strings.TrimRight(fallback, "/"), now: time.Now}
}

// SetURLs setzt die Adressen neu; bei leerer oder geänderter Adresse geht es zurück auf Haupt.
func (r *Route) SetURLs(primary, fallback string) {
	primary, fallback = strings.TrimRight(primary, "/"), strings.TrimRight(fallback, "/")
	r.mu.Lock()
	defer r.mu.Unlock()
	if fallback == "" || fallback != r.fallback || primary != r.primary {
		r.useFB = false
	}
	r.primary, r.fallback = primary, fallback
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

// Fail meldet einen Fehler auf base; true, wenn auf die Ausweichadresse gewechselt wurde.
func (r *Route) Fail(base string, err error) (switched bool) {
	base = strings.TrimRight(base, "/")
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.useFB || r.fallback == "" || base != r.primary || !IsNetworkErr(err) {
		return false
	}
	r.useFB = true
	r.since = r.now()
	r.lastErr = cleanText(err.Error())
	return true
}

// Run prüft bei aktiver Ausweichadresse regelmäßig die Hauptadresse und wechselt bei Erfolg zurück.
func (r *Route) Run(ctx context.Context, probe func(ctx context.Context, base string) error) {
	t := time.NewTicker(routeProbeEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		r.mu.Lock()
		active, primary := r.useFB, r.primary
		r.mu.Unlock()
		if !active || probe(ctx, primary) != nil {
			continue
		}
		r.mu.Lock()
		if r.primary == primary {
			r.useFB = false
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
