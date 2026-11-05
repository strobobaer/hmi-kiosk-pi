package app

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"hmikiosk/internal/coord"
	"hmikiosk/internal/platform"
	"hmikiosk/internal/release"
	"hmikiosk/internal/update"
	"hmikiosk/internal/web"
)

func newLocalServer(ctx context.Context, h http.Handler) *http.Server {
	return &http.Server{
		Handler:           h,
		BaseContext:       func(net.Listener) context.Context { return ctx },
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      0, // der Ereignisstrom läuft unbegrenzt
		IdleTimeout:       60 * time.Second,
	}
}

func newAdminServer(h http.Handler, cert tls.Certificate) *http.Server {
	return &http.Server{
		Handler:           h,
		TLSConfig:         &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       5 * time.Minute, // das Hochladen eines Updates
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       60 * time.Second,
	}
}

// startServers: Nur die Kachelseite ist Pflicht. Fehlt die Admin-Seite oder die
// Terminal-API, bleibt das Terminal bedienbar und meldet es.
func (s *service) startServers(ctx context.Context, wg *sync.WaitGroup) (local, admin *http.Server, localErr <-chan error, bound Bound, err error) {
	bound = Bound{Status: s.Status, Snapshot: s.mgr.Snapshot}
	l, err := net.Listen("tcp", s.opts.LocalAddr)
	if err != nil {
		return nil, nil, nil, bound, fmt.Errorf("Kachelseite: %w", err)
	}
	bound.Local = l.Addr().String()
	local = newLocalServer(ctx, s.localHandler())
	errs := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := local.Serve(l); !errors.Is(err, http.ErrServerClosed) {
			errs <- fmt.Errorf("Kachelseite: %w", err)
		}
	}()

	admin, bound.Admin = s.startAdmin(wg)
	return local, admin, errs, bound, nil
}

func (s *service) startAdmin(wg *sync.WaitGroup) (*http.Server, string) {
	cert, ok := s.loadCert()
	if !ok {
		return nil, ""
	}
	l, err := net.Listen("tcp", s.opts.AdminAddr)
	if err != nil {
		s.warn(fmt.Sprintf("Admin-Seite und Terminal-API nicht verfügbar: %v", err))
		return nil, ""
	}
	srv := newAdminServer(s.adminMux(), cert)
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := srv.ServeTLS(l, "", ""); !errors.Is(err, http.ErrServerClosed) {
			s.warn(fmt.Sprintf("Admin-Seite und Terminal-API beendet: %v", err))
		}
	}()
	return srv, l.Addr().String()
}

// loadCert weicht bei defekter Datenpartition in den RAM aus, damit die Admin-Seite
// den Fehler melden kann und die Abstimmung weiterläuft.
func (s *service) loadCert() (tls.Certificate, bool) {
	cert, err := web.LoadOrCreateCert(filepath.Join(s.opts.DataDir, tlsDirName))
	if err == nil {
		return cert, true
	}
	s.warn(fmt.Sprintf("Zertifikat auf der Datenpartition nicht verfügbar, es gilt ein vorläufiges: %v", err))
	cert, err = web.LoadOrCreateCert(filepath.Join(s.opts.RunDir, tlsDirName))
	if err != nil {
		s.warn(fmt.Sprintf("Admin-Seite und Terminal-API nicht verfügbar, kein Zertifikat: %v", err))
		return tls.Certificate{}, false
	}
	return cert, true
}

func (s *service) adminMux() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/peer/", coord.NewHandlerFunc(s.coord, s.peerKey))
	mux.Handle("/", s.adminHandler())
	return mux
}

func (s *service) adminHandler() http.Handler {
	// Ohne Schlüsseldatei kann die Admin-Seite nichts ver- oder entschlüsseln.
	if s.box == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "Die Admin-Seite ist nicht verfügbar: Die Schlüsseldatei auf der Datenpartition ist nicht lesbar. Einzelheiten stehen im Protokoll des Dienstes.", http.StatusServiceUnavailable)
		})
	}
	return web.NewAdmin(web.AdminDeps{
		Store:        s.store,
		Box:          s.box,
		Apply:        s.Apply,
		Probe:        s.adminProbe,
		Helper:       s.helper,
		Status:       s.Status,
		LogDir:       filepath.Join(s.opts.DataDir, logDirName),
		RemoteIsWifi: remoteIsWifi,
		EthernetLink: func() bool { return platform.EthernetHasLink(sysRoot) },
		Update: &update.Installer{
			Root:      s.opts.DataDir,
			PublicKey: release.PublicKey(),
			Activate: func(ctx context.Context, version string) error {
				return s.helper.Call(ctx, "update-activate", version)
			},
		},
		Selftest: s.adminSelftest,
		Soak:     s.soak,
	})
}

func shutdownServers(servers ...*http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownLimit)
	defer cancel()
	var wg sync.WaitGroup
	for _, srv := range servers {
		if srv == nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := srv.Shutdown(ctx); err != nil {
				srv.Close()
			}
		}()
	}
	wg.Wait()
}
