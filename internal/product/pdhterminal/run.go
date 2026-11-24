package pdhterminal

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"hmikiosk/internal/appcore"
	"hmikiosk/internal/browser"
	"hmikiosk/internal/config"
	"hmikiosk/internal/helperapi"
	"hmikiosk/internal/keyboard"
	"hmikiosk/internal/localport"
	"hmikiosk/internal/platform"
	"hmikiosk/internal/product/pdhterminal/card"
	"hmikiosk/internal/product/pdhterminal/notify"
	"hmikiosk/internal/product/pdhterminal/oled"
	"hmikiosk/internal/product/pdhterminal/slides"
	"hmikiosk/internal/release"
	"hmikiosk/internal/secrets"
	"hmikiosk/internal/selftest"
	"hmikiosk/internal/update"
	"hmikiosk/internal/updatecheck"
	"hmikiosk/internal/web"
	"hmikiosk/internal/winsignal"
)

// Vorgaben der Adressen und Pfade; sie gelten wie beim HMI-Kiosk, damit Helfer,
// Chromium-Richtlinie (feste Freigabe 8080/8081) und Einheiten gleich bleiben.
// DefaultLocalAddr und DefaultLocalAdminAddr sind die Grundwerte; ohne ausdrückliche Adresse gilt
// der Port aus der Konfiguration (Kiosk.LocalPort, siehe localport), und die lokale Admin-Seite liegt einen Port darüber.
const (
	DefaultLocalAddr      = "127.0.0.1:8080"
	DefaultAdminAddr      = ":8443"
	DefaultLocalAdminAddr = appcore.DefaultLocalAdminAddr
	// DefaultHelperSocket gilt unter Linux; den Vorgabewert liefert der Aufrufer (cmd/pdh-terminal).
	DefaultHelperSocket = "/run/hmi-kiosk-helper/helper.sock"

	secretsFile   = "secrets.key"
	logDirName    = "log"
	slidesDirName = "slides" // Zwischenspeicher der Folienschau unter dem Datenordner
	tlsDirName    = "tls"
	chromiumDir   = "chromium"
	shutdownLimit = 5 * time.Second
	startPath     = "/start"
)

