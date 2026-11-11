package pdhterminal

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"hmikiosk/internal/appcore"
	"hmikiosk/internal/browser"
	"hmikiosk/internal/config"
	"hmikiosk/internal/helperapi"
	"hmikiosk/internal/platform"
	"hmikiosk/internal/release"
	"hmikiosk/internal/secrets"
	"hmikiosk/internal/selftest"
	"hmikiosk/internal/update"
	"hmikiosk/internal/updatecheck"
	"hmikiosk/internal/web"
)

// Vorgaben der Adressen und Pfade; sie gelten wie beim HMI-Kiosk, damit Helfer,
// Chromium-Richtlinie (feste Freigabe 8080/8081) und Einheiten gleich bleiben.
const (
	DefaultLocalAddr      = "127.0.0.1:8080"
	DefaultAdminAddr      = ":8443"
	DefaultLocalAdminAddr = appcore.DefaultLocalAdminAddr
	DefaultHelperSocket   = "/run/hmi-kiosk-helper/helper.sock"

	secretsFile   = "secrets.key"
	logDirName    = "log"
	tlsDirName    = "tls"
	chromiumDir   = "chromium"
	shutdownLimit = 5 * time.Second
	startPath     = "/start"
)

// Options sind die Startwerte des Dienstes. Adressen dürfen Port 0 nennen.
type Options struct {
	DataDir, RunDir string
	LocalAddr       string // Startseite, nur Loopback
	AdminAddr       string // Admin-Seite über HTTPS
	LocalAdminAddr  string // Admin-Seite am Terminal, nur Loopback
	HelperSocket    string
	ChromiumEnabled bool
	// HardwareID liest die Hardware-Kennung; nil heißt: aus dem Gerät lesen.
	HardwareID func() string
	// Ready wird gerufen, wenn der Dienst läuft (für Tests).
	Ready func(Bound)
}

// Bound nennt die tatsächlich gebundenen Adressen; Admin und LocalAdmin sind leer, wenn
// die Seite nicht gestartet werden konnte.
type Bound struct{ Local, Admin, LocalAdmin string }

// rootFS ist die Wurzel für Systemteile (Systemstand); Tests setzen einen Ordner.
var rootFS = "/"

const (
	procRoot = "/proc"
	sysRoot  = "/sys"
	devInput = "/dev/input"
)

// localAdminOrigin ist der Ursprung der Admin-Seite am Terminal (für den Systemstand).
var localAdminOrigin = strings.TrimSuffix(web.LocalAdminURL, "/admin/")

type service struct {
	o        Options
	store    *config.Store
	journal  *appcore.Journal
	box      *secrets.Box
	helper   *helperapi.Client
	origins  *appcore.OriginSync
	updates  *updatecheck.Service
	presence appcore.AdminPresence
	client   Client
	sup      atomic.Pointer[browser.Supervisor] // ab startBrowser
	certSrc  atomic.Pointer[web.CertSource]

	cfgMu    sync.Mutex // Laden, Ändern, Speichern der Konfiguration als Einheit (auch für die Admin-Seite)
	mu       sync.RWMutex
	cfg      config.Config
	cfgWarn  string
	boxWarn  string
	warnings []string
	markGood bool
	hwID     string
}

// Run führt den Dienst aus, bis ctx endet; ein geordnetes Ende ergibt nil.
func Run(ctx context.Context, o Options) error {
	s, err := newService(o)
	if err != nil {
		return err
	}
	return s.serve(ctx)
}

