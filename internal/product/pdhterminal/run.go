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
	"hmikiosk/internal/keyboard"
	"hmikiosk/internal/platform"
	"hmikiosk/internal/product/pdhterminal/card"
	"hmikiosk/internal/product/pdhterminal/notify"
	"hmikiosk/internal/product/pdhterminal/oled"
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
	// DefaultHelperSocket gilt unter Linux; den Vorgabewert liefert der Aufrufer (cmd/pdh-terminal).
	DefaultHelperSocket = "/run/hmi-kiosk-helper/helper.sock"

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
	// HelperSocket ist der Socket des root-Helfers; leer heisst: kein Helfer (Windows).
	HelperSocket    string
	ChromiumEnabled bool
	// TrayEnabled legt das Symbol im Infobereich an (nur Windows; sonst ohne Wirkung).
	TrayEnabled bool
	tray        tray // ersetzt das Symbol (für Tests)
	// HardwareID liest die Hardware-Kennung; nil heißt: aus dem Gerät lesen.
	HardwareID func() string
	// HelperCall ersetzt den Aufruf des root-Helfers für den Ursprungsabgleich (für Tests).
	HelperCall func(ctx context.Context, cmd, arg string) error
	// Ready wird gerufen, wenn der Dienst läuft (für Tests).
	Ready func(Bound)
}

// Bound nennt die tatsächlich gebundenen Adressen; Admin und LocalAdmin sind leer, wenn
// die Seite nicht gestartet werden konnte.
type Bound struct{ Local, Admin, LocalAdmin string }

// rootFS ist die Wurzel für Systemteile (Systemstand); Tests setzen einen Ordner.
var rootFS = "/"

// sessionProfileFile nennt das Grafikprofil der laufenden Sitzung; Tests setzen eine Datei.
var sessionProfileFile = "/run/hmi-kiosk-session/profile"

const (
	procRoot = "/proc"
	sysRoot  = "/sys"
	devInput = "/dev/input"
)

// restartDelay ist die Frist zwischen Update-Aktivierung und Neustart (Windows); Tests verkürzen sie.
var restartDelay = 2 * time.Second

// localAdminOrigin ist der Ursprung der Admin-Seite am Terminal (für den Systemstand).
var localAdminOrigin = strings.TrimSuffix(web.LocalAdminURL, "/admin/")

type service struct {
	o         Options
	store     *config.Store
	journal   *appcore.Journal
	box       *secrets.Box
	helper    *helperapi.Client
	origins   *appcore.OriginSync
	updates   *updatecheck.Service
	presence  appcore.AdminPresence
	client    Client
	cards     *card.Service                      // Kartenleser; sein Hub speist SSE und Admin-Seite
	display   *oled.Service                      // OLED-Statusdisplay
	sup       atomic.Pointer[browser.Supervisor] // ab startBrowser
	win       atomic.Pointer[window]             // Edge-Fenster (nur Windows), ab startEdge
	kbd       *keyboard.Supervisor               // wvkbd; nil ohne Chromium
	certSrc   atomic.Pointer[web.CertSource]
	adminAddr atomic.Pointer[string] // gebundene Adresse der Admin-Seite (auch nach spätem Binden)
	tray      atomic.Pointer[tray]   // Symbol im Infobereich, solange es läuft
	exit      chan error             // Bitte um ein Ende von serve (ErrQuit, ErrRestart)
	problems  map[string]string      // Gründe für die rote Färbung des Symbols (s.mu)

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
	s := &service{o: o, store: config.Open(o.DataDir), exit: make(chan error, 1)}
	if o.HelperSocket != "" {
		s.helper = helperapi.New(o.HelperSocket)
	}
	s.cards = card.NewService(card.NewHub(time.Now), log.Printf)
	s.display = oled.NewService(log.Printf)

	journal, err := appcore.NewJournal(filepath.Join(o.DataDir, logDirName))
	s.journal = journal
	log.SetOutput(journal)
	if err != nil {
		log.Printf("Protokoll nicht verfügbar: %v", err)
	}
	if err := os.MkdirAll(o.RunDir, 0o755); err != nil {
		s.warn(fmt.Sprintf("Laufzeitordner nicht verfügbar: %v", err))
	}
	call := o.HelperCall
	if call == nil {
		call = s.callHelper
	}
	s.origins = appcore.NewOriginSync(call)
	// Das Produkt steht ausdrücklich da: Ein leeres Produkt hieße HMI-Kiosk.
	s.updates = appcore.NewUpdateCheck(release.PDHTerminal, func() config.UpdateCfg { return s.config().Update }, nil)
	s.cleanupStartup()
	loadFailed := s.loadConfig()
	s.hwID = s.detectHardwareID()
	s.claimHardware(loadFailed)
	s.openBox(loadFailed)
	if o.ChromiumEnabled {
		s.initKeyboard()
	}
	// Die Richtlinie vom Helfer-Start gilt bis hierhin; abgeglichen wird trotzdem.
	s.origins.Set(config.PDHOrigins(s.config().PDH))
	return s, nil
}