// Options sind die Startwerte des Dienstes. Adressen dürfen Port 0 nennen.
type Options struct {
	DataDir, RunDir string
	LocalAddr       string // Startseite, nur Loopback; leer: 127.0.0.1 mit dem Port aus der Konfiguration (Grundwert 8080)
	AdminAddr       string // Admin-Seite über HTTPS
	LocalAdminAddr  string // Admin-Seite am Terminal, nur Loopback; leer: der Port der Startseite + 1 (Grundwert 8081)
	// HelperSocket ist der Socket des root-Helfers; leer heisst: kein Helfer (Windows).
	HelperSocket    string
	ChromiumEnabled bool
	// TrayEnabled legt das Symbol im Infobereich an (nur Windows; sonst ohne Wirkung).
	TrayEnabled bool
	tray        tray // ersetzt das Symbol (für Tests)
	// Kiosk: Windows-Kiosk-Benutzer (Starter mit -kiosk). Edge läuft im Vollbild-Kiosk und startet sichtbar
	// neu, es gibt weder Symbol im Infobereich noch Benachrichtigungen; die Admin-Geste (5 s oben rechts
	// halten) öffnet die lokale Admin-Seite, und diese bekommt den Abschnitt „Kiosk“.
	Kiosk bool
	// HardwareID liest die Hardware-Kennung; nil heißt: aus dem Gerät lesen.
	HardwareID func() string
	// HelperCall ersetzt den Aufruf des root-Helfers für den Ursprungsabgleich (für Tests).
	HelperCall func(ctx context.Context, cmd, arg string) error
	// OpenSignal ist der Name des Ereignisses, mit dem ein zweiter Start das Fenster anfordert (leer: unter
	// Windows winsignal.OpenName, sonst keines). OnOpen ersetzt „Terminal öffnen“ (für Tests).
	OpenSignal string
	OnOpen     func()
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

// localAdminOrigin ist der Ursprung der Admin-Seite am Terminal (für den Systemstand); er folgt dem eingestellten Port.
func localAdminOrigin() string { return strings.TrimSuffix(web.LocalAdminURL(), "/admin/") }

// helperSetLocalPort ist der Helferbefehl, der den Port der Startseite in die Chromium-Richtlinie schreibt
// (helper.CmdSetLocalPort; das Paket helper gehört dem root-Prozess und wird hier nicht eingebunden).
const helperSetLocalPort = "set-local-port"

// startPortHint ist die Folge, die bei belegtem oder gesperrtem Port der Startseite genannt wird.
const startPortHint = "Startseite nicht verfügbar – Port im Admin-Bereich ändern (PDH, Lokale Startseite)"

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
	route     *Route                             // Wegwahl Haupt-/Ausweichadresse; s.client trägt sie
	slides    *slides.Store                      // Zwischenspeicher der Folienschau (globales Terminal)
	funcs     *FuncSync                          // Abgleich der Funktionen mit PDH
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

	port         int          // Port der Startseite dieses Laufs (localport); ändert sich nur beim Start (Rückfall auf 8080)
	portSwitch   atomic.Bool  // ein Wechsel auf einen anderen Port ist eingeplant
	derivedAddrs bool         // Adressen der lokalen Seiten stammen aus dem Port der Konfiguration (nicht ausdrücklich angegeben)
	badPort      atomic.Int32 // gespeicherter Port, der sich nicht binden ließ (Rückfall auf 8080); kein Neustart deswegen
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
	o.AdminAddr = orDefault(o.AdminAddr, DefaultAdminAddr)
	s := &service{o: o, store: config.Open(o.DataDir), exit: make(chan error, 1)}
	if o.HelperSocket != "" {
		s.helper = helperapi.New(o.HelperSocket)
	}
	s.cards = card.NewService(card.NewHub(time.Now), log.Printf)
	s.slides = &slides.Store{Dir: filepath.Join(o.DataDir, slidesDirName)}
	s.route = NewRoute("", "")
	s.client = Client{Route: s.route}
	s.funcs = NewFuncSync(o.DataDir, s.client)
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
	// Der Port der Startseite kommt aus der Konfiguration (Grundwert 8080) und gilt für diesen Lauf.
	localport.Set(s.config().LocalPort())
	s.port = localport.Get()
	s.derivedAddrs = o.LocalAddr == "" && o.LocalAdminAddr == ""
	s.o.LocalAddr = orDefault(o.LocalAddr, localport.Addr())
	s.o.LocalAdminAddr = orDefault(o.LocalAdminAddr, localport.AdminAddr())
	s.hwID = s.detectHardwareID()
	s.claimHardware(loadFailed)
	s.openBox(loadFailed)
	if o.ChromiumEnabled {
		s.initKeyboard()
	}
	// Die Richtlinie vom Helfer-Start gilt bis hierhin; abgeglichen wird trotzdem.
	s.origins.Set(config.PDHOrigins(s.config().PDH))
	s.route.SetURLs(s.config().PDH.URL, s.config().PDH.FallbackURL)
	return s, nil
}

// probePrimary prüft für die Wegwahl die Hauptadresse mit dem Gerätegeheimnis.
func (s *service) probePrimary(ctx context.Context, base string) error {
	d, err := LoadDevice(s.o.DataDir)
	if err != nil || d == nil {
		return errUnreachable
	}
	return s.client.Probe(ctx, *d, base)
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
	s.route.SetURLs(c.PDH.URL, c.PDH.FallbackURL)
	s.cards.Apply(s.cardSettings(c.Card))
	s.display.Apply(statusDisplaySettings(c.StatusDisplay))
	s.portChanged(c.LocalPort())
}

// helperCall ruft den Helfer (in Tests ersetzt durch Options.HelperCall).
func (s *service) helperCall(ctx context.Context, cmd, arg string) error {
	if s.o.HelperCall != nil {
		return s.o.HelperCall(ctx, cmd, arg)
	}
	return s.callHelper(ctx, cmd, arg)
}

const policyPortTimeout = 15 * time.Second

// policySyncStartLimit: So lange wartet der Start auf den Abgleich der Richtlinie, bevor der Browser startet
// (Variable für Tests). Die Admin-Seite über HTTPS und die Startseite laufen da schon.
var policySyncStartLimit = 10 * time.Second

// browserStartHook (Tests) meldet jeden Browserstart mit der Adresse.
var browserStartHook func(startURL string)

// syncPolicyPort schreibt den Port der Startseite in die Chromium-Richtlinie (Linux; unter Windows gibt es keinen
// Helfer und keine Richtlinie). Der Vermerk ist während des Aufrufs ungültig (setPolicyPort).
func (s *service) syncPolicyPort(ctx context.Context, port int) error {
	cctx, cancel := context.WithTimeout(ctx, policyPortTimeout)
	defer cancel()
	return setPolicyPort(cctx, s.o.DataDir, s.helperCall, port)
}

