// Package web liefert die lokale Kachelseite und die Sitzungsleiste, die
// Chromium auf dem Terminal anzeigt.
package web

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"hmikiosk/internal/session"
)

// SessionAPI ist der Teil des Sitzungsautomaten, den die Seiten brauchen.
type SessionAPI interface {
	Open(string) error
	Confirm() error
	Cancel()
	Back()
	Snapshot() session.Snapshot
	Subscribe() (<-chan session.Snapshot, func())
}

// TileView ist alles, was die Kachelseite über ein HMI erfährt: weder Adresse
// noch Passwort.
type TileView struct {
	ID, Name, Icon, Color string
	Reachable             bool
	AuthFailed            bool
	BusyBy                string
	Disabled              bool
	Web                   bool // Web-Kachel (Grafana, Webseite): nie belegt
}

var heartbeat = 15 * time.Second

// allowedHosts schützt gegen DNS-Rebinding: Nur die lokalen Namen sind gültig.
var allowedHosts = map[string]bool{"127.0.0.1:8080": true, "localhost:8080": true}

// tilePort ist der Port der Kachelseite; im Netz gilt nur er.
const tilePort = "8080"

var allowedOrigins = map[string]bool{
	"http://127.0.0.1:8080": true,
	"http://localhost:8080": true,
}

type local struct {
	s      SessionAPI
	tiles  func() []TileView
	layout func() string
	ex     LocalExtras
	page   *template.Template
	bar    *template.Template
	static fs.FS
}

// LocalExtras sind die optionalen Teile der Selbstprüfung auf der lokalen Seite.
type LocalExtras struct {
	// TouchHit nimmt den Treffer eines Ziels der Touch-Seite an; false lehnt ihn ab.
	TouchHit func(target int) bool
	// Network meldet, ob die Kachelseite im Netz freigegeben ist (nur für Tests).
	// Dann gelten zusätzlich IP-Adressen mit Port 8080 als Host, nie Namen. nil heißt aus.
	Network func() bool
	// Keyboard blendet die Bildschirmtastatur ein und aus (nil: keine).
	Keyboard KeyboardAPI
	// InputMode liefert die Bedienart (touch, mouse); bei mouse zeigt sich die
	// Tastatur nie von selbst, nur über den Knopf der Leiste.
	InputMode func() string
	// TilesShown meldet, dass Chromium am Terminal die Kachelseite geladen hat
	// (lokaler Host); die Admin-Seite am Terminal ist dann geschlossen.
	TilesShown func()
	// LocalAdmin meldet, ob die Admin-Seite am Terminal läuft (nil: ja). Ohne sie fehlt
	// das Zahnrad: Chromium bliebe sonst auf einer Fehlerseite ohne Rückweg stehen.
	LocalAdmin func() bool
}

// KeyboardAPI steuert die Bildschirmtastatur (keyboard.Controller).
type KeyboardAPI interface {
	Show() error
	Hide() error
	Toggle() error
}

// Adressen der Admin-Seite am Terminal (nur Loopback, HTTP) und der Kachelseite.
const (
	LocalAdminURL = "http://127.0.0.1:8081/admin/"
	KioskURL      = "http://127.0.0.1:8080/"
)

// NewLocal baut den Handler der Kachelseite. layout liefert "auto" oder die
// Spaltenzahl "2", "3", "4".
func NewLocal(s SessionAPI, tiles func() []TileView, layout func() string) http.Handler {
	return NewLocalWith(s, tiles, layout, LocalExtras{})
}