// callHelper ruft den root-Helfer; ohne Helfer (Windows) gibt es nichts abzugleichen.
func (s *service) callHelper(ctx context.Context, cmd, arg string) error {
	if s.helper == nil {
		return nil
	}
	return s.helper.Call(ctx, cmd, arg)
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
	s.cards.Apply(cardSettings(c.Card))
	s.display.Apply(statusDisplaySettings(c.StatusDisplay))
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
	return platformHardwareID()
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
	// Der Kartenleser läuft unabhängig von Chromium; apply folgt späteren Änderungen.
	s.cards.Apply(cardSettings(s.config().Card))
	s.display.Apply(statusDisplaySettings(s.config().StatusDisplay))
	background(func() { s.origins.Run(ctx) })
	background(func() { appcore.RunUpdateCheck(ctx, s.updates) })

	// Die Startseite ist die Grundlage der Anzeige. Unter Linux endet der Dienst ohne sie (systemd startet ihn neu).
	// Unter Windows läuft er mit rotem Symbol weiter und bindet alle 30 s neu: Ein belegter Port ist ein Problem der
	// Umgebung, der Prozess selbst ist gesund (ein Ende würde der Starter als Fehlstart der Version zählen).
	l, err := appcore.ListenLoopback(s.o.LocalAddr)
	if err != nil && !staysUpWithoutStart {
		cancel()
		wg.Wait()
		return fmt.Errorf("Startseite: %w", err)
	}
	bound := Bound{}
	origin := func() string { return cardOrigin(s.config().PDH) }
	cardAPI := http.NewServeMux()
	cardAPI.Handle("/api/card/events", card.EventsHandler(s.cards.Hub(), s.cards, origin))
	cardAPI.Handle("/api/card/result", card.ResultHandler(origin, s.windowForeground, notify.New(s.raiseWindow)))
	startHandler := StartHandler(s.o.DataDir, func() config.PDHCfg { return s.config().PDH }, s.client, cardAPI)
	localErr := make(chan error, 1)
	serveLocal := func(l net.Listener) *http.Server {
		srv := appcore.NewLocalServer(ctx, startHandler)
		background(func() {
			if err := srv.Serve(l); !errors.Is(err, http.ErrServerClosed) {
				localErr <- fmt.Errorf("Startseite: %w", err)
			}
		})
		return srv
	}
	var local *http.Server
	if l != nil {
		bound.Local = l.Addr().String()
		local = serveLocal(l)
	} else {
		s.warn(fmt.Sprintf("Startseite nicht verfügbar: %v", err))
		s.setProblem(problemStart, portBusyText(s.o.LocalAddr, "Startseite nicht verfügbar"))
	}

	remote, onTerminal := web.NewAdminHandlers(s.adminDeps())
	adm := &adminListener{}
	adminAddr := s.startAdmin(&wg, remote, adm)
	bound.Admin = adminAddr
	if adminAddr == "" {
		background(func() { s.retryAdmin(ctx, &wg, remote, adm) })
	}
	stopLocalAdmin, err := appcore.ServeLocalAdmin(ctx, s.o.LocalAdminAddr, onTerminal, &s.presence)
	if err != nil {
		s.warn(fmt.Sprintf("Admin-Seite am Terminal nicht verfügbar: %v", err))
		s.setProblem(problemLocal, "Port belegt, Admin-Seite am Terminal nicht verfügbar")
	} else {
		bound.LocalAdmin = s.o.LocalAdminAddr
	}

	stopTray := s.startTray(ctx, bound, background)
	// Browser und späte Startseite (nach belegtem Port) teilen sich late; closed verhindert Neues nach dem Ende.
	var late struct {
		mu      sync.Mutex
		closed  bool
		srv     *http.Server
		browser <-chan struct{}
	}
	if l != nil {
		late.browser = s.startBrowser(ctx, "http://"+bound.Local+startPath)
	} else {
		background(func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-time.After(portRetryEvery):
				}
				nl, err := appcore.ListenLoopback(s.o.LocalAddr)
				if err != nil {
					continue
				}
				late.mu.Lock()
				if late.closed || ctx.Err() != nil {
					late.mu.Unlock()
					nl.Close()
					return
				}
				late.srv = serveLocal(nl)
				late.browser = s.startBrowser(ctx, "http://"+nl.Addr().String()+startPath)
				late.mu.Unlock()
				s.setProblem(problemStart, "")
				log.Printf("Startseite nach belegtem Port gebunden: %s", nl.Addr())
				return
			}
		})
	}
	s.announce(ctx, bound, background)

	err = nil
	select {
	case <-ctx.Done():
	case err = <-localErr:
	case err = <-s.exit:
	}
	cancel()
	stopTray()
	if serr := appcore.Notify("STOPPING=1"); serr != nil {
		log.Printf("Abmeldung bei systemd: %v", serr)
	}
	late.mu.Lock()
	late.closed = true
	lateSrv, browserDone := late.srv, late.browser
	late.mu.Unlock()
	if browserDone != nil {
		<-browserDone
	}
	if stopLocalAdmin != nil {
		stopLocalAdmin() // ServeLocalAdmin endet nicht mit ctx
	}
	shutdownServers(local, lateSrv, adm.server())
	wg.Wait()
	return err
}

