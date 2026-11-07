// Package app setzt alle Pakete zum Kiosk-Dienst zusammen.
package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"hmikiosk/internal/browser"
	"hmikiosk/internal/client"
	"hmikiosk/internal/config"
	"hmikiosk/internal/coord"
	"hmikiosk/internal/helperapi"
	"hmikiosk/internal/platform"
	"hmikiosk/internal/release"
	"hmikiosk/internal/rfb"
	"hmikiosk/internal/secrets"
	"hmikiosk/internal/selftest"
	"hmikiosk/internal/session"
	"hmikiosk/internal/web"
)

const (
	procRoot = "/proc"
	sysRoot  = "/sys"
	devInput = "/dev/input"

	healthyFile   = "healthy"
	sessionsDir   = "sessions"
	chromiumDir   = "chromium"
	secretsFile   = "secrets.key"
	logDirName    = "log"
	tlsDirName    = "tls"
	shutdownLimit = 5 * time.Second

	DefaultLocalAddr      = "127.0.0.1:8080"
	DefaultAdminAddr      = ":8443"
	DefaultHelperSocket   = "/run/hmi-kiosk-helper/helper.sock"
	DefaultSessionProfile = "/run/hmi-kiosk-session/profile"
)

// Options sind die Startwerte des Dienstes. Adressen dürfen Port 0 nennen.
type Options struct {
	DataDir, RunDir, LocalAddr, AdminAddr string
	HelperSocket                          string
	// SessionProfileFile nennt das Grafikprofil der laufenden Sitzung (hmi-kiosk-session prepare).
	SessionProfileFile string
	Runner             client.Runner
	// HardwareID liest die Hardware-Kennung des Geräts; nil heißt: aus dem Gerät lesen.
	HardwareID      func() string
	ChromiumEnabled bool
	// Ready wird gerufen, wenn der Dienst läuft (für Tests).
	Ready func(Bound)
}

// Bound beschreibt den laufenden Dienst. Admin ist leer, wenn die Admin-Seite
// nicht gestartet werden konnte.
type Bound struct {
	Local, Admin string
	Status       func() web.SystemStatus
	Snapshot     func() session.Snapshot
}

type service struct {
	opts     Options
	store    *config.Store
	journal  *Journal
	notify   notifier
	box      *secrets.Box // nie nil; ohne Schlüssel (Reparaturmodus) nicht verfügbar
	runner   client.Runner
	coord    *coord.Coordinator
	mgr      *session.Manager
	hosts    *hostCache
	reach    *reachability
	helper   *helperapi.Client
	lookPath func(string) (string, error)
	browser  chan struct{} // Änderungen, die die Browser-Goroutine betreffen

	selftestMu   sync.Mutex
	soak         *soakJob
	ctxMu        sync.Mutex
	ctx          context.Context // Lebensdauer des Dienstes, ab serve
	selftestPage selftestPage
	touch        *selftest.TouchBoard

	markGood     bool   // der geladene Stand kommt unverändert aus config.json
	loadFailed   bool   // keine lesbare Konfiguration, es gilt die Grundeinstellung
	usedFallback bool   // der Rückfallstand gilt, config.json ist verloren oder defekt
	hwID         string // Hardware-Kennung dieses Geräts, leer wenn nicht lesbar

	mu       sync.RWMutex
	cfg      config.Config
	key      []byte
	keyWarn  string
	boxWarn  string // Grund, warum die Schlüsseldatei nicht verfügbar ist
	cfgWarn  string
	warnings []string
}

// Run führt den Dienst aus, bis ctx endet; ein geordnetes Ende ergibt nil.
func Run(ctx context.Context, opts Options) error {
	s, err := newService(opts)
	if err != nil {
		return err
	}
	return s.serve(ctx)
}