// NewLocalWith ergänzt NewLocal um die Touch-Seite der Selbstprüfung.
func NewLocalWith(s SessionAPI, tiles func() []TileView, layout func() string, ex LocalExtras) http.Handler {
	sub, err := fs.Sub(assets, "assets")
	if err != nil {
		panic(err)
	}
	funcs := template.FuncMap{"icon": iconSVG, "color": colorClass}
	l := &local{
		s: s, tiles: tiles, layout: layout, ex: ex, static: sub,
		page: template.Must(template.New("tiles.html").Funcs(funcs).ParseFS(sub, "tiles.html")),
		bar:  template.Must(template.New("bar.html").Funcs(funcs).ParseFS(sub, "bar.html")),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", l.handleTiles)
	mux.HandleFunc("GET /bar", l.handleBar)
	mux.HandleFunc("GET /kiosk.css", l.asset("kiosk.css", "text/css; charset=utf-8"))
	mux.HandleFunc("GET /kiosk.js", l.asset("kiosk.js", "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /keyboard.js", l.asset("keyboard.js", "text/javascript; charset=utf-8"))
	// Die Tastatur bleibt lokal, auch bei Netzfreigabe: Niemand im Netz soll sie bedienen.
	mux.Handle("POST /api/keyboard/{action}", onlyLocalHost(KeyboardHandler(ex.Keyboard)))
	mux.HandleFunc("GET /selftest/touch", l.asset("selftest.html", "text/html; charset=utf-8"))
	mux.HandleFunc("GET /selftest.js", l.asset("selftest.js", "text/javascript; charset=utf-8"))
	mux.Handle("POST /api/selftest/touch", onlyLocalHost(touchHit(ex.TouchHit)))
	mux.HandleFunc("GET /api/events", l.handleEvents)
	mux.HandleFunc("POST /api/hmi/{id}/open", l.handleOpen)
	mux.HandleFunc("POST /api/confirm", l.handleConfirm)
	mux.HandleFunc("POST /api/cancel", func(w http.ResponseWriter, _ *http.Request) { l.s.Cancel(); noContent(w) })
	mux.HandleFunc("POST /api/back", func(w http.ResponseWriter, _ *http.Request) { l.s.Back(); noContent(w) })
	return secureWith(mux, ex.Network)
}

// SecureLocal setzt Host- und Origin-Prüfung der lokalen Seite vor weitere lokale
// Routen. Sie gilt immer nur lokal, auch wenn die Kachelseite im Netz freigegeben ist.
func SecureLocal(next http.Handler) http.Handler { return secure(onlyLocalHost(next)) }

func secure(next http.Handler) http.Handler { return secureWith(next, nil) }

// secureWith prüft Host und Origin. Mit Netzfreigabe gilt zusätzlich ein IP-Host mit
// Port 8080, und ein POST darf nur von genau diesem Host kommen (gleicher Ursprung).
func secureWith(next http.Handler, network func() bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "no-store")
		netHost := !allowedHosts[r.Host] && network != nil && network() && ipHost(r.Host)
		if !allowedHosts[r.Host] && !netHost {
			writeError(w, http.StatusForbidden, "Anfrage nicht erlaubt")
			return
		}
		if r.Method == http.MethodPost && !originAllowed(r, netHost) {
			writeError(w, http.StatusForbidden, "Anfrage nicht erlaubt")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ipHost lässt nur eine IP-Adresse (IPv6 in Klammern, ohne Zone) mit Port 8080 zu.
// Namen bleiben gesperrt: Sie könnte ein fremder DNS-Server auf das Terminal zeigen lassen.
func ipHost(hostport string) bool {
	host, port, err := net.SplitHostPort(hostport)
	if err != nil || port != tilePort {
		return false
	}
	if strings.Contains(host, ":") && !strings.HasPrefix(hostport, "[") {
		return false
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.Zone() == ""
}

// originAllowed verlangt Origin oder Referer; fehlen beide, wird abgelehnt. Bei
// einem Host aus dem Netz (netHost) gilt nur der eigene Ursprung http://<Host>.
func originAllowed(r *http.Request, netHost bool) bool {
	ok := func(origin string) bool {
		if netHost {
			return origin == "http://"+r.Host
		}
		return allowedOrigins[origin]
	}
	if o := r.Header.Get("Origin"); o != "" {
		return ok(o)
	}
	if ref := r.Header.Get("Referer"); ref != "" {
		u, err := url.Parse(ref)
		return err == nil && ok(u.Scheme+"://"+u.Host)
	}
	return false
}

func (l *local) asset(name, ctype string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := fs.ReadFile(l.static, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", ctype)
		_, _ = w.Write(b)
	}
}

// fromTerminal: lokaler Name als Host und eine Verbindung über Loopback. Der Host allein
// reicht nicht: Bei Netzfreigabe kann ein Client im Netz Host und Origin vortäuschen.
func fromTerminal(r *http.Request) bool {
	return allowedHosts[r.Host] && loopbackPeer(r)
}

// loopbackPeer: Die Gegenstelle ist eine Loopback-Adresse; Unlesbares gilt als fremd.
func loopbackPeer(r *http.Request) bool {
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	return err == nil && ap.Addr().Unmap().IsLoopback()
}

// onlyLocalHost lässt nur Anfragen vom Terminal selbst zu (fromTerminal).
func onlyLocalHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !fromTerminal(r) {
			writeError(w, http.StatusForbidden, "Anfrage nicht erlaubt")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// KeyboardHandler bedient POST …/{action} mit show, hide oder toggle. Host und
// Ursprung prüft der Aufrufer.
func KeyboardHandler(kb KeyboardAPI) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if kb == nil {
			writeError(w, http.StatusServiceUnavailable, "Bildschirmtastatur nicht verfügbar")
			return
		}
		var err error
		switch r.PathValue("action") {
		case "show":
			err = kb.Show()
		case "hide":
			err = kb.Hide()
		case "toggle":
			err = kb.Toggle()
		default:
			writeError(w, http.StatusNotFound, "Unbekannte Aktion")
			return
		}
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "Bildschirmtastatur nicht verfügbar")
			return
		}
		noContent(w)
	})
}