// setPolicyPort ruft set-local-port. Der Vermerk wird vorher ungültig und erst nach bestätigtem Erfolg auf den Port
// gesetzt: Antwortet der Helfer zu spät oder mit Fehler, kann er die Richtlinie trotzdem geschrieben haben, und der
// nächste Start bzw. das nächste Speichern gleicht dann erneut ab.
func setPolicyPort(ctx context.Context, dataDir string, call func(context.Context, string, string) error, port int) error {
	invalidatePolicyPortMarker(dataDir)
	if err := call(ctx, helperSetLocalPort, strconv.Itoa(port)); err != nil {
		return err
	}
	setPolicyPortMarker(dataDir, port)
	return nil
}

// syncPolicyAtStart gleicht die Richtlinie beim Start mit dem Port ab, mit dem dieser Lauf wirklich arbeitet (auch
// nach dem Rückfall auf 8080). Synchron und vor dem Browser: Chromium darf die Startseite nicht laden, bevor die
// Richtlinie ihren Port erlaubt. Misslingt es oder dauert es länger als policySyncStartLimit, startet der Browser
// trotzdem. Jeder Fehlschlag ist sichtbar (Warnung und Problemanzeige), auch beim Grundwert: Nach einem Rückfall oder
// einem früher gescheiterten Rückwechsel stünde die Richtlinie sonst unbemerkt auf dem falschen Port.
func (s *service) syncPolicyAtStart(ctx context.Context, fellBack bool) {
	port := s.port
	if !fellBack && !s.needsPolicySync(port) {
		return
	}
	sctx, cancel := context.WithTimeout(ctx, policySyncStartLimit)
	defer cancel()
	err := s.syncPolicyPort(sctx, port)
	if err == nil || ctx.Err() != nil {
		return
	}
	s.warn(fmt.Sprintf("Die Chromium-Richtlinie erlaubt den Port %d der Startseite womöglich nicht: %v", port, err))
	s.setProblem(problemPolicy, fmt.Sprintf("Browser-Richtlinie nicht auf Port %d gesetzt – Startseite womöglich gesperrt (Systemteile aktualisieren)", port))
}

// policyPortFile merkt sich den Port, den der Helfer zuletzt bestätigt in die Richtlinie geschrieben hat. Der Dienst
// ruft den Helfer beim Start nur, wenn der Port vom Grundwert abweicht, die Richtlinie auf einem anderen steht oder
// der Vermerk ungültig ist (ein Aufruf lief, ohne dass sein Erfolg bestätigt wurde). Fehlt der Vermerk, wurde nie ein
// Aufruf begonnen: Es gilt die Richtlinie des Pakets (8080).
const policyPortFile = "policy-port"

// policyPortUnknown steht im Vermerk, solange ein Aufruf von set-local-port nicht bestätigt ist.
const policyPortUnknown = "unbekannt"

// policyMarker liefert den Vermerk: present, ob die Datei da ist; ok, ob sie einen gültigen Port enthält.
func policyMarker(dataDir string) (port int, present, ok bool) {
	b, err := os.ReadFile(filepath.Join(dataDir, policyPortFile))
	if errors.Is(err, fs.ErrNotExist) {
		return 0, false, false
	}
	if err != nil {
		return 0, true, false
	}
	p, perr := strconv.Atoi(strings.TrimSpace(string(b)))
	return p, true, perr == nil && p > 0
}

// policyMatches: Steht die Richtlinie bestätigt auf port? Ohne Vermerk gilt die des Pakets (Grundwert).
func policyMatches(dataDir string, port int) bool {
	m, present, ok := policyMarker(dataDir)
	if !present {
		return port == localport.Default
	}
	return ok && m == port
}

// policyMarkerInvalid: Ein Aufruf von set-local-port lief ohne bestätigten Erfolg.
func policyMarkerInvalid(dataDir string) bool {
	_, present, ok := policyMarker(dataDir)
	return present && !ok
}

func invalidatePolicyPortMarker(dataDir string) {
	if err := os.WriteFile(filepath.Join(dataDir, policyPortFile), []byte(policyPortUnknown+"\n"), 0o600); err != nil {
		log.Printf("Vermerk der Richtlinie nicht als ungültig markiert: %v", err)
	}
}