func newService(opts Options) (*service, error) {
	if opts.Runner == nil {
		return nil, errors.New("Options.Runner fehlt")
	}
	if opts.DataDir == "" || opts.RunDir == "" {
		return nil, errors.New("Datenordner und Laufzeitordner müssen angegeben sein")
	}
	if opts.LocalAddr == "" {
		opts.LocalAddr = DefaultLocalAddr
	}
	if opts.AdminAddr == "" {
		opts.AdminAddr = DefaultAdminAddr
	}
	if opts.HelperSocket == "" {
		opts.HelperSocket = DefaultHelperSocket
	}
	if opts.SessionProfileFile == "" {
		opts.SessionProfileFile = DefaultSessionProfile
	}
	s := &service{
		opts:    opts,
		store:   config.Open(opts.DataDir),
		notify:  notifierFromEnv(),
		helper:  helperapi.New(opts.HelperSocket),
		browser: make(chan struct{}, 1),
		touch:   selftest.NewTouchBoard(),
	}
	journal, err := NewJournal(filepath.Join(opts.DataDir, logDirName), MaxLogBytes)
	s.journal = journal
	log.SetOutput(journal)
	if err != nil {
		log.Printf("Protokoll nicht verfügbar: %v", err)
	}
	if err := os.MkdirAll(opts.RunDir, 0o755); err != nil {
		s.warn(fmt.Sprintf("Laufzeitordner nicht verfügbar: %v", err))
	}
	s.soak = newSoakJob(s)
	s.cleanupStartup()
	s.loadConfig()
	s.hwID = s.detectHardwareID()
	s.claimHardware()
	s.openBox()
	s.mu.Lock()
	s.key, s.keyWarn = s.openKey(s.cfg)
	cfg, key := s.cfg, s.key
	s.mu.Unlock()
	s.runner = trackingRunner{inner: opts.Runner, rec: &pidRecorder{path: filepath.Join(opts.RunDir, pidFileName)}}
	s.buildParts(cfg, key)
	s.coord.SetStandby(s.maintenance())
	return s, nil
}