func newService(o Options) (*service, error) {
	if o.DataDir == "" || o.RunDir == "" {
		return nil, errors.New("Datenordner und Laufzeitordner müssen angegeben sein")
	}
	o.LocalAddr = orDefault(o.LocalAddr, DefaultLocalAddr)
	o.AdminAddr = orDefault(o.AdminAddr, DefaultAdminAddr)
	o.LocalAdminAddr = orDefault(o.LocalAdminAddr, DefaultLocalAdminAddr)
	o.HelperSocket = orDefault(o.HelperSocket, DefaultHelperSocket)
	s := &service{o: o, store: config.Open(o.DataDir), helper: helperapi.New(o.HelperSocket)}

	journal, err := appcore.NewJournal(filepath.Join(o.DataDir, logDirName))
	s.journal = journal
	log.SetOutput(journal)
	if err != nil {
		log.Printf("Protokoll nicht verfügbar: %v", err)
	}
	if err := os.MkdirAll(o.RunDir, 0o755); err != nil {
		s.warn(fmt.Sprintf("Laufzeitordner nicht verfügbar: %v", err))
	}
	s.origins = appcore.NewOriginSync(s.helper.Call)
	// Das Produkt steht ausdrücklich da: Ein leeres Produkt hieße HMI-Kiosk.
	s.updates = appcore.NewUpdateCheck(release.PDHTerminal, func() config.UpdateCfg { return s.config().Update }, nil)
	s.cleanupStartup()
	loadFailed := s.loadConfig()
	s.hwID = s.detectHardwareID()
	s.claimHardware(loadFailed)
	s.openBox(loadFailed)
	// Die Richtlinie vom Helfer-Start gilt bis hierhin; abgeglichen wird trotzdem.
	s.origins.Set(config.PDHOrigins(s.config().PDH))
	return s, nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// warn merkt sich einen Hinweis für die Admin-Seite und schreibt ihn ins Protokoll.
func (s *service) warn(msg string) {
	log.Print(msg)
	s.mu.Lock()
	defer s.mu.Unlock()
	if !slices.Contains(s.warnings, msg) {
		s.warnings = append(s.warnings, msg)
	}
}

func (s *service) config() config.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

// apply übernimmt eine gespeicherte Konfiguration; die erlaubten Web-Ursprünge folgen
// der PDH-Adresse (leer, solange keine eingetragen ist).
func (s *service) apply(c config.Config) {
	s.mu.Lock()
	s.cfg, s.cfgWarn = c, "" // Der Aufrufer hat gespeichert, config.json ist also wieder gültig.
	s.mu.Unlock()
	s.origins.Set(config.PDHOrigins(c.PDH))
}

// cleanupStartup räumt auf, was ein abgestürzter Vorgänger hinterlassen hat. Die alte
// Gesundmarke zählt nicht mehr: Sie gälte sonst, bevor dieser Start gelungen ist.
func (s *service) cleanupStartup() {
	if err := os.RemoveAll(filepath.Join(s.o.RunDir, chromiumDir)); err != nil {
		log.Printf("Reste von Chromium nicht entfernt: %v", err)
	}
	if err := os.Remove(filepath.Join(s.o.RunDir, appcore.HealthyFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("Alte Gesundmarke nicht entfernt: %v", err)
	}
}

// loadConfig lädt die Konfiguration; true, wenn sie nicht lesbar war und die Grundeinstellung gilt.
func (s *service) loadConfig() (failed bool) {
	cfg, info, err := s.store.Load()
	var warn string
	switch {
	case err != nil:
		cfg = config.Default()
		warn = fmt.Sprintf("Konfiguration nicht lesbar, der Dienst läuft mit der Grundeinstellung: %v", err)
		failed = true
	case info.UsedFallback:
		warn = fmt.Sprintf("Konfiguration defekt, Rückfall auf den letzten guten Stand: %s", info.Reason)
	case info.FirstStart:
		if err := s.store.Save(cfg); err != nil {
			warn = fmt.Sprintf("Konfiguration nicht gespeichert, sie gilt nur bis zum Neustart: %v", err)
		} else {
			s.markGood = true
		}
	default:
		s.markGood = true
	}
	if warn != "" {
		log.Print(warn)
	}
	s.mu.Lock()
	s.cfg, s.cfgWarn = cfg, warn
	s.mu.Unlock()
	return failed
}

func (s *service) detectHardwareID() string {
	if s.o.HardwareID != nil {
		return s.o.HardwareID()
	}
	return platform.HardwareID(procRoot, sysRoot)
}

// claimHardware hält die Kennung des ersten Geräts fest (wie beim HMI-Kiosk); einen
// Wartungsmodus kennt das PDH-Terminal nicht.
func (s *service) claimHardware(loadFailed bool) {
	cfg := s.config()
	if s.hwID == "" || loadFailed || !s.markGood || cfg.Terminal.HardwareID != "" {
		return
	}
	cfg.Terminal.HardwareID = s.hwID
	if err := s.store.Save(cfg); err != nil {
		s.warn(fmt.Sprintf("Hardware-Kennung nicht gespeichert, sie gilt nur bis zum Neustart: %v", err))
		return
	}
	s.mu.Lock()
	s.cfg.Terminal.HardwareID = s.hwID
	s.mu.Unlock()
}

func (s *service) openBox(loadFailed bool) {
	s.box = secrets.Unavailable()
	err := os.MkdirAll(s.o.DataDir, 0o700)
	if err == nil {
		var box *secrets.Box
		box, err = secrets.OpenBox(filepath.Join(s.o.DataDir, secretsFile), s.config().HasSealedValues() || loadFailed)
		if err == nil {
			s.box = box
		}
	}
	if err != nil {
		warn := fmt.Sprintf("Schlüsseldatei nicht verfügbar, gespeicherte Passwörter sind nicht lesbar: %v", err)
		log.Print(warn)
		s.mu.Lock()
		s.boxWarn = warn
		s.mu.Unlock()
	}
}

// serve läuft bis ctx endet.
func (s *service) serve(parent context.Context) error {
	defer s.close()
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	var wg sync.WaitGroup
	background := func(f func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f()
		}()
	}
	background(func() { s.origins.Run(ctx) })
	background(func() { appcore.RunUpdateCheck(ctx, s.updates) })

	l, err := appcore.ListenLoopback(s.o.LocalAddr)
	if err != nil {
		cancel()
		wg.Wait()
		return fmt.Errorf("Startseite: %w", err)
	}
	bound := Bound{Local: l.Addr().String()}
	local := appcore.NewLocalServer(ctx, StartHandler(s.o.DataDir, func() config.PDHCfg { return s.config().PDH }, s.client))
	localErr := make(chan error, 1)
	background(func() {
		if err := local.Serve(l); !errors.Is(err, http.ErrServerClosed) {
			localErr <- fmt.Errorf("Startseite: %w", err)
		}
	})

	remote, onTerminal := web.NewAdminHandlers(s.adminDeps())
	admin, adminAddr := s.startAdmin(&wg, remote)
	bound.Admin = adminAddr
	stopLocalAdmin, err := appcore.ServeLocalAdmin(ctx, s.o.LocalAdminAddr, onTerminal, &s.presence)
	if err != nil {
		s.warn(fmt.Sprintf("Admin-Seite am Terminal nicht verfügbar: %v", err))
	} else {
		bound.LocalAdmin = s.o.LocalAdminAddr
	}

	browserDone := s.startBrowser(ctx, "http://"+bound.Local+startPath)
	s.announce(ctx, bound, background)

	err = nil
	select {
	case <-ctx.Done():
	case err = <-localErr:
	}
	cancel()
	if serr := appcore.Notify("STOPPING=1"); serr != nil {
		log.Printf("Abmeldung bei systemd: %v", serr)
	}
	<-browserDone
	if stopLocalAdmin != nil {
		stopLocalAdmin() // ServeLocalAdmin endet nicht mit ctx
	}
	shutdownServers(local, admin)
	wg.Wait()
	return err
}

func (s *service) close() {
	if log.Writer() == s.journal {
		log.SetOutput(os.Stderr)
	}
	s.journal.Close()
}

// announce schließt den Start ab: Alles läuft, also gilt der Stand als gut, die Marke für
// den Update-Wächter steht, und systemd erfährt es.
func (s *service) announce(ctx context.Context, bound Bound, background func(func())) {
	// Ohne Passwort kein Rückfall: Dieser Stand öffnete sonst die Ersteinrichtung.
	if s.markGood && s.config().Admin.PasswordHash != "" {
		if err := s.store.MarkGood(); err != nil {
			log.Printf("Rückfallstand nicht gespeichert: %v", err)
		}
	}
	if err := appcore.WriteHealthy(s.o.RunDir, release.Version); err != nil {
		s.warn(fmt.Sprintf("Gesundmarke nicht geschrieben, ein Update fiele zurück: %v", err))
	}
	if err := appcore.Notify("READY=1"); err != nil {
		log.Printf("Anmeldung bei systemd: %v", err)
	}
	// systemd bekommt sein Lebenszeichen nur, solange die Startseite antwortet.
	background(func() {
		appcore.WatchdogProbe(ctx, func(pctx context.Context) error {
			c, err := (&net.Dialer{}).DialContext(pctx, "tcp", bound.Local)
			if err == nil {
				c.Close()
			}
			return err
		})
	})
	log.Printf("PDH-Terminal läuft, Version %s", release.Version)
	if s.o.Ready != nil {
		s.o.Ready(bound)
	}
}

// startBrowser liefert einen Kanal, der schließt, sobald Chromium beendet ist.
func (s *service) startBrowser(ctx context.Context, startURL string) <-chan struct{} {
	done := make(chan struct{})
	if !s.o.ChromiumEnabled {
		close(done)
		return done
	}
	path, err := browser.FindChromium(nil)
	if err != nil {
		s.warn(fmt.Sprintf("Chromium nicht verfügbar: %v", err))
	}
	ram := filepath.Join(s.o.RunDir, chromiumDir)
	if err := os.MkdirAll(ram, 0o700); err != nil {
		s.warn(fmt.Sprintf("RAM-Ordner für Chromium nicht verfügbar: %v", err))
	}
	sup := browser.NewExec(path, ram, func() int { w, _ := s.screen(); return w })
	sup.SetProfile(s.config().Display.GraphicsProfile)
	sup.SetStartURL(startURL)
	sup.Show(browser.TargetStart)
	s.sup.Store(sup)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		sup.Run(ctx)
	}()
	go func() {
		defer wg.Done()
		// Die Berührung oben rechts öffnet die Admin-Seite. Die Höhe ist hier unbekannt
		// (0): Für diese Achse gilt die Regel „6 % der Achse“.
		w, _ := s.screen()
		cfg := platform.CornerCfg{ScreenW: w}
		if err := platform.WatchCorner(ctx, devInput, cfg, func() { sup.Show(browser.TargetAdmin) }); err != nil && ctx.Err() == nil {
			log.Printf("Touch-Überwachung beendet: %v", err)
		}
	}()
	go func() {
		wg.Wait()
		close(done)
	}()
	return done
}

