package pdhterminal

import "net/http"

// keyboardPageHandler lässt die PDH-Seite die Bildschirmtastatur auf Anforderung ein- und ausblenden:
// POST /api/keyboard/show beim Fokus auf ein Eingabefeld, POST /api/keyboard/hide beim Verlassen. Ursprung
// und CORS wie bei /api/card/result (nur die PDH-Adresse, Preflight samt Private-Network). inner ist
// web.KeyboardHandler; „toggle“ gibt es für fremde Seiten nicht. Ohne Tastatur (Windows, X11) antwortet inner
// mit 503, die Seite ignoriert das.
func keyboardPageHandler(origin func() string, inner http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		allowed := origin()
		if got := r.Header.Get("Origin"); allowed == "" || got != allowed {
			http.Error(w, "Ursprung nicht erlaubt", http.StatusForbidden)
			return
		}
		hd := w.Header()
		hd.Set("Access-Control-Allow-Origin", allowed)
		hd.Set("Vary", "Origin")
		switch r.Method {
		case http.MethodOptions:
			hd.Set("Access-Control-Allow-Methods", "POST")
			hd.Set("Access-Control-Allow-Headers", "Content-Type")
			hd.Set("Access-Control-Max-Age", "600")
			if r.Header.Get("Access-Control-Request-Private-Network") == "true" {
				hd.Set("Access-Control-Allow-Private-Network", "true")
			}
			w.WriteHeader(http.StatusNoContent)
		case http.MethodPost:
			if a := r.PathValue("action"); a != "show" && a != "hide" {
				http.Error(w, "Unbekannte Aktion", http.StatusNotFound)
				return
			}
			inner.ServeHTTP(w, r)
		default:
			hd.Set("Allow", "POST, OPTIONS")
			http.Error(w, "Methode nicht erlaubt", http.StatusMethodNotAllowed)
		}
	})
}
