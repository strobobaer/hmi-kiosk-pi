package web

import (
	"bytes"
	"context"
	"errors"
	"html/template"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"hmikiosk/internal/config"
	"hmikiosk/internal/coord"
	"hmikiosk/internal/rfb"
	"hmikiosk/internal/secrets"
)

// HelperAPI ruft den root-Helfer.
type HelperAPI interface {
	Call(ctx context.Context, cmd string, arg string) error
}

// UpdateAPI installiert ein hochgeladenes Programmpaket.
type UpdateAPI interface {
	Install(ctx context.Context, blob []byte) (version string, err error)
}

// SystemStatus sind die Anzeigewerte im Bereich System.
type SystemStatus struct {
	Version                       string
	OverlayActive                 bool
	DataFreeBytes, DataTotalBytes int64
	Display                       string
	Warnings                      []string
}

// SelftestStep ist ein Ergebnis der Selbstprüfung.
type SelftestStep struct {
	Name   string
	OK     bool
	Detail string
}

// AdminDeps sind die Abhängigkeiten der Admin-Seite.
type AdminDeps struct {
	Store        *config.Store
	Box          *secrets.Box
	Apply        func(config.Config)
	Probe        func(ctx context.Context, h config.HMI) rfb.ProbeResult
	Helper       HelperAPI
	Status       func() SystemStatus
	LogDir       string
	RemoteIsWifi func(*http.Request) bool
	EthernetLink func() bool
	Update       UpdateAPI
	Selftest     func(ctx context.Context) []SelftestStep
	Clock        coord.Clock
}

const formBodyLimit = 64 << 10

// Variablen statt Konstanten, damit der Test ohne 200 MB Speicher auskommt.
var (
	updateBodyLimit int64 = 200 << 20
	importBodyLimit int64 = 1 << 20
)

// Der Prüfaufwand ist teuer (64 MB je Lauf); die Tests ersetzen ihn.
var (
	hashPassword  = secrets.HashPassword
	checkPassword = secrets.CheckPassword
)

type routeKind int

const (
	routeAsset   routeKind = iota // ohne Anmeldung, nur Stil und Skript
	routeSetup                    // nur solange kein Passwort gesetzt ist
	routeLogin                    // Passwort gesetzt, ohne Sitzung
	routeSession                  // Sitzung und CSRF
	routeUpload                   // Sitzung und CSRF als erstes multipart-Feld
)

type route struct {
	method, path string // method leer: alle Methoden
	kind         routeKind
	limit        int64
	handle       func(http.ResponseWriter, *http.Request, sessionRef)
	upload       func(http.ResponseWriter, *http.Request, sessionRef, *multipart.Reader)
}

type admin struct {
	d        AdminDeps
	sessions *sessionStore
	lock     lockout
	// authSlot lässt nur eine teure Passwortoperation zugleich zu: Sonst passieren
	// parallele Anfragen die Sperre und belegen je 64 MB.
	authSlot chan struct{}
	// cfgMu hält Laden, Ändern, Speichern und Apply als eine Einheit zusammen.
	cfgMu      sync.Mutex
	updateMu   sync.Mutex
	selftestMu sync.Mutex
	pages      map[string]*template.Template
	static     fs.FS
	h          http.Handler
}

// NewAdmin baut den Handler der Admin-Seite. Alle Routen liegen unter /admin/.
func NewAdmin(d AdminDeps) http.Handler { return newAdmin(d).handler() }

func newAdmin(d AdminDeps) *admin {
	if d.Clock == nil {
		d.Clock = coord.SystemClock{}
	}
	sub, err := fs.Sub(assets, "assets/admin")
	if err != nil {
		panic(err)
	}
	a := &admin{
		d: d, static: sub, sessions: newSessionStore(), authSlot: make(chan struct{}, 1),
		pages: parseAdminPages(sub),
	}
	mux := http.NewServeMux()
	for _, rt := range a.routes() {
		pattern := rt.path
		if rt.method != "" {
			pattern = rt.method + " " + rt.path
		}
		mux.Handle(pattern, a.wrap(rt))
	}
	a.h = mux
	return a
}