// cleanupStartup räumt auf, was ein abgestürzter Vorgänger hinterlassen hat. Die
// alte Gesundmarke zählt nicht mehr: Sie gälte sonst, bevor dieser Start gelungen ist.
func (s *service) cleanupStartup() {
	run := s.opts.RunDir
	killLeftovers(procRoot, filepath.Join(run, pidFileName), leftoverGrace)
	for _, dir := range []string{sessionsDir, chromiumDir} {
		if err := os.RemoveAll(filepath.Join(run, dir)); err != nil {
			log.Printf("Reste in %s nicht entfernt: %v", dir, err)
		}
	}
	if err := os.Remove(filepath.Join(run, healthyFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("Alte Gesundmarke nicht entfernt: %v", err)
	}
}

func (s *service) loadConfig() {
	cfg, info, err := s.store.Load()
	var warn string
	switch {
	case err != nil:
		cfg = config.Default()
		warn = fmt.Sprintf("Konfiguration nicht lesbar, der Dienst läuft mit der Grundeinstellung: %v", err)
		s.loadFailed = true
	case info.UsedFallback:
		warn = fmt.Sprintf("Konfiguration defekt, Rückfall auf den letzten guten Stand: %s", info.Reason)
		s.usedFallback = true
	case info.FirstStart:
		// Die Kennung des Terminals muss einen Neustart überleben.
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
	s.cfg, s.cfgWarn = cloneConfig(cfg), warn
	s.mu.Unlock()
}

func (s *service) openBox() {
	s.box = secrets.Unavailable()
	err := os.MkdirAll(s.opts.DataDir, 0o700)
	if err == nil {
		var box *secrets.Box
		box, err = secrets.OpenBox(filepath.Join(s.opts.DataDir, secretsFile), hasSealedValues(s.config()) || s.loadFailed)
		if err == nil {
			s.box = box
		}
	}
	if err != nil {
		warn := fmt.Sprintf("Schlüsseldatei nicht verfügbar, VNC-Passwörter sind nicht lesbar: %v", err)
		log.Print(warn)
		s.mu.Lock()
		s.boxWarn = warn
		s.mu.Unlock()
	}
}

func (s *service) buildParts(cfg config.Config, key []byte) {
	s.coord = coord.New(coord.Settings{
		Self:        selfOf(cfg),
		Peers:       peersFor(cfg, key),
		RulesFor:    s.rulesFor,
		IsPermanent: s.isPermanent,
	}, coord.NewHTTPTransportFunc(s.peerKey, nil), coord.SystemClock{})
	s.hosts = newHostCache(
		func(ctx context.Context, host string) ([]net.IP, error) {
			return net.DefaultResolver.LookupIP(ctx, "ip", host)
		},
		func(ip net.IP, port int) (bool, error) { return platform.HasEstablished(procRoot, ip, port) },
	)
	s.mgr = session.New(session.Deps{
		Coord:    s.coord,
		Runner:   s.runner,
		Profile:  s.profile,
		TCP:      s.hosts.tcp,
		RAMDir:   s.ramDir,
		Clock:    session.SystemClock{},
		Config:   s.config,
		Password: s.password,
		Screen:   s.screen,
		Probe:    s.sessionProbe,
		OnEnd:    s.onSessionEnd,
		Blocked:  s.sessionBlocked,
	})
	s.reach = newReachability(
		func() []config.HMI { return s.config().HMIs },
		s.hmiInSession,
		rfb.TCPReachable,
	)
}

// serve läuft bis ctx endet. Der Automat startet vor den Servern, weil seine
// öffentlichen Methoden sonst blockieren.
func (s *service) serve(parent context.Context) error {
	defer s.close()
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	s.ctxMu.Lock()
	s.ctx = ctx
	s.ctxMu.Unlock()

	var wg sync.WaitGroup
	background := func(f func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f()
		}()
	}
	mgrDone := make(chan struct{})
	go func() {
		defer close(mgrDone)
		s.mgr.Run(ctx)
	}()
	s.syncMaintenance()
	background(func() { s.coord.Run(ctx) })
	background(func() { s.reach.run(ctx) })
	background(func() {
		if err := platform.WatchTouch(ctx, devInput, s.mgr.TouchActivity); err != nil && ctx.Err() == nil {
			log.Printf("Touch-Überwachung beendet: %v", err)
		}
	})
	// Chromium erst, wenn die Kachelseite angenommen wird: Seine Fehlerseite
	// lädt sonst nie von selbst nach.
	local, admin, localErr, bound, err := s.startServers(ctx, &wg)
	browserDone := closedChan()
	if err == nil {
		browserDone = s.startBrowser(ctx)
		err = s.announce(ctx, bound, background)
	}
	if err == nil {
		select {
		case <-ctx.Done():
		case err = <-localErr:
		}
	}
	cancel()
	s.soak.wait()
	if serr := s.notify.send("STOPPING=1"); serr != nil {
		log.Printf("Abmeldung bei systemd: %v", serr)
	}
	<-mgrDone
	<-browserDone
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

// announce schließt den Start ab: Alles läuft, also gilt der Stand als gut, die
// Marke für den Update-Wächter steht, und systemd erfährt es.
func (s *service) announce(ctx context.Context, bound Bound, background func(func())) error {
	pctx, cancel := context.WithTimeout(ctx, watchdogProbeLimit)
	defer cancel()
	if err := s.mgr.Ping(pctx); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("Sitzungsverwaltung antwortet nicht: %w", err)
	}
	cfg := s.config()
	// Ohne Passwort kein Rückfall: Dieser Stand öffnete sonst die Ersteinrichtung.
	if s.markGood && cfg.Admin.PasswordHash != "" {
		if err := s.store.MarkGood(); err != nil {
			log.Printf("Rückfallstand nicht gespeichert: %v", err)
		}
	}
	if err := writeAtomic(filepath.Join(s.opts.RunDir, healthyFile), []byte(release.Version), 0o644); err != nil {
		s.warn(fmt.Sprintf("Gesundmarke nicht geschrieben, ein Update fiele zurück: %v", err))
	}
	if err := s.notify.send("READY=1"); err != nil {
		log.Printf("Anmeldung bei systemd: %v", err)
	}
	if every := watchdogInterval(os.Getenv, os.Getpid()); every > 0 {
		background(func() { runWatchdog(ctx, s.notify, every, s.mgr.Ping) })
	}
	if _, _, found := platform.DisplaySize(sysRoot); !found {
		log.Print("Kein Monitor erkannt, die Auflösung kommt aus der Einstellung oder dem Standardwert")
	}
	log.Printf("Dienst läuft, Version %s", release.Version)
	if s.opts.Ready != nil {
		s.opts.Ready(bound)
	}
	return nil
}

// startBrowser liefert einen Kanal, der schließt, sobald Chromium beendet ist.
func (s *service) startBrowser(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	if !s.opts.ChromiumEnabled {
		close(done)
		return done
	}
	path, err := browser.FindChromium(s.lookPath)
	if err != nil {
		s.warn(fmt.Sprintf("Chromium nicht verfügbar: %v", err))
	}
	ram := filepath.Join(s.opts.RunDir, chromiumDir)
	if err := os.MkdirAll(ram, 0o700); err != nil {
		s.warn(fmt.Sprintf("RAM-Ordner für Chromium nicht verfügbar: %v", err))
	}
	sup := browser.New(s.runner, path, ram, func() int {
		w, _ := s.screen()
		return w
	})
	sup.SetProfile(s.sessionProfile())
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		sup.Run(ctx)
	}()
	go func() {
		defer wg.Done()
		s.feedBrowser(ctx, sup)
	}()
	go func() {
		wg.Wait()
		close(done)
	}()
	return done
}

