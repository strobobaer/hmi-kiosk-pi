package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"hmikiosk/internal/appcore"
	"hmikiosk/internal/platform"
	"hmikiosk/internal/web"
)

const (
	// DefaultLocalAdminAddr ist die Admin-Seite am Terminal: nur Loopback, HTTP.
	DefaultLocalAdminAddr = appcore.DefaultLocalAdminAddr
	localAdminIdle        = appcore.LocalAdminIdle
	localAdminNotice      = "Die Admin-Seite ist am Terminal geöffnet"
)

// localAdminLeft: Die Kachelseite ist wieder da (Zurück, Inaktivität, Neustart von Chromium).
func (s *service) localAdminLeft() {
	// Auch nach natürlichem Ablauf: Der Hinweis einer gesperrten Sitzung soll weg.
	if s.presence.Left() && s.rootCtx() != nil {
		s.mgr.ClearNotice(localAdminNotice)
	}
}

// startLocalAdmin startet die Admin-Seite am Terminal. Fehlt sie, bleibt das Terminal
// bedienbar; die Admin-Seite über HTTPS gibt es weiter.
func (s *service) startLocalAdmin(ctx context.Context, wg *sync.WaitGroup, h http.Handler) (*http.Server, string) {
	l, err := appcore.ListenLoopback(s.opts.LocalAdminAddr)
	if err != nil {
		s.warn(fmt.Sprintf("Admin-Seite am Terminal nicht verfügbar: %v", err))
		return nil, ""
	}
	srv := newLocalServer(ctx, h)
	srv.ReadTimeout, srv.WriteTimeout = 5*time.Minute, 5*time.Minute // Hochladen eines Updates
	s.localAdminUp.Store(true)
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer s.localAdminUp.Store(false)
		if err := srv.Serve(l); !errors.Is(err, http.ErrServerClosed) {
			s.warn(fmt.Sprintf("Admin-Seite am Terminal beendet: %v", err))
		}
	}()
	return srv, l.Addr().String()
}

// localAdminOrigin ist der Ursprung, den das Zahnrad öffnet.
var localAdminOrigin = strings.TrimSuffix(web.LocalAdminURL, "/admin/")

// localAdminShown: Das Zahnrad erscheint nur, wenn die Admin-Seite am Terminal läuft und die
// Chromium-Richtlinie sie öffnen lässt. Systemteile vor 0.1.6 sperren sie („This page is blocked“).
func (s *service) localAdminShown() bool {
	return s.localAdminUp.Load() && platform.ChromiumAllows(rootFS, localAdminOrigin)
}