// oskMode: „auto“ zeigt die Tastatur bei Eingabefeldern, „manual“ nur über den Knopf.
func oskMode(inputMode func() string) string {
	if inputMode != nil && inputMode() == "mouse" {
		return "manual"
	}
	return "auto"
}

type tilesPage struct {
	Tiles    []TileView
	Layout   string
	AdminURL string
	OSK      string
}

func (l *local) handleTiles(w http.ResponseWriter, r *http.Request) {
	var tiles []TileView
	if l.tiles != nil {
		tiles = l.tiles()
	}
	layout := "auto"
	if l.layout != nil {
		layout = l.layout()
	}
	if fromTerminal(r) && l.ex.TilesShown != nil {
		l.ex.TilesShown()
	}
	render(w, l.page, "tiles.html", tilesPage{
		Tiles: tiles, Layout: layoutClass(layout), AdminURL: l.adminURL(r.Host), OSK: oskMode(l.ex.InputMode),
	})
}

// adminURL ist leer, wenn es am Terminal keine Admin-Seite gibt.
func (l *local) adminURL(hostport string) string {
	if allowedHosts[hostport] && l.ex.LocalAdmin != nil && !l.ex.LocalAdmin() {
		return ""
	}
	return adminURLFor(hostport)
}

// adminURLFor: Am Terminal führt das Zahnrad zur Loopback-Admin-Seite; von einem
// PC aus (Netzfreigabe, IP-Host) zur Admin-Seite über HTTPS unter derselben Adresse.
func adminURLFor(hostport string) string {
	if allowedHosts[hostport] {
		return LocalAdminURL
	}
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		return LocalAdminURL
	}
	return "https://" + net.JoinHostPort(host, adminPort) + "/admin/"
}

func layoutClass(v string) string {
	switch v {
	case "2", "3", "4":
		return "cols-" + v
	}
	return "cols-auto"
}

type barPage struct {
	Name     string
	ViewOnly bool
	Web      bool
	Warned   bool
	Notice   string
}

func (l *local) handleBar(w http.ResponseWriter, _ *http.Request) {
	snap := l.s.Snapshot()
	render(w, l.bar, "bar.html", barPage{
		Name: snap.HMIName, ViewOnly: snap.ViewOnly, Web: snap.Web,
		Warned: snap.State == session.StateWarned, Notice: snap.Notice,
	})
}

func render(w http.ResponseWriter, t *template.Template, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = t.ExecuteTemplate(w, name, data) // Kopf ist gesendet, ein Fehler lässt sich nicht mehr melden
}

// wireSnapshot ist die Sicht der Oberfläche auf den Sitzungsstand: nur Namen.
type wireSnapshot struct {
	State       string   `json:"state"`
	HMI         string   `json:"hmi"`
	HMIName     string   `json:"hmiName"`
	ViewOnly    bool     `json:"viewOnly"`
	Web         bool     `json:"web"`
	Notice      string   `json:"notice"`
	ConfirmName string   `json:"confirmName"`
	Unreachable []string `json:"unreachable"`
	BadKey      []string `json:"badKey"`
}