// localLeft: Die Admin-Seite wurde verlassen („Zurück“ oder Leerlauf). Der Browser kehrt
// zur Startseite zurück, sonst öffnete ein Neustart von Chromium wieder die Admin-Seite.
func (s *service) localLeft() {
	s.presence.Left()
	if sup := s.sup.Load(); sup != nil {
		sup.Show(browser.TargetStart)
	}
}

// screen liefert die Bildschirmgröße aus Einstellung oder Monitor.
func (s *service) screen() (int, int) {
	c := s.config()
	w, h, found := platform.DisplaySize(sysRoot)
	return platform.Resolve(c.Display.OverrideW, c.Display.OverrideH, w, h, found)
}

// startAdmin: Fehlt die Admin-Seite über HTTPS, bleibt das Terminal bedienbar und meldet es.
func (s *service) startAdmin(wg *sync.WaitGroup, h http.Handler) (*http.Server, string) {
	cert, dir, ok := s.loadCert()
	if !ok {
		return nil, ""
	}
	l, err := net.Listen("tcp", s.o.AdminAddr)
	if err != nil {
		s.warn(fmt.Sprintf("Admin-Seite nicht verfügbar: %v", err))
		return nil, ""
	}
	src := web.NewCertSource(dir, cert)
	s.certSrc.Store(src)
	mux := http.NewServeMux()
	mux.Handle("/admin/", h)
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/", http.StatusSeeOther)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "Seite nicht gefunden", http.StatusNotFound)
	})
	srv := &http.Server{
		Handler:           mux,
		TLSConfig:         &tls.Config{GetCertificate: src.GetCertificate, MinVersion: tls.VersionTLS12},
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       5 * time.Minute, // das Hochladen eines Updates
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       60 * time.Second,
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := srv.ServeTLS(l, "", ""); !errors.Is(err, http.ErrServerClosed) {
			s.warn(fmt.Sprintf("Admin-Seite beendet: %v", err))
		}
	}()
	return srv, l.Addr().String()
}