func (s *service) close() {
	s.cards.Close()
	s.display.Close()
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
		if bound.Local == "" { // Startseite nicht gebunden (Windows, belegter Port): kein systemd-Takt
			return
		}
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
	if onWindows {
		return s.startEdge(ctx, startURL)
	}
	path, err := browser.FindChromium(nil)
	if err != nil {
		// Ohne Programm liefe ein Überwacher endlos in Neustarts: einmal melden, ohne Browser weiterlaufen.
		s.warn(fmt.Sprintf("Chromium nicht verfügbar, das Terminal läuft ohne Anzeige: %v", err))
		close(done)
		return done
	}
	ram := filepath.Join(s.o.RunDir, chromiumDir)
	if err := os.MkdirAll(ram, 0o700); err != nil {
		s.warn(fmt.Sprintf("RAM-Ordner für Chromium nicht verfügbar: %v", err))
	}
	sup := browser.NewExec(path, ram, func() int { w, _ := s.screen(); return w })
	sup.SetProfile(s.sessionProfile())
	sup.SetStartURL(startURL)
	sup.Show(browser.TargetStart)
	s.sup.Store(sup)

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		sup.Run(ctx)
	}()
	go func() {
		defer wg.Done()
		if s.kbd != nil {
			s.kbd.Run(ctx)
		}
	}()
	go func() {
		defer wg.Done()
		// Die Berührung oben rechts öffnet die Admin-Seite.
		w, h := s.screen()
		cfg := cornerConfig(w, h)
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

// cornerConfig ist das Feld für die Admin-Geste: 80 × 80 px oben rechts, 5 s Haltezeit.
func cornerConfig(w, h int) platform.CornerCfg {
	return platform.CornerCfg{ScreenW: w, ScreenH: h, RegionPx: 80, Hold: 5 * time.Second}
}

// localLeft: Die Admin-Seite wurde verlassen („Zurück“ oder Leerlauf). Der Browser kehrt
// zur Startseite zurück, sonst öffnete ein Neustart von Chromium wieder die Admin-Seite.
func (s *service) localLeft() {
	s.presence.Left()
	if s.kbd != nil {
		_ = s.kbd.Hide() // sonst stünde die Tastatur noch auf der Startseite
	}
	if sup := s.sup.Load(); sup != nil {
		sup.Show(browser.TargetStart)
	}
}

// sessionProfile liefert das Grafikprofil der laufenden Sitzung (hmi-kiosk-session prepare); Chromium
// muss zu ihr passen. Ohne Datei (Start außerhalb des Abbilds) gilt die Einstellung.
func (s *service) sessionProfile() string {
	if b, err := os.ReadFile(sessionProfileFile); err == nil {
		if p := strings.TrimSpace(string(b)); p == "labwc" || p == "x11" {
			return p
		}
	}
	return s.config().Display.GraphicsProfile
}

// screen liefert die Bildschirmgröße aus Einstellung oder Monitor.
func (s *service) screen() (int, int) {
	c := s.config()
	w, h, found := platform.DisplaySize(sysRoot)
	return platform.Resolve(c.Display.OverrideW, c.Display.OverrideH, w, h, found)
}

// adminListener hält den Admin-Server, der auch erst nach einem belegten Port starten kann.
type adminListener struct {
	mu       sync.Mutex
	srv      *http.Server
	closed   bool
	cert     tls.Certificate
	dir      string
	canRetry bool // das Zertifikat ist da; nur der Port fehlte
}

// set merkt den Server; nach dem Ende (server) nimmt es keinen mehr an.
func (a *adminListener) set(srv *http.Server) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return false
	}
	a.srv = srv
	return true
}