func (a *admin) handler() http.Handler { return a.h }

func (a *admin) routes() []route {
	public := func(method, path string, kind routeKind, h func(http.ResponseWriter, *http.Request, sessionRef)) route {
		return route{method: method, path: path, kind: kind, limit: formBodyLimit, handle: h}
	}
	get := func(path string, h func(http.ResponseWriter, *http.Request, sessionRef)) route {
		return route{method: http.MethodGet, path: path, kind: routeSession, handle: h}
	}
	post := func(path string, h func(http.ResponseWriter, *http.Request, sessionRef)) route {
		return route{method: http.MethodPost, path: path, kind: routeSession, limit: formBodyLimit, handle: h}
	}
	upload := func(path string, limit int64, h func(http.ResponseWriter, *http.Request, sessionRef, *multipart.Reader)) route {
		return route{method: http.MethodPost, path: path, kind: routeUpload, limit: limit, upload: h}
	}
	return []route{
		public(http.MethodGet, "/admin/admin.css", routeAsset, a.asset("admin.css", "text/css; charset=utf-8")),
		public(http.MethodGet, "/admin/admin.js", routeAsset, a.asset("admin.js", "text/javascript; charset=utf-8")),
		public(http.MethodGet, "/admin/setup", routeSetup, a.handleSetupForm),
		public(http.MethodPost, "/admin/setup", routeSetup, a.handleSetup),
		public(http.MethodGet, "/admin/login", routeLogin, a.handleLoginForm),
		public(http.MethodPost, "/admin/login", routeLogin, a.handleLogin),

		get("/admin/{$}", func(w http.ResponseWriter, r *http.Request, _ sessionRef) { redirect(w, r, "/admin/hmis") }),
		post("/admin/logout", a.handleLogout),

		get("/admin/hmis", a.handleHMIList),
		get("/admin/hmis/new", a.handleHMINewForm),
		post("/admin/hmis/new", a.handleHMICreate),
		get("/admin/hmis/{id}", a.handleHMIEditForm),
		post("/admin/hmis/{id}", a.handleHMISave),
		post("/admin/hmis/{id}/delete", a.handleHMIDelete),
		post("/admin/hmis/{id}/probe", a.handleHMIProbe),

		get("/admin/client", a.handleClientForm),
		post("/admin/client", a.handleClientSave),
		get("/admin/kiosk", a.handleKioskForm),
		post("/admin/kiosk", a.handleKioskSave),
		get("/admin/terminals", a.handleTerminalsForm),
		post("/admin/terminals", a.handleTerminalsSave),
		post("/admin/terminals/generate-key", a.handleKeyGenerate),
		post("/admin/terminals/key", a.handleKeySet),

		get("/admin/network", a.handleNetworkPage),
		post("/admin/network/ntp", a.handleNTP),
		post("/admin/network/wifi-off", a.handleWifiOff),
		post("/admin/network/wifi-on", a.handleWifiOn),
		post("/admin/network/bt-off", a.handleBtOff),
		post("/admin/network/bt-on", a.handleBtOn),

		get("/admin/system", a.handleSystemPage),
		get("/admin/system/log", a.handleLogDownload),
		post("/admin/system/display", a.handleDisplay),
		post("/admin/system/time", a.handleSetTime),
		post("/admin/system/reboot", a.handleReboot),
		post("/admin/system/restart-service", a.handleRestartService),
		post("/admin/system/selftest", a.handleSelftest),
		post("/admin/system/password", a.handleChangePassword),
		post("/admin/system/export", a.handleExport),
		upload("/admin/system/update", updateBodyLimit, a.handleUpdate),
		upload("/admin/system/import", importBodyLimit, a.handleImport),

		// Auffangroute: auch unbekannte Pfade verraten ohne Anmeldung nichts.
		{path: "/admin/", kind: routeSession, limit: formBodyLimit, handle: func(w http.ResponseWriter, r *http.Request, _ sessionRef) {
			notFound(w)
		}},
	}
}

