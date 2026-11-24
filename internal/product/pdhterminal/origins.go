package pdhterminal

import (
	"net/http"

	"hmikiosk/internal/config"
)

// cardOrigins sind die Ursprünge, die den Kartenstrom lesen dürfen: die Haupt- und, wenn eingetragen, die
// Ausweichadresse (beide vom Admin gesetzt, gleiche Schreibweise wie cardOrigin). Beide gelten unabhängig von
// der aktiven Adresse: Eine am Tailnet geladene Seite behält den Kartenleser, wenn die Probe zurückschaltet.
func cardOrigins(p config.PDHCfg) []string {
	var out []string
	if o := cardOrigin(p, p.URL); o != "" {
		out = append(out, o)
		if p.FallbackURL != "" {
			if f := cardOrigin(p, p.FallbackURL); f != "" && f != o {
				out = append(out, f)
			}
		}
	}
	return out
}

// byOrigin baut den Handler je Anfrage mit dem Ursprung, der zum Origin-Header passt (leer, wenn keiner der
// erlaubten passt; dann antwortet er mit 403). Die Handler selbst kennen weiter nur einen Ursprung.
func byOrigin(allowed func() []string, build func(origin func() string) http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, match := r.Header.Get("Origin"), ""
		for _, o := range allowed() {
			if o == got {
				match = o
			}
		}
		build(func() string { return match }).ServeHTTP(w, r)
	})
}