// server schließt die Annahme und liefert den Server (nil, wenn keiner lief).
func (a *adminListener) server() *http.Server {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closed = true
	return a.srv
}

// staysUpWithoutStart: Unter Windows läuft der Dienst ohne Startseite weiter (siehe serve); Tests schalten es auch unter Linux ein.
var staysUpWithoutStart = onWindows

// portRetryEvery ist die Frist, nach der ein belegter Port neu versucht wird; Tests verkürzen sie.
var portRetryEvery = 30 * time.Second

// portBusyText: „Port 8080 belegt, <folge>“.
func portBusyText(addr, consequence string) string {
	if _, p, err := net.SplitHostPort(addr); err == nil && p != "" {
		return fmt.Sprintf("Port %s belegt, %s", p, consequence)
	}
	return "Port belegt, " + consequence
}

// startAdmin: Fehlt die Admin-Seite über HTTPS, bleibt das Terminal bedienbar und meldet es. Die Adresse ist
// leer, wenn sie nicht lief.
func (s *service) startAdmin(wg *sync.WaitGroup, h http.Handler, adm *adminListener) string {
	cert, dir, ok := s.loadCert()
	if !ok {
		return ""
	}
	adm.cert, adm.dir, adm.canRetry = cert, dir, true
	return s.listenAdmin(wg, h, adm, false)
}

// retryAdmin bindet den Port der Admin-Seite alle portRetryEvery neu, bis es gelingt (oder ctx endet).
func (s *service) retryAdmin(ctx context.Context, wg *sync.WaitGroup, h http.Handler, adm *adminListener) {
	if !adm.canRetry {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(portRetryEvery):
		}
		if addr := s.listenAdmin(wg, h, adm, true); addr != "" {
			s.setProblem(problemAdmin, "")
			log.Printf("Admin-Seite nach belegtem Port gebunden: %s", addr)
			return
		}
	}
}