// loadCert weicht bei defekter Datenpartition in den RAM aus, damit die Admin-Seite den
// Fehler melden kann. dir ist der Ordner des Zertifikats.
func (s *service) loadCert() (tls.Certificate, string, bool) {
	dir := filepath.Join(s.o.DataDir, tlsDirName)
	cert, err := web.LoadOrCreateCert(dir)
	if err == nil {
		return cert, dir, true
	}
	s.warn(fmt.Sprintf("Zertifikat auf der Datenpartition nicht verfügbar, es gilt ein vorläufiges: %v", err))
	dir = filepath.Join(s.o.RunDir, tlsDirName)
	cert, err = web.LoadOrCreateCert(dir)
	if err != nil {
		s.warn(fmt.Sprintf("Admin-Seite nicht verfügbar, kein Zertifikat: %v", err))
		return tls.Certificate{}, "", false
	}
	return cert, dir, true
}

func (s *service) tlsFingerprint() string {
	if src := s.certSrc.Load(); src != nil {
		return src.PublicKeyFingerprint()
	}
	return ""
}

func (s *service) adminDeps() web.AdminDeps {
	return web.AdminDeps{
		Store:        s.store,
		Box:          s.box,
		Apply:        s.apply,
		ConfigMu:     &s.cfgMu,
		Helper:       s.helper,
		Status:       s.status,
		LogDir:       filepath.Join(s.o.DataDir, logDirName),
		RemoteIsWifi: remoteIsWifi,
		EthernetLink: func() bool { return platform.EthernetHasLink(sysRoot) },
		Update: &update.Installer{
			Root:        s.o.DataDir,
			PublicKey:   release.PublicKey(),
			Product:     release.PDHTerminal,
			SystemLevel: func() int { return platform.InstalledSystemLevel(rootFS, localAdminOrigin) },
			Activate: func(ctx context.Context, version string) error {
				return s.helper.Call(ctx, "update-activate", version)
			},
		},
		UpdateCheck:          s.updates,
		Selftest:             s.adminSelftest,
		Backup:               s.helper,
		Net:                  s.helper,
		HardwareID:           func() string { return s.hwID },
		TLSDir:               filepath.Join(s.o.DataDir, tlsDirName),
		TLSFingerprint:       s.tlsFingerprint,
		KeyFile:              filepath.Join(s.o.DataDir, secretsFile),
		UpdateKeyFingerprint: release.Fingerprint(release.PublicKey()),
		LocalActive:          s.presence.Touch,
		LocalLeft:            s.localLeft,
		Product:              Admin(s.o.DataDir, s.client),
	}
}

