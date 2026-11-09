package web

import (
	"context"
	"io/fs"
	"net/http"
	"net/url"
)

// Die Admin-Seite am Terminal: Chromium im Kiosk kann die HTTPS-Seite mit dem
// selbstsignierten Zertifikat nicht öffnen. Dieselben Seiten gibt es deshalb auch
// über HTTP an 127.0.0.1:8081 (nie im Netz), ohne /peer/, mit eigenen Cookies, kurzer
// Sitzung und der Tastatur-API. Anmeldung und Sperre sind dieselben.

// pathLocalBack führt vom Terminal zurück zur Kachelseite; auch im Wartungs- und Reparaturmodus.
const pathLocalBack = "/admin/local/back"

// localAdminHosts schützt gegen DNS-Rebinding: Nur die lokalen Namen gelten.
var localAdminHosts = map[string]bool{"127.0.0.1:8081": true, "localhost:8081": true}

var localAdminOrigins = map[string]bool{"http://127.0.0.1:8081": true, "http://localhost:8081": true}

type terminalKey struct{}

// onTerminal meldet, ob die Anfrage über den Loopback-Listener der Admin-Seite kam.
func onTerminal(r *http.Request) bool {
	v, _ := r.Context().Value(terminalKey{}).(bool)
	return v
}

// NewAdminHandlers baut beide Zugänge der Admin-Seite mit gemeinsamen Sitzungen und
// gemeinsamer Sperre: remote für HTTPS, local für den Loopback-Listener am Terminal.
func NewAdminHandlers(d AdminDeps) (remote, local http.Handler) {
	a := newAdmin(d)
	return a.handler(), a.localHandler()
}

func (a *admin) localHandler() http.Handler { return a.local }

func (a *admin) buildLocal() http.Handler {
	root, err := fs.Sub(assets, "assets")
	if err != nil {
		panic(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/admin/", a.h)
	// Zurück zum Kiosk geht immer, auch mit abgelaufener oder ohne Sitzung: Sonst führte
	// die Inaktivität erst auf die Anmeldeseite und von dort erst nach weiteren 5 Minuten zurück.
	mux.HandleFunc("POST "+pathLocalBack, a.handleLocalBack)
	mux.Handle("POST /api/keyboard/{action}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		KeyboardHandler(a.d.Keyboard).ServeHTTP(w, r)
	}))
	mux.HandleFunc("GET /keyboard.js", func(w http.ResponseWriter, r *http.Request) {
		b, err := fs.ReadFile(root, "keyboard.js")
		if err != nil {
			notFound(w)
			return
		}
		setAdminHeaders(w, true)
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		_, _ = w.Write(b)
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) { redirect(w, r, "/admin/") })
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { notFound(w) })
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !localAdminHosts[r.Host] {
			http.Error(w, "Anfrage nicht erlaubt", http.StatusForbidden)
			return
		}
		if isWrite(r) && !localAdminOrigin(r) {
			http.Error(w, "Anfrage nicht erlaubt", http.StatusForbidden)
			return
		}
		// Das Zahnrad (Navigation von der Kachelseite) öffnet die Seite auch ohne Sitzung.
		if trustedNavigation(r) {
			a.localActive()
		}
		mux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), terminalKey{}, true)))
	})
}

// localAdminOrigin verlangt Origin oder Referer vom Loopback-Listener; fehlen beide,
// wird abgelehnt.
func localAdminOrigin(r *http.Request) bool {
	if o := r.Header.Get("Origin"); o != "" {
		return localAdminOrigins[o]
	}
	if ref := r.Header.Get("Referer"); ref != "" {
		u, err := url.Parse(ref)
		return err == nil && localAdminOrigins[u.Scheme+"://"+u.Host]
	}
	return false
}

// trustedNavigation: Chromium öffnet die Seite als Dokument, nicht von einer fremden Seite
// aus (Sec-Fetch-Site none, same-origin oder same-site wie die Kachelseite an 8080).
func trustedNavigation(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Mode") != "navigate" {
		return false
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "none", "same-origin", "same-site":
		return true
	}
	return false
}

// foreignFetch: Abruf von einer anderen Seite (Bild, fetch, Formular einer Web-Kachel).
// Er hält die Admin-Seite am Terminal nicht offen, auch wenn das Cookie mitkommt.
func foreignFetch(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "cross-site":
		return true
	case "same-site":
		return r.Header.Get("Sec-Fetch-Mode") != "navigate"
	}
	return false
}

func (a *admin) localActive() {
	if a.d.LocalActive != nil {
		a.d.LocalActive()
	}
}

// handleLocalBack beendet eine Sitzung am Terminal, falls es eine gibt, und kehrt immer
// zur Kachelseite zurück. Das Skript der Seite nutzt denselben Weg nach 5 Minuten ohne
// Eingabe. Host und Ursprung hat buildLocal geprüft.
func (a *admin) handleLocalBack(w http.ResponseWriter, r *http.Request) {
	if s, ok := a.sessionFrom(r); ok {
		a.sessions.remove(s)
	}
	name, _ := cookieNames(true)
	clearCookie(w, name, false)
	if a.d.LocalLeft != nil {
		a.d.LocalLeft()
	}
	http.Redirect(w, r, KioskURL, http.StatusSeeOther)
}
