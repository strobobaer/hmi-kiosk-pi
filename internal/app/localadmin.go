package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"hmikiosk/internal/coord"
)

const (
	// DefaultLocalAdminAddr ist die Admin-Seite am Terminal: nur Loopback, HTTP.
	DefaultLocalAdminAddr = "127.0.0.1:8081"
	// localAdminIdle: So lange gilt die Admin-Seite am Terminal nach dem letzten Aufruf
	// als offen (wie die kurze Sitzung der Seite).
	localAdminIdle   = 5 * time.Minute
	localAdminNotice = "Die Admin-Seite ist am Terminal geöffnet"
)

// adminPresence merkt sich, ob Chromium am Terminal gerade die Admin-Seite zeigt.
// Solange startet keine Sitzung und keine Daueranzeige: Sie würde die Seite schließen.
type adminPresence struct {
	clock coord.Clock
	mu    sync.Mutex
	until time.Time
}

func (p *adminPresence) now() time.Time {
	if p.clock == nil {
		return time.Now()
	}
	return p.clock.Now()
}

func (p *adminPresence) touch() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.until = p.now().Add(localAdminIdle)
}

func (p *adminPresence) left() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.until = time.Time{}
}

func (p *adminPresence) active() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.until.IsZero() && p.now().Before(p.until)
}

// localAdminLeft: Die Kachelseite ist wieder da (Zurück, Inaktivität, Neustart von Chromium).
func (s *service) localAdminLeft() {
	if !s.presence.active() {
		s.presence.left()
		return
	}
	s.presence.left()
	if s.rootCtx() != nil {
		s.mgr.ClearNotice(localAdminNotice)
	}
}

// listenLoopback bindet nur an eine Loopback-Adresse (IP-Literal); alles andere wird
// abgelehnt, damit die Admin-Seite ohne TLS nie im Netz erreichbar ist.
func listenLoopback(addr string) (net.Listener, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("%s ist keine Loopback-Adresse", addr)
	}
	return net.Listen("tcp", addr)
}

// startLocalAdmin startet die Admin-Seite am Terminal. Fehlt sie, bleibt das Terminal
// bedienbar; die Admin-Seite über HTTPS gibt es weiter.
func (s *service) startLocalAdmin(ctx context.Context, wg *sync.WaitGroup, h http.Handler) (*http.Server, string) {
	l, err := listenLoopback(s.opts.LocalAdminAddr)
	if err != nil {
		s.warn(fmt.Sprintf("Admin-Seite am Terminal nicht verfügbar: %v", err))
		return nil, ""
	}
	srv := newLocalServer(ctx, h)
	srv.ReadTimeout, srv.WriteTimeout = 5*time.Minute, 5*time.Minute // Hochladen eines Updates
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := srv.Serve(l); !errors.Is(err, http.ErrServerClosed) {
			s.warn(fmt.Sprintf("Admin-Seite am Terminal beendet: %v", err))
		}
	}()
	return srv, l.Addr().String()
}