func toWire(s session.Snapshot) wireSnapshot {
	w := wireSnapshot{
		State: string(s.State), HMI: s.HMI, HMIName: s.HMIName, ViewOnly: s.ViewOnly, Web: s.Web, Notice: s.Notice,
		Unreachable: append([]string{}, s.Unreachable...), BadKey: append([]string{}, s.BadKey...),
	}
	if s.Confirm != nil {
		w.ConfirmName = s.Confirm.Name
	}
	return w
}

func (l *local) handleEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "Ereignisstrom nicht möglich")
		return
	}
	ch, cancel := l.s.Subscribe()
	defer cancel()
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	fl.Flush()

	tick := time.NewTicker(heartbeat)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case s, ok := <-ch:
			if !ok {
				return
			}
			b, _ := json.Marshal(toWire(s))
			if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
				return
			}
			fl.Flush()
		case <-tick.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			fl.Flush()
		}
	}
}

func (l *local) handleOpen(w http.ResponseWriter, r *http.Request) {
	if err := l.s.Open(r.PathValue("id")); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	noContent(w)
}

func (l *local) handleConfirm(w http.ResponseWriter, _ *http.Request) {
	if err := l.s.Confirm(); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	noContent(w)
}

func noContent(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) }

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

var colors = map[string]bool{"blue": true, "green": true, "amber": true, "red": true, "gray": true, "violet": true}

// colorClass lässt nur feste Farbnamen durch; alles andere wird grau.
func colorClass(c string) string {
	c = strings.ToLower(c)
	if colors[c] {
		return "c-" + c
	}
	return "c-gray"
}

// Symbole sind feste Pfade im 24er-Raster, nie Eingaben.
var icons = map[string]string{
	"monitor":  `<rect x="2.5" y="4" width="19" height="13" rx="1.5"/><path d="M8 21h8M12 17v4"/>`,
	"press":    `<path d="M5 3h14M12 3v9M7 12h10v4H7zM4 21h16M9 16v5M15 16v5"/>`,
	"tank":     `<path d="M5 6c0-1.7 3.1-3 7-3s7 1.3 7 3v12c0 1.7-3.1 3-7 3s-7-1.3-7-3z"/><path d="M5 6c0 1.7 3.1 3 7 3s7-1.3 7-3M5 13c0 1.7 3.1 3 7 3s7-1.3 7-3"/>`,
	"conveyor": `<rect x="2.5" y="9" width="19" height="6" rx="3"/><circle cx="6.5" cy="12" r="1"/><circle cx="12" cy="12" r="1"/><circle cx="17.5" cy="12" r="1"/><path d="M5 18v3M19 18v3"/>`,
	"gear":     `<circle cx="12" cy="12" r="3.2"/><path d="M12 2.5v3M12 18.5v3M2.5 12h3M18.5 12h3M5.3 5.3l2.1 2.1M16.6 16.6l2.1 2.1M18.7 5.3l-2.1 2.1M7.4 16.6l-2.1 2.1"/>`,
	"bolt":     `<path d="M13 2.5 5 13.5h6l-1 8 8-11h-6z"/>`,
}

func iconSVG(name string) template.HTML {
	body, ok := icons[name]
	if !ok {
		body = icons["monitor"]
	}
	return template.HTML(`<svg class="icon" viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">` + body + `</svg>`)
}

// touchHit nimmt {"target": n} an. Ohne Selbstprüfung gibt es die Route nicht.
func touchHit(hit func(int) bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if hit == nil {
			writeError(w, http.StatusNotFound, "Keine Selbstprüfung aktiv")
			return
		}
		var in struct {
			Target *int `json:"target"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&in); err != nil || in.Target == nil {
			writeError(w, http.StatusBadRequest, "Ungültige Anfrage")
			return
		}
		if !hit(*in.Target) {
			writeError(w, http.StatusBadRequest, "Unbekanntes Ziel")
			return
		}
		noContent(w)
	}
}