// remoteIsWifi: Die Antwort geht über die Schnittstelle, auf der die Anfrage ankam.
func remoteIsWifi(r *http.Request) bool {
	addr, ok := r.Context().Value(http.LocalAddrContextKey).(*net.TCPAddr)
	if !ok {
		return false
	}
	_, wifi := platform.InterfaceForIP(addr.IP)
	return wifi
}

// adminSelftest ist die Selbstprüfung für die Admin-Seite: nur die Schritte, die jedes
// Produkt braucht (Schutz von System und Funk).
func (s *service) adminSelftest(ctx context.Context) []web.SelftestStep {
	d := selftest.Deps{
		Config:      s.config,
		Mounts:      func() ([]byte, error) { return os.ReadFile(filepath.Join(procRoot, "mounts")) },
		Exec:        runCommand,
		BootConfig:  readBootConfig,
		ImageMarker: func() bool { _, err := os.Stat(selftest.ImageMarkerFile); return err == nil },
	}
	res := selftest.RunSteps(ctx, d, selftest.CoreSteps())
	out := make([]web.SelftestStep, len(res))
	for i, r := range res {
		out[i] = web.SelftestStep{Name: r.Name, OK: r.OK, Detail: r.Detail, NeedsHuman: r.NeedsHuman}
	}
	return out
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
