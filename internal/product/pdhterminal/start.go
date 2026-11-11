package pdhterminal

import (
	"context"
	_ "embed"
	"html/template"
	"net/http"

	"hmikiosk/internal/config"
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
func StartHandler(dataDir string, cfg func() config.PDHCfg, client Client) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/start", http.StatusFound)
	})
	mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) {
		serveStart(w, r, dataDir, cfg(), client)
	})
	secured := web.SecureLocal(mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Methode nicht erlaubt", http.StatusMethodNotAllowed)
			return
		}
		secured.ServeHTTP(w, r)
	})
}

func serveStart(w http.ResponseWriter, r *http.Request, dataDir string, p config.PDHCfg, client Client) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Content-Type", "text/html; charset=utf-8")

	var v startView
	csp := "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; form-action 'none'"
	d, err := LoadDevice(dataDir)
	if p.URL == "" || config.ValidatePDH(p) != nil || err != nil || d == nil || d.URL != p.URL {
		v = startView{Message: msgUnpaired, Refresh: 60}
	} else {
		ctx, cancel := context.WithTimeout(r.Context(), checkTimeout)
		state, _ := client.Check(ctx, *d)
		cancel()
		switch state {
		case StateOK:
			v = startView{Form: true, Action: p.URL + "/terminal/geraet", Secret: d.Secret}
			csp = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; form-action " + p.URL
		case StateRevoked:
			v = startView{Message: msgRevoked, Refresh: 60}
		case StateUntrusted:
			v = startView{Message: msgUntrusted, Refresh: 30}
		default:
			v = startView{Message: msgOffline, Refresh: 30}
		}
	}
	h.Set("Content-Security-Policy", csp)
	if err := startTmpl.Execute(w, v); err != nil {
		return
	}
}
