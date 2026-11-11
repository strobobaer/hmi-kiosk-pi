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

	"hmikiosk/internal/appcore"
	"hmikiosk/internal/coord"
	"hmikiosk/internal/platform"
	"hmikiosk/internal/release"
	"hmikiosk/internal/update"
	"hmikiosk/internal/web"
)

func newLocalServer(ctx context.Context, h http.Handler) *http.Server {
	return appcore.NewLocalServer(ctx, h)
}

// newAdminServer: Das Zertifikat kommt aus getCert, damit es eine neue Adresse abdecken kann.
func newAdminServer(h http.Handler, getCert func(*tls.ClientHelloInfo) (*tls.Certificate, error)) *http.Server {
	return &http.Server{
		Handler:           h,
		TLSConfig:         &tls.Config{GetCertificate: getCert, MinVersion: tls.VersionTLS12},
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       5 * time.Minute, // das Hochladen eines Updates
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       60 * time.Second,
	}
}

// startServers: Nur die Kachelseite ist Pflicht. Fehlt die Admin-Seite oder die
// Terminal-API, bleibt das Terminal bedienbar und meldet es.
func (s *service) startServers(ctx context.Context, wg *sync.WaitGroup) (local, admin, localAdmin *http.Server, localErr <-chan error, bound Bound, err error) {
	bound = Bound{Status: s.Status, Snapshot: s.mgr.Snapshot}
	l, err := s.listenTilesAtStart()
	if err != nil {
		return nil, nil, nil, nil, bound, fmt.Errorf("Kachelseite: %w", err)
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

	remote, onTerminal := s.adminHandlers()
	admin, bound.Admin = s.startAdmin(wg, remote)
	localAdmin, bound.LocalAdmin = s.startLocalAdmin(ctx, wg, onTerminal)
	return local, admin, localAdmin, errs, bound, nil
}

// listenTilesAtStart bindet die Kachelseite nach der Einstellung. Ist der Port im Netz
// nicht frei, läuft sie lokal weiter: Das Terminal muss bedienbar bleiben.
func (s *service) listenTilesAtStart() (*tileListener, error) {
	s.tileMu.Lock()
	defer s.tileMu.Unlock()
	want := tileAddr(s.opts.LocalAddr, s.config().Kiosk.NetworkTiles)
	l, err := listenTiles(want)
	if err != nil && want != s.opts.LocalAddr {
		s.warn(fmt.Sprintf("Kachelseite nicht im Netz freigegeben, sie läuft nur lokal: %v", err))
		l, err = listenTiles(s.opts.LocalAddr)
	}
	if err != nil {
		return nil, err
	}
	s.tileLn = l
	return l, nil
}

func (s *service) startAdmin(wg *sync.WaitGroup, h http.Handler) (*http.Server, string) {
	cert, dir, ok := s.loadCert()
	if !ok {
		return nil, ""
	}
	l, err := net.Listen("tcp", s.opts.AdminAddr)
	if err != nil {
		s.warn(fmt.Sprintf("Admin-Seite und Terminal-API nicht verfügbar: %v", err))
		return nil, ""
	}
	src := web.NewCertSource(dir, cert)
	s.certSrc.Store(src)
	srv := newAdminServer(s.adminMux(h), src.GetCertificate)
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
// den Fehler melden kann und die Abstimmung weiterläuft. dir ist der Ordner des Zertifikats.
func (s *service) loadCert() (tls.Certificate, string, bool) {
	dir := filepath.Join(s.opts.DataDir, tlsDirName)
	cert, err := web.LoadOrCreateCert(dir)
	if err == nil {
		return cert, dir, true
	}
	s.warn(fmt.Sprintf("Zertifikat auf der Datenpartition nicht verfügbar, es gilt ein vorläufiges: %v", err))
	dir = filepath.Join(s.opts.RunDir, tlsDirName)
	cert, err = web.LoadOrCreateCert(dir)
	if err != nil {
		s.warn(fmt.Sprintf("Admin-Seite und Terminal-API nicht verfügbar, kein Zertifikat: %v", err))
		return tls.Certificate{}, "", false
	}
	return cert, dir, true
}

func (s *service) adminMux(admin http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/peer/", coord.NewHandlerFunc(s.coord, s.peerKey))
	mux.Handle("/admin/", admin)
	mux.Handle("/api/remote/", s.remoteAPI())
	// Die kurze Adresse https://<Gerät>:8443 führt zur Admin-Seite.
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/", http.StatusSeeOther)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "Seite nicht gefunden", http.StatusNotFound)
	})
	return mux
}

// adminHandlers baut beide Zugänge der Admin-Seite (HTTPS und am Terminal) mit
// gemeinsamen Sitzungen und gemeinsamer Sperre.
func (s *service) adminHandlers() (remote, local http.Handler) {
	// Ohne Schlüsseldatei bleibt die Admin-Seite erreichbar: Sie leitet auf die Reparatur um.
	return web.NewAdminHandlers(s.adminDeps())
}

func (s *service) adminDeps() web.AdminDeps {
	return web.AdminDeps{
		Store:        s.store,
		Box:          s.box,
		Apply:        s.Apply,
		ConfigMu:     &s.cfgMu,
		Probe:        s.adminProbe,
		Helper:       s.helper,
		Status:       s.Status,
		LogDir:       filepath.Join(s.opts.DataDir, logDirName),
		RemoteIsWifi: remoteIsWifi,
		EthernetLink: func() bool { return platform.EthernetHasLink(sysRoot) },
		Update: &update.Installer{
			Root:        s.opts.DataDir,
			PublicKey:   release.PublicKey(),
			Product:     release.HMIKiosk,
			SystemLevel: func() int { return platform.InstalledSystemLevel(rootFS, localAdminOrigin) },
			Activate: func(ctx context.Context, version string) error {
				return s.helper.Call(ctx, "update-activate", version)
			},
		},
		UpdateCheck:          s.updates,
		Selftest:             s.adminSelftest,
		Soak:                 s.soak,
		Backup:               s.helper,
		Net:                  s.helper,
		HardwareID:           func() string { return s.hwID },
		TileURLs:             s.tileURLs,
		TLSDir:               filepath.Join(s.opts.DataDir, tlsDirName),
		TLSFingerprint:       s.tlsFingerprint,
		KeyFile:              filepath.Join(s.opts.DataDir, secretsFile),
		CheckPeer:            s.checkPeer,
		UpdateKeyFingerprint: release.Fingerprint(release.PublicKey()),
		Keyboard:             s.kbd,
		LocalActive:          s.presence.Touch,
		LocalLeft:            s.localAdminLeft,
		ClearWebLogins:       s.clearWebLogins,
	}
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
