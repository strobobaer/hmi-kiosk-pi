// Package web liefert die lokale Kachelseite und die Sitzungsleiste, die
// Chromium auf dem Terminal anzeigt.
package web

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
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
	BusyBy                string
	Disabled              bool
}

var heartbeat = 15 * time.Second

// allowedHosts schützt gegen DNS-Rebinding: Nur die lokalen Namen sind gültig.
var allowedHosts = map[string]bool{"127.0.0.1:8080": true, "localhost:8080": true}

var allowedOrigins = map[string]bool{
	"http://127.0.0.1:8080": true,
	"http://localhost:8080": true,
}

type local struct {
	s      SessionAPI
	tiles  func() []TileView
	layout func() string
	page   *template.Template
	bar    *template.Template
	static fs.FS
}

// NewLocal baut den Handler der Kachelseite. layout liefert "auto" oder die
// Spaltenzahl "2", "3", "4".
func NewLocal(s SessionAPI, tiles func() []TileView, layout func() string) http.Handler {
	sub, err := fs.Sub(assets, "assets")
	if err != nil {
		panic(err)
	}
	funcs := template.FuncMap{"icon": iconSVG, "color": colorClass}
	l := &local{
		s: s, tiles: tiles, layout: layout, static: sub,
		page: template.Must(template.New("tiles.html").Funcs(funcs).ParseFS(sub, "tiles.html")),
		bar:  template.Must(template.New("bar.html").Funcs(funcs).ParseFS(sub, "bar.html")),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", l.handleTiles)
	mux.HandleFunc("GET /bar", l.handleBar)
	mux.HandleFunc("GET /kiosk.css", l.asset("kiosk.css", "text/css; charset=utf-8"))
	mux.HandleFunc("GET /kiosk.js", l.asset("kiosk.js", "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /api/events", l.handleEvents)
	mux.HandleFunc("POST /api/hmi/{id}/open", l.handleOpen)
	mux.HandleFunc("POST /api/confirm", l.handleConfirm)
	mux.HandleFunc("POST /api/cancel", func(w http.ResponseWriter, _ *http.Request) { l.s.Cancel(); noContent(w) })
	mux.HandleFunc("POST /api/back", func(w http.ResponseWriter, _ *http.Request) { l.s.Back(); noContent(w) })
	return secure(mux)
}

func secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "no-store")
		if !allowedHosts[r.Host] {
			writeError(w, http.StatusForbidden, "Anfrage nicht erlaubt")
			return
		}
		if r.Method == http.MethodPost && !originAllowed(r) {
			writeError(w, http.StatusForbidden, "Anfrage nicht erlaubt")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// originAllowed verlangt Origin oder Referer; fehlen beide, wird abgelehnt.
func originAllowed(r *http.Request) bool {
	if o := r.Header.Get("Origin"); o != "" {
		return allowedOrigins[o]
	}
	if ref := r.Header.Get("Referer"); ref != "" {
		u, err := url.Parse(ref)
		return err == nil && allowedOrigins[u.Scheme+"://"+u.Host]
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

type tilesPage struct {
	Tiles  []TileView
	Layout string
}

func (l *local) handleTiles(w http.ResponseWriter, _ *http.Request) {
	var tiles []TileView
	if l.tiles != nil {
		tiles = l.tiles()
	}
	layout := "auto"
	if l.layout != nil {
		layout = l.layout()
	}
	render(w, l.page, "tiles.html", tilesPage{Tiles: tiles, Layout: layoutClass(layout)})
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
	Warned   bool
	Notice   string
}

func (l *local) handleBar(w http.ResponseWriter, _ *http.Request) {
	snap := l.s.Snapshot()
	render(w, l.bar, "bar.html", barPage{
		Name: snap.HMIName, ViewOnly: snap.ViewOnly,
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
	Notice      string   `json:"notice"`
	ConfirmName string   `json:"confirmName"`
	Unreachable []string `json:"unreachable"`
	BadKey      []string `json:"badKey"`
}

func toWire(s session.Snapshot) wireSnapshot {
	w := wireSnapshot{
		State: string(s.State), HMI: s.HMI, HMIName: s.HMIName, ViewOnly: s.ViewOnly, Notice: s.Notice,
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