// feedBrowser zeigt je Sitzungsstand Kacheln oder Leiste. Eine geänderte
// Einstellung löst das auch ohne neuen Stand aus.
func (s *service) feedBrowser(ctx context.Context, sup *browser.Supervisor) {
	snaps, stop := s.mgr.Subscribe()
	defer stop()
	var last session.Snapshot
	for {
		select {
		case <-ctx.Done():
			return
		case snap, ok := <-snaps:
			if !ok {
				return
			}
			last = snap
		case <-s.browser:
			sup.SetProfile(s.sessionProfile())
		}
		sup.Show(s.browserTarget(last))
	}
}

func (s *service) rootCtx() context.Context {
	s.ctxMu.Lock()
	defer s.ctxMu.Unlock()
	return s.ctx
}

// sessionProfile liefert das Grafikprofil der laufenden Sitzung. Die Einstellung
// wechselt die Sitzung erst beim Neustart; Chromium muss zur laufenden passen, sonst
// fehlt ihm die Anzeige. Ohne Datei (Start außerhalb des Abbilds) gilt die Einstellung.
func (s *service) sessionProfile() string {
	b, err := os.ReadFile(s.opts.SessionProfileFile)
	if err == nil {
		switch p := strings.TrimSpace(string(b)); p {
		case "labwc", "x11":
			return p
		default:
			log.Printf("Unbekanntes Grafikprofil %q in %s, verwende die Einstellung", p, s.opts.SessionProfileFile)
		}
	}
	return s.config().Display.GraphicsProfile
}

func closedChan() <-chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}

// hasSealedValues meldet, ob die Konfiguration mit der Schlüsseldatei verschlüsselte
// Werte enthält; ein neuer Schlüssel machte sie dann unlesbar.
func hasSealedValues(c config.Config) bool {
	if c.SharedKeyEnc != "" {
		return true
	}
	for _, h := range c.HMIs {
		if h.PasswordEnc != "" {
			return true
		}
	}
	return false
}