// wrap setzt die Prüfungen in fester Reihenfolge um. Fehlende Anmeldung geht vor
// CSRF, damit Unbefugte nur eine Weiterleitung sehen.
func (a *admin) wrap(rt route) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setAdminHeaders(w)
		if isWrite(r) && !sameOrigin(r) {
			http.Error(w, "Anfrage nicht erlaubt", http.StatusForbidden)
			return
		}
		if rt.kind == routeAsset {
			rt.handle(w, r, sessionRef{})
			return
		}
		cfg, info, err := a.d.Store.Load()
		if err != nil || setupClosedByFallback(cfg, info) {
			http.Error(w, "Konfiguration nicht lesbar", http.StatusInternalServerError)
			return
		}
		noPassword := cfg.Admin.PasswordHash == ""
		switch {
		case rt.kind == routeSetup && !noPassword:
			notFound(w)
			return
		case rt.kind != routeSetup && noPassword:
			redirect(w, r, "/admin/setup")
			return
		}
		if rt.kind == routeSetup || rt.kind == routeLogin {
			if isWrite(r) && !a.parseForm(w, r, rt.limit) {
				return
			}
			if isWrite(r) && !checkPreToken(r) {
				rejectCSRF(w)
				return
			}
			rt.handle(w, r, sessionRef{})
			return
		}
		s, ok := a.sessionFrom(r)
		if !ok {
			redirect(w, r, "/admin/login")
			return
		}
		if rt.kind == routeUpload {
			a.serveUpload(w, r, rt, s)
			return
		}
		if isWrite(r) {
			if !a.parseForm(w, r, rt.limit) {
				return
			}
			if !tokensEqual(r.PostForm.Get("csrf"), s.csrf) {
				rejectCSRF(w)
				return
			}
		}
		rt.handle(w, r, s)
	})
}

// setupClosedByFallback erkennt einen Rückfallstand ohne Passwort. Die aktuelle
// Datei ist dann verloren oder defekt und kann durchaus ein Passwort enthalten
// haben; die Ersteinrichtung stünde sonst jedem im Anlagennetz offen.
func setupClosedByFallback(c config.Config, info config.LoadInfo) bool {
	return info.UsedFallback && c.Admin.PasswordHash == ""
}

func notFound(w http.ResponseWriter) {
	http.Error(w, "Seite nicht gefunden", http.StatusNotFound)
}

func isWrite(r *http.Request) bool { return r.Method != http.MethodGet && r.Method != http.MethodHead }

func setAdminHeaders(w http.ResponseWriter) {
	h := w.Header()
	// Wie local.go, aber Formulare müssen hier abgeschickt werden können.
	h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
}

// sameOrigin prüft Origin gegen den Host, falls der Browser ihn mitschickt.
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || !strings.EqualFold(u.Host, r.Host) {
		return false
	}
	return r.TLS == nil || u.Scheme == "https"
}

// parseForm liest nur den Rumpf (r.PostForm), nie Werte aus der URL.
func (a *admin) parseForm(w http.ResponseWriter, r *http.Request, limit int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	err := r.ParseForm()
	if err == nil {
		return true
	}
	if isTooLarge(err) {
		http.Error(w, "Anfrage zu groß", http.StatusRequestEntityTooLarge)
	} else {
		http.Error(w, "Anfrage ungültig", http.StatusBadRequest)
	}
	return false
}

func isTooLarge(err error) bool {
	var tooLarge *http.MaxBytesError
	return errors.As(err, &tooLarge)
}

func rejectCSRF(w http.ResponseWriter) {
	http.Error(w, "Anfrage abgelehnt. Bitte die Seite neu laden und noch einmal versuchen.", http.StatusForbidden)
}

func redirect(w http.ResponseWriter, r *http.Request, path string) {
	http.Redirect(w, r, path, http.StatusSeeOther)
}

func (a *admin) asset(name, ctype string) func(http.ResponseWriter, *http.Request, sessionRef) {
	return func(w http.ResponseWriter, r *http.Request, _ sessionRef) {
		b, err := fs.ReadFile(a.static, name)
		if err != nil {
			notFound(w)
			return
		}
		w.Header().Set("Content-Type", ctype)
		_, _ = w.Write(b)
	}
}