func policyPortMarker(dataDir string) (int, bool) {
	b, err := os.ReadFile(filepath.Join(dataDir, policyPortFile))
	if err != nil {
		return 0, false
	}
	p, err := strconv.Atoi(strings.TrimSpace(string(b)))
	return p, err == nil
}

func setPolicyPortMarker(dataDir string, port int) {
	if err := os.WriteFile(filepath.Join(dataDir, policyPortFile), []byte(strconv.Itoa(port)+"\n"), 0o600); err != nil {
		log.Printf("Port der Richtlinie nicht vermerkt: %v", err)
	}
}

// needsPolicySync: Beim Grundwert bleibt die Richtlinie des Pakets, solange nie ein Aufruf begonnen wurde; ein
// ungültiger oder abweichender Vermerk löst den Abgleich aus.
func (s *service) needsPolicySync(port int) bool {
	if port != localport.Default {
		return true
	}
	return !policyMatches(s.o.DataDir, port)
}

// portChanged: Hat die gespeicherte Konfiguration einen anderen Port als dieser Lauf, schreibt der Dienst die
// Richtlinie neu und startet danach selbst neu (kurz verzögert, damit die Antwort der Admin-Seite noch ankommt).
// Misslingt das Schreiben der Richtlinie, läuft das Terminal auf dem alten Port weiter: Chromium bliebe sonst auf dem
// neuen Port gesperrt. Maßgeblich ist der Port, an den die Startseite in diesem Lauf gebunden ist (s.port); badPort
// unterdrückt nur den erneuten Versuch mit dem Port, der beim Start nicht band, solange kein Speichern gelang
// (savedLocalPort setzt ihn zurück).
func (s *service) portChanged(want int) {
	if want == s.port || (want != 0 && int32(want) == s.badPort.Load()) || !s.portSwitch.CompareAndSwap(false, true) {
		return
	}
	time.AfterFunc(restartDelay, func() {
		defer s.portSwitch.Store(false)
		want := s.config().LocalPort()
		if want == s.port {
			return
		}
		if err := s.syncPolicyPort(context.Background(), want); err != nil {
			s.warn(fmt.Sprintf("Port der Startseite %d nicht übernommen, das Terminal läuft weiter auf Port %d: %v", want, s.port, err))
			return
		}
		log.Printf("Port der Startseite wechselt von %d auf %d, das Terminal startet neu", s.port, want)
		s.requestExit(ErrRestart)
	})
}

// savedLocalPort: Das Speichern im Admin-Bereich hat Bindetest und Richtlinie bestanden. Ein früher gescheiterter Port
// hält dann keinen Neustart mehr zurück, und das Problem des Abgleichs beim Start ist erledigt.
func (s *service) savedLocalPort() {
	s.badPort.Store(0)
	s.setProblem(problemPolicy, "")
}

// localState beschreibt, welchen Port die Startseite tatsächlich benutzt (für die Admin-Seite).
type localState struct {
	Port    int    // Port der Startseite dieses Laufs; die lokale Admin-Seite liegt einen darüber
	Failed  int    // gespeicherter Port, der sich nicht binden ließ (Rückfall auf 8080)
	Problem string // Grund, wenn die Startseite oder die Admin-Seite am Terminal nicht gebunden ist
}

func (s *service) localState() localState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var parts []string
	for _, k := range []string{problemStart, problemLocal, problemPolicy} {
		if m := s.problems[k]; m != "" {
			parts = append(parts, m)
		}
	}
	return localState{Port: s.port, Failed: int(s.badPort.Load()), Problem: strings.Join(parts, "; ")}
}

// adminPort ist der Port der Admin-Seite über HTTPS (gebunden, sonst aus der Adresse, sonst 8443).
func (s *service) adminPort() int {
	addr := s.o.AdminAddr
	if p := s.adminAddr.Load(); p != nil {
		addr = *p
	}
	if _, ps, err := net.SplitHostPort(addr); err == nil {
		if p, err := strconv.Atoi(ps); err == nil && p > 0 {
			return p
		}
	}
	return config.DefaultAdminPort
}

// cardSettings liefert die Leser-Einstellungen: Mit Zwischenspeicher gilt das Terminal als globales
// Terminal, und der Leser bleibt aus.
func (s *service) cardSettings(c config.CardCfg) card.Settings {
	cs := cardSettings(c)
	cs.Global = s.slides.Present()
	return cs
}

