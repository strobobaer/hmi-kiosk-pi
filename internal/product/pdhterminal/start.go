package pdhterminal

import (
	"context"
	_ "embed"
	"html/template"
	"net/http"
	"strings"

	"hmikiosk/internal/config"
	"hmikiosk/internal/product/pdhterminal/slides"
	"hmikiosk/internal/web"
)

//go:embed start.html
var startPage string

var startTmpl = template.Must(template.New("start").Parse(startPage))

type startView struct {
	Form    bool
	Action  string
	Secret  string
	Message string
	Refresh int
	Theme   template.CSS // Farbvariablen (:root), gleich dem Hintergrund von PDH
}

const (
	msgUnpaired  = "Gerät nicht gekoppelt. Admin-Seite: 5 Sekunden oben rechts auf den Bildschirm drücken."
	msgRevoked   = "Gerät in PDH gesperrt. Bitte in der Admin-Seite (5 Sekunden oben rechts drücken) neu koppeln."
	msgOffline   = "PDH nicht erreichbar. Neuer Versuch in 30 Sekunden."
	msgUntrusted = "Zertifikat von PDH wird nicht vertraut. Neuer Versuch in 30 Sekunden."
)

// StartHandler ist die Startseite des PDH-Terminals: Sie prüft die Kopplung und
// meldet das Gerät per Formular (POST mit dem Geheimnis) bei PDH an. Das Geheimnis
// steht nur im Formularfeld der Antwort, nie in einer Adresse oder einem Header.
// cards (darf nil sein) bedient /api/card/ (events und result); die Routen liegen vor der GET/HEAD-Sperre
// (Preflight braucht OPTIONS, result ist ein POST) und bleiben hinter der Host-Prüfung. Unbekanntes
// unter /api/card/ beantwortet cards selbst mit 404. Dasselbe gilt für /api/slides/ (Zwischenspeicher der
// Folienschau). store (darf nil sein) ist der Zwischenspeicher: Liegt einer vor und ist PDH nicht erreichbar,
// zeigt die Startseite statt der Offline-Meldung die lokale Folienschau unter /slides.
func StartHandler(dataDir string, cfg func() config.PDHCfg, client Client, cards http.Handler, store *slides.Store) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/start", http.StatusFound)
	})
	mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) {
		serveStart(w, r, dataDir, cfg(), client, store)
	})
	if store != nil {
		slides.RegisterThemed(mux, store, func() string { return LoadTheme(dataDir).CSS() }, func(ctx context.Context) bool {
			return pdhOnline(ctx, dataDir, cfg(), client)
		})
	}
	secured := web.SecureLocal(mux)
	// Die Kartenrouten werden von der PDH-Seite (fremder Ursprung) aufgerufen: Host und Loopback prüft
	// LocalPeer, den Origin-Header prüft jede Route selbst (nur die PDH-Adresse).
	var cardRoutes http.Handler
	if cards != nil {
		cardRoutes = web.LocalPeer(cards)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cards != nil && (strings.HasPrefix(r.URL.Path, "/api/card/") || strings.HasPrefix(r.URL.Path, "/api/slides/") || strings.HasPrefix(r.URL.Path, "/api/keyboard/")) {
			cardRoutes.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Methode nicht erlaubt", http.StatusMethodNotAllowed)
			return
		}
		secured.ServeHTTP(w, r)
	})
}

func serveStart(w http.ResponseWriter, r *http.Request, dataDir string, p config.PDHCfg, client Client, store *slides.Store) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Content-Type", "text/html; charset=utf-8")

	var v startView
	theme := template.CSS(LoadTheme(dataDir).CSS())
	csp := "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; form-action 'none'"
	d, err := LoadDevice(dataDir)
	if p.URL == "" || config.ValidatePDH(p) != nil || err != nil || d == nil || d.URL != p.URL {
		v = startView{Message: msgUnpaired, Refresh: 60, Theme: theme}
	} else {
		ctx, cancel := context.WithTimeout(r.Context(), checkTimeout)
		state, _ := client.Check(ctx, *d)
		cancel()
		switch state {
		case StateOK:
			v = startView{Form: true, Action: client.base(*d) + "/terminal/geraet", Secret: d.Secret, Theme: theme}
			csp = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; form-action " + p.URL
			if p.FallbackURL != "" { // beide Adressen: Ein Wechsel während des Ladens darf das Absenden nicht sperren
				csp += " " + p.FallbackURL
			}
		case StateRevoked:
			v = startView{Message: msgRevoked, Refresh: 60, Theme: theme}
		case StateUntrusted:
			v = startView{Message: msgUntrusted, Refresh: 30, Theme: theme}
		default:
			if store.Present() { // globales Terminal: lokale Folienschau statt der Offline-Meldung
				http.Redirect(w, r, "/slides", http.StatusFound)
				return
			}
			v = startView{Message: msgOffline, Refresh: 30, Theme: theme}
		}
	}
	h.Set("Content-Security-Policy", csp)
	if err := startTmpl.Execute(w, v); err != nil {
		return
	}
}

// pdhOnline sagt, ob PDH erreichbar und das Gerät angemeldet werden kann (wie die Startseite es prüft).
func pdhOnline(ctx context.Context, dataDir string, p config.PDHCfg, client Client) bool {
	d, err := LoadDevice(dataDir)
	if p.URL == "" || config.ValidatePDH(p) != nil || err != nil || d == nil || d.URL != p.URL {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	state, _ := client.Check(ctx, *d)
	return state == StateOK
}

// cardOrigin ist der einzige Ursprung, der den Kartenstrom lesen darf: die aktive PDH-Adresse base (Haupt- oder
// Ausweichadresse, nicht die Zusatzursprünge) in der Schreibweise des Origin-Headers, also ohne Standardport.
// base leer: die Hauptadresse. Leer, wenn keine gültige PDH-Adresse eingetragen ist.
func cardOrigin(p config.PDHCfg, base string) string {
	if p.URL == "" {
		return ""
	}
	if base == "" {
		base = p.URL
	}
	o, err := config.WebOrigin(base)
	if err != nil {
		return ""
	}
	if strings.HasPrefix(o, "https://") {
		return strings.TrimSuffix(o, ":443")
	}
	return strings.TrimSuffix(o, ":80")
}
