package app

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"hmikiosk/internal/config"
)

const (
	reachEvery       = 10 * time.Second
	reachLimit       = 2 * time.Second
	reachConcurrency = 8
	resolveLimit     = 2 * time.Second
)

// hostCache trennt die Namensauflösung von der TCP-Abfrage: tcp läuft alle 500 ms
// in der Zustands-Goroutine und darf nie auf DNS warten. resolve läuft einmal je
// Versuch außerhalb davon.
type hostCache struct {
	lookup      func(ctx context.Context, host string) ([]net.IP, error)
	established func(ip net.IP, port int) (bool, error)

	mu  sync.Mutex
	ips map[string][]net.IP
}

func newHostCache(lookup func(context.Context, string) ([]net.IP, error), established func(net.IP, int) (bool, error)) *hostCache {
	return &hostCache{lookup: lookup, established: established, ips: map[string][]net.IP{}}
}

func (c *hostCache) resolve(ctx context.Context, host string) error {
	if net.ParseIP(host) != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, resolveLimit)
	defer cancel()
	ips, err := c.lookup(ctx, host)
	if err == nil && len(ips) == 0 {
		err = errors.New("keine Adresse")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		delete(c.ips, host)
		return err
	}
	c.ips[host] = ips
	return nil
}

// tcp meldet eine ESTABLISHED-Verbindung; ohne bekannte Adresse gibt es keine.
func (c *hostCache) tcp(host string, port int) bool {
	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else {
		c.mu.Lock()
		ips = c.ips[host]
		c.mu.Unlock()
	}
	for _, ip := range ips {
		if ok, err := c.established(ip, port); err == nil && ok {
			return true
		}
	}
	return false
}

// reachability prüft alle HMIs für die Kacheln.
type reachability struct {
	hmis      func() []config.HMI
	inSession func() string
	check     func(ctx context.Context, host string, port int) bool
	nudge     chan struct{}

	mu    sync.Mutex
	state map[string]bool
}

func newReachability(hmis func() []config.HMI, inSession func() string, check func(context.Context, string, int) bool) *reachability {
	return &reachability{
		hmis: hmis, inSession: inSession, check: check,
		nudge: make(chan struct{}, 1),
		state: map[string]bool{},
	}
}

func (r *reachability) reachable(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state[id]
}

// wake prüft sofort, etwa nach einer Änderung der HMI-Liste.
func (r *reachability) wake() {
	select {
	case r.nudge <- struct{}{}:
	default:
	}
}

func (r *reachability) run(ctx context.Context) {
	t := time.NewTicker(reachEvery)
	defer t.Stop()
	for {
		r.round(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-r.nudge:
		}
	}
}

func (r *reachability) round(ctx context.Context) {
	hmis := r.hmis()
	known := make(map[string]bool, len(hmis))
	sem := make(chan struct{}, reachConcurrency)
	var wg sync.WaitGroup
	for _, h := range hmis {
		known[h.ID] = true
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return
		}
		// Erst hier fragen, denn die Warteschlange kann dauern. Es gibt keinen
		// Prozessfilter: Eine Prüfung während der Sitzung sähe für einen Moment
		// wie die ESTABLISHED-Verbindung des Clients aus.
		if h.ID == r.inSession() {
			<-sem
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			cctx, cancel := context.WithTimeout(ctx, reachLimit)
			defer cancel()
			ok := r.check(cctx, h.Host, h.Port)
			r.mu.Lock()
			r.state[h.ID] = ok
			r.mu.Unlock()
		}()
	}
	wg.Wait()
	r.mu.Lock()
	defer r.mu.Unlock()
	for id := range r.state {
		if !known[id] {
			delete(r.state, id)
		}
	}
}