// applyCards übernimmt den Zustand des Zwischenspeichers (Leser aus oder wieder an) ohne Neustart.
func (s *service) applyCards() { s.cards.Apply(s.cardSettings(s.config().Card)) }

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
	s.cards.Apply(s.cardSettings(s.config().Card))
	s.display.Apply(statusDisplaySettings(s.config().StatusDisplay))
	background(func() { s.origins.Run(ctx) })
	background(func() { appcore.RunUpdateCheck(ctx, s.updates) })
	background(func() { s.funcs.Run(ctx) })
	background(func() { s.route.Run(ctx, s.probePrimary) })

	// Die Startseite ist die Grundlage der Anzeige, aber nie Bedingung für die Admin-Seite über HTTPS: Ein nicht
	// bindbarer Port (belegt, von Windows reserviert, vom Admin falsch eingestellt) ist ein Problem der Umgebung.
	// Der Dienst läuft dann mit rotem Symbol bzw. Warnung weiter und bindet alle 30 s neu (ein Ende würde unter
	// Windows der Starter als Fehlstart zählen, unter Linux startete systemd ohne Ende neu und die Admin-Seite käme nie hoch).
	// Ein eingestellter Port, der sich nicht binden lässt, fällt auf 8080 zurück, wenn 8080 frei ist.
	l, err := appcore.ListenLoopback(s.o.LocalAddr)
	fellBack := false
	if err != nil && s.derivedAddrs && s.port != localport.Default {
		if fl, ferr := appcore.ListenLoopback("127.0.0.1:" + strconv.Itoa(localport.Default)); ferr == nil {
			s.warn(fmt.Sprintf("Eingestellter Port %d nicht verfügbar, läuft auf %d: %v", s.port, localport.Default, err))
			s.badPort.Store(int32(s.port))
			s.mu.Lock()
			s.port = localport.Default
			s.mu.Unlock()
			localport.Set(0)
			s.o.LocalAddr, s.o.LocalAdminAddr = localport.Addr(), localport.AdminAddr()
			l, err, fellBack = fl, nil, true
		}
	}
	bound := Bound{}
	origin := func() string { return cardOrigin(s.config().PDH) }
	cardAPI := http.NewServeMux()
	cardAPI.Handle("/api/card/events", card.EventsHandler(s.cards.Hub(), s.cards, origin))
	cardAPI.Handle("/api/card/result", card.ResultHandler(origin, s.windowForeground, s.notifier()))
	cardAPI.Handle("/api/slides/cache", slides.CacheHandler(s.slides, origin, s.applyCards))
	cardAPI.Handle("/api/keyboard/{action}", keyboardPageHandler(origin, web.KeyboardHandler(s.keyboardAPI())))
	startHandler := StartHandler(s.o.DataDir, func() config.PDHCfg { return s.config().PDH }, s.client, cardAPI, s.slides)
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
	// Reihenfolge: Admin-Seite über HTTPS, Startseite, Abgleich der Richtlinie (begrenzt), Browser. Ein hängender
	// Helfer hält so nur den Browser auf, nie die Admin-Seite.
	remote, onTerminal := web.NewAdminHandlers(s.adminDeps())
	adm := &adminListener{}
	adminAddr := s.startAdmin(&wg, remote, adm)
	bound.Admin = adminAddr
	if adminAddr == "" {
		background(func() { s.retryAdmin(ctx, &wg, remote, adm) })
	}
	var local *http.Server
	if l != nil {
		bound.Local = l.Addr().String()
		local = serveLocal(l)
	} else {
		s.warn(fmt.Sprintf("Startseite nicht verfügbar: %v. Der Port lässt sich im Admin-Bereich ändern (PDH, Lokale Startseite).", err))
		s.setProblem(problemStart, portBusyText(s.o.LocalAddr, startPortHint))
	}
	stopLocalAdmin, err := appcore.ServeLocalAdmin(ctx, s.o.LocalAdminAddr, onTerminal, &s.presence)
	if err != nil {
		s.warn(fmt.Sprintf("Admin-Seite am Terminal nicht verfügbar: %v. Der Port der Startseite lässt sich im Admin-Bereich ändern (PDH, Lokale Startseite), die Admin-Seite liegt einen Port darüber.", err))
		s.setProblem(problemLocal, portBusyText(s.o.LocalAdminAddr, "Admin-Seite am Terminal nicht verfügbar – Port im Admin-Bereich ändern (PDH, Lokale Startseite)"))
	} else {
		bound.LocalAdmin = s.o.LocalAdminAddr
	}

	stopTray := s.startTray(ctx, bound, background)
	if s.o.Kiosk && onWindows {
		background(func() { s.watchAdminGesture(ctx) })
	}
	if name := s.openSignalName(); name != "" {
		background(func() {
			open := func() {
				if s.o.OnOpen != nil {
					s.o.OnOpen()
					return
				}
				s.openTerminal(ctx)
			}
			if err := winsignal.Listen(ctx, name, open); err != nil {
				log.Printf("Öffnen durch einen zweiten Start nicht möglich: %v", err)
			}
		})
	}
	s.syncPolicyAtStart(ctx, fellBack)
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
		if bound.Local == "" { // Startseite nicht gebunden: Der Dienst lebt (HTTPS-Admin), systemd bekommt den Takt ohne Probe
			appcore.Watchdog(ctx)
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
	if browserStartHook != nil {
		browserStartHook(startURL)
	}
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

// notifier liefert die Benachrichtigung für Kartenergebnisse; im Kiosk ist sie aus, weil das Terminal immer
// vorne liegt.
func (s *service) notifier() notify.Shower {
	if s.o.Kiosk {
		return noNotify{}
	}
	return notify.New(s.raiseWindow)
}

type noNotify struct{}

func (noNotify) Show([]string, string, string) {}

// adminGesture: Die Admin-Geste wurde erkannt. Der Browser (Edge im Kiosk) zeigt die lokale Admin-Seite.
func (s *service) adminGesture() {
	log.Print("Admin-Geste erkannt, die Admin-Seite wird geöffnet")
	if sup := s.sup.Load(); sup != nil {
		sup.Show(browser.TargetAdmin)
	}
}

// watchAdminGesture überwacht die Maus (Berührung wirkt als Maus) auf die Admin-Geste, bis ctx endet.
// Unter Windows misst der Hook den Bildschirm selbst; deshalb gibt cornerConfig(0, 0) keine Größe vor.
func (s *service) watchAdminGesture(ctx context.Context) {
	log.Print("Admin-Geste: Maus-Hook wird angelegt")
	if err := platform.WatchCorner(ctx, devInput, cornerConfig(0, 0), s.adminGesture); err != nil && ctx.Err() == nil {
		log.Printf("Admin-Geste nicht verfügbar: %v", err)
	}
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

// portRetryEvery ist die Frist, nach der ein belegter Port neu versucht wird; Tests verkürzen sie.
var portRetryEvery = 30 * time.Second

// portBusyText: „Port 8080 belegt, <folge>“ (der tatsächliche Port aus der Adresse).
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

// adminRootMux: Die kurze Adresse https://<Gerät>:8443 führt zur Admin-Seite.
func adminRootMux(h http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/admin/", h)
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/", http.StatusFound)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "Seite nicht gefunden", http.StatusNotFound)
	})
	return mux
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
	srv := &http.Server{
		Handler:           adminRootMux(h),
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
	if _, port, err := net.SplitHostPort(addr); err == nil {
		SetAdminPort(port)
	}
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

// keyboardAPI liefert die Bildschirmtastatur als Schnittstelle; ohne Tastatur eine nil-Schnittstelle.
func (s *service) keyboardAPI() web.KeyboardAPI {
	if s.kbd == nil {
		return nil
	}
	return s.kbd
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
		installer.SystemLevel = func() int { return platform.InstalledSystemLevel(rootFS, localAdminOrigin()) }
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
		Product:              s.adminProduct(),
	}
}

// adminProduct beschreibt das Terminal für den Admin-Rahmen; im Kiosk (Windows) mit dem Abschnitt „Kiosk“.
func (s *service) adminProduct() *web.AdminProduct {
	p := newAdminProduct(s.o.DataDir, s.client, s.cards, s.display)
	p.funcs = s.funcs
	p.local, p.adminPort, p.saved = s.localState, s.adminPort, s.savedLocalPort
	if s.o.Kiosk && onWindows {
		p.kiosk, p.logoff = true, logoffUser
	}
	return p.product()
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

// openSignalName: Das Ereignis, auf das ein zweiter Start (Startmenü, Benachrichtigung) das Fenster anfordert.
func (s *service) openSignalName() string {
	if s.o.OpenSignal != "" {
		return s.o.OpenSignal
	}
	if onWindows && s.o.ChromiumEnabled {
		return winsignal.OpenName
	}
	return ""
}