func (s *service) listenAdmin(wg *sync.WaitGroup, h http.Handler, adm *adminListener, quiet bool) string {
	l, err := net.Listen("tcp", s.o.AdminAddr)
	if err != nil {
		if !quiet {
			s.warn(fmt.Sprintf("Admin-Seite nicht verfügbar: %v", err))
		}
		s.setProblem(problemAdmin, portBusyText(s.o.AdminAddr, "Admin-Seite nicht verfügbar"))
		return ""
	}
	src := web.NewCertSource(adm.dir, adm.cert)
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
	if !adm.set(srv) {
		l.Close()
		return ""
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := srv.ServeTLS(l, "", ""); !errors.Is(err, http.ErrServerClosed) {
			s.warn(fmt.Sprintf("Admin-Seite beendet: %v", err))
		}
	}()
	addr := l.Addr().String()
	s.adminAddr.Store(&addr)
	return addr
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
	var kbd web.KeyboardAPI // nil-Schnittstelle, solange es keine Tastatur gibt
	if s.kbd != nil {
		kbd = s.kbd
	}
	// Ohne Helfer (Windows) bleiben die Schnittstellen nil-Schnittstellen, nicht nil-Zeiger darin.
	var helper web.HelperAPI
	var backup web.BackupAPI
	var netAPI web.NetAPI
	// Windows: Es gibt keinen Verweis umzuschalten. Der Starter wählt beim Neustart die höchste gültige Version;
	// die Aktivierung bittet nur darum, kurz verzögert, damit die Antwort der Admin-Seite noch den Browser erreicht.
	activate := func(context.Context, string) error {
		time.AfterFunc(restartDelay, func() { s.requestExit(ErrRestart) })
		return nil
	}
	if s.helper != nil {
		helper, backup, netAPI = s.helper, s.helper, s.helper
		activate = func(ctx context.Context, version string) error {
			return s.helper.Call(ctx, "update-activate", version)
		}
	}
	installer := &update.Installer{
		Root:      s.o.DataDir,
		PublicKey: release.PublicKey(),
		Product:   release.PDHTerminal,
		Platform:  release.CurrentPlatform(),
		Activate:  activate,
	}
	if s.helper != nil { // der Systemstand ist ein Linux-Begriff; ohne Helfer (Windows) gibt es keine Prüfung
		installer.SystemLevel = func() int { return platform.InstalledSystemLevel(rootFS, localAdminOrigin) }
	}
	return web.AdminDeps{
		Keyboard:             kbd,
		Store:                s.store,
		Box:                  s.box,
		Apply:                s.apply,
		ConfigMu:             &s.cfgMu,
		Helper:               helper,
		Status:               s.status,
		LogDir:               filepath.Join(s.o.DataDir, logDirName),
		RemoteIsWifi:         remoteIsWifi,
		EthernetLink:         func() bool { return platform.EthernetHasLink(sysRoot) },
		Update:               installer,
		UpdateCheck:          s.updates,
		Selftest:             s.adminSelftest,
		Backup:               backup,
		Net:                  netAPI,
		HardwareID:           func() string { return s.hwID },
		TLSDir:               filepath.Join(s.o.DataDir, tlsDirName),
		TLSFingerprint:       s.tlsFingerprint,
		KeyFile:              filepath.Join(s.o.DataDir, secretsFile),
		UpdateKeyFingerprint: release.Fingerprint(release.PublicKey()),
		LocalActive:          s.presence.Touch,
		LocalLeft:            s.localLeft,
		Product:              Admin(s.o.DataDir, s.client, s.cards, s.display),
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

// adminSelftest ist die Selbstprüfung für die Admin-Seite; die Schritte hängen von der
// Plattform ab (selftestSteps).
func (s *service) adminSelftest(ctx context.Context) []web.SelftestStep {
	d := selftest.Deps{
		Config: s.config,
	}
	if t := s.config().Card.Type; t != "" && t != "aus" {
		d.CardReader = s.cards.Probe
	}
	res := selftest.RunSteps(ctx, d, s.selftestSteps(&d))
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
