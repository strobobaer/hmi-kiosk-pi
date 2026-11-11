package appcore

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"time"
)

const (
	// DefaultLocalAdminAddr ist die Admin-Seite am Terminal: nur Loopback, HTTP.
	DefaultLocalAdminAddr = "127.0.0.1:8081"
	// LocalAdminIdle: So lange gilt die Admin-Seite am Terminal nach dem letzten Aufruf
	// als offen (wie die kurze Sitzung der Seite).
	LocalAdminIdle = 5 * time.Minute
)

// Clock liefert die Zeit; im Test ersetzbar.
type Clock interface{ Now() time.Time }

// AdminPresence merkt sich, ob Chromium am Terminal gerade die Admin-Seite zeigt.
// Solange startet keine Sitzung und keine Daueranzeige: Sie würde die Seite schließen.
type AdminPresence struct {
	Clock Clock // nil: Systemzeit
	mu    sync.Mutex
	until time.Time
}

func (p *AdminPresence) now() time.Time {
	if p.Clock == nil {
		return time.Now()
	}
	return p.Clock.Now()
}

// Touch verlängert die Präsenz um LocalAdminIdle.
func (p *AdminPresence) Touch() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.until = p.now().Add(LocalAdminIdle)
}

// Left beendet die Präsenz; wasOpen meldet, ob sie seit dem letzten Left bestand
// (auch wenn sie inzwischen abgelaufen ist).
func (p *AdminPresence) Left() (wasOpen bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	wasOpen = !p.until.IsZero()
	p.until = time.Time{}
	return wasOpen
}

// Active meldet, ob die Admin-Seite am Terminal offen ist.
func (p *AdminPresence) Active() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.until.IsZero() && p.now().Before(p.until)
}

// ListenLoopback bindet nur an eine Loopback-Adresse (IP-Literal); alles andere wird
// abgelehnt, damit die Admin-Seite ohne TLS nie im Netz erreichbar ist.
func ListenLoopback(addr string) (net.Listener, error) {
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

// NewLocalServer ist der HTTP-Server für Loopback-Seiten (Ereignisstrom ohne Schreibgrenze).
func NewLocalServer(ctx context.Context, h http.Handler) *http.Server {
	return &http.Server{
		Handler:           h,
		BaseContext:       func(net.Listener) context.Context { return ctx },
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      0, // der Ereignisstrom läuft unbegrenzt
		IdleTimeout:       60 * time.Second,
	}
}

// ServeLocalAdmin startet die Admin-Seite am Terminal auf einer Loopback-Adresse. stop
// beendet den Server und wartet auf ihn; presence (darf nil sein) gilt danach als verlassen.
func ServeLocalAdmin(ctx context.Context, addr string, h http.Handler, presence *AdminPresence) (stop func(), err error) {
	l, err := ListenLoopback(addr)
	if err != nil {
		return nil, err
	}
	srv := NewLocalServer(ctx, h)
	srv.ReadTimeout, srv.WriteTimeout = 5*time.Minute, 5*time.Minute // Hochladen eines Updates
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := srv.Serve(l); !errors.Is(err, http.ErrServerClosed) {
			log.Printf("Admin-Seite am Terminal beendet: %v", err)
		}
	}()
	return func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if srv.Shutdown(sctx) != nil {
			srv.Close()
		}
		<-done
		if presence != nil {
			presence.Left()
		}
	}, nil
}