// --- Speichern ---

// saveFailed heißt: Die Datei wurde nicht geschrieben, es gilt der alte Stand.
type saveFailed struct{ err error }

func (e saveFailed) Error() string {
	return "Speichern fehlgeschlagen, es bleiben die bisherigen Einstellungen in Kraft (" + e.err.Error() + ")."
}

// errConfigUnreadable nennt bewusst weder Datei noch Pfad: Die Meldung kann auch
// auf der Anmeldeseite erscheinen.
var errConfigUnreadable = errors.New("Die Konfiguration ist nicht lesbar, es bleiben die bisherigen Einstellungen in Kraft.")

// publicMessage ist der Text für Seiten ohne Anmeldung, ohne technische Details.
func publicMessage(err error) string {
	var sf saveFailed
	if errors.As(err, &sf) {
		return "Speichern fehlgeschlagen, es bleiben die bisherigen Einstellungen in Kraft."
	}
	return err.Error()
}

// update ist der einzige Schreibweg: laden, ändern, prüfen, speichern, Apply.
func (a *admin) update(change func(*config.Config) error) error {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	return a.updateLocked(change)
}

func (a *admin) updateLocked(change func(*config.Config) error) error {
	cfg, info, err := a.d.Store.Load()
	if err != nil || setupClosedByFallback(cfg, info) {
		return errConfigUnreadable
	}
	if err := change(&cfg); err != nil {
		return err
	}
	if err := config.Validate(cfg); err != nil {
		return err
	}
	if err := a.d.Store.Save(cfg); err != nil {
		return saveFailed{err}
	}
	a.d.Apply(cfg)
	return nil
}

// --- Seiten ---

var adminPageNames = []string{
	"login.html", "setup.html", "hmis.html", "hmi_edit.html", "client.html",
	"kiosk.html", "terminals.html", "network.html", "system.html",
}

func parseAdminPages(sub fs.FS) map[string]*template.Template {
	funcs := template.FuncMap{"icon": iconSVG, "color": colorClass, "bytes": formatBytes}
	pages := make(map[string]*template.Template, len(adminPageNames))
	for _, name := range adminPageNames {
		pages[name] = template.Must(template.New("layout.html").Funcs(funcs).ParseFS(sub, "layout.html", name))
	}
	return pages
}

// adminView ist der Rahmen jeder Seite.
type adminView struct {
	Title, Section string
	CSRF           string
	LoggedIn       bool
	TerminalName   string
	Notice, Error  string
	Body           any
}

func (a *admin) writePage(w http.ResponseWriter, status int, page string, v adminView) {
	var buf bytes.Buffer
	if err := a.pages[page].Execute(&buf, v); err != nil {
		http.Error(w, "Seite konnte nicht erzeugt werden", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// page rendert eine Seite für eine angemeldete Besucherin und holt dabei die
// einmalige Meldung der vorigen Aktion ab.
func (a *admin) page(w http.ResponseWriter, s sessionRef, status int, page, section, title, errMsg string, body any) {
	name := ""
	if cfg, _, err := a.d.Store.Load(); err == nil {
		name = cfg.Terminal.Name
	}
	a.writePage(w, status, page, adminView{
		Title: title, Section: section, CSRF: s.csrf, LoggedIn: true, TerminalName: name,
		Notice: a.sessions.takeFlash(s), Error: errMsg, Body: body,
	})
}

// done schickt nach Erfolg mit einmaliger Meldung auf die Zielseite (POST-Redirect-GET).
func (a *admin) done(w http.ResponseWriter, r *http.Request, s sessionRef, path, msg string) {
	a.sessions.setFlash(s, msg)
	redirect(w, r, path)
}

// formStatus liefert den Status einer abgelehnten Eingabe.
func formStatus(err error) int {
	var sf saveFailed
	if errors.As(err, &sf) || errors.Is(err, errConfigUnreadable) {
		return http.StatusInternalServerError
	}
	return http.StatusUnprocessableEntity
}
