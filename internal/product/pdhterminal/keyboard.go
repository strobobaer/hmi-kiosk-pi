package pdhterminal

import "net/http"

// keyboardPageHandler lässt die PDH-Seite die Bildschirmtastatur auf Anforderung ein- und ausblenden. Ursprung
// und CORS wie bei /api/card/result (nur die PDH-Adresse, Preflight samt Private-Network). inner ist
// web.KeyboardHandler; „toggle“ gibt es für fremde Seiten nicht. Ohne Tastatur (Windows, X11) antwortet inner
// mit 503, die Seite ignoriert das.
//
// Aktionen (POST /api/keyboard/{action}):
//   - show, auto-show: Fokus auf einem Eingabefeld, die Tastatur erscheint von selbst. „show“ senden alle bisherigen
//     PDH-Versionen so; „auto-show“ ist derselbe Zweck unter eindeutigem Namen für spätere. Nur bei wirksamer Maus
//     und eingestelltem „Bildschirmtastatur bei Mausbedienung: aus“ (suppress liefert true) beantwortet das Terminal
//     beide mit 204 ohne Wirkung; sonst öffnet sie sich wie bei Touch.
//   - open: ausdrücklicher Wunsch (z. B. ein Tastatur-Knopf der PDH-Seite), zeigt die Tastatur immer.
//   - hide: verbirgt die Tastatur (immer).
//
// suppress darf nil sein (dann nie unterdrücken).
func keyboardPageHandler(origin func() string, inner http.Handler, suppress func() bool) http.Handler {
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
			switch r.PathValue("action") {
			case "hide":
			case "show", "auto-show":
				if suppress != nil && suppress() {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				r.SetPathValue("action", "show")
			case "open":
				r.SetPathValue("action", "show")
			default:
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
