package pdhterminal

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"hmikiosk/internal/config"
)

func TestCardOriginsBothAccepted(t *testing.T) {
	with := config.PDHCfg{URL: "https://pdh.lan", FallbackURL: "https://pdh.tail1234.ts.net:443"}
	without := config.PDHCfg{URL: "https://pdh.lan"}
	if got := cardOrigins(with); len(got) != 2 || got[0] != "https://pdh.lan" || got[1] != "https://pdh.tail1234.ts.net" {
		t.Errorf("cardOrigins mit Ausweich: %v", got)
	}
	if got := cardOrigins(without); len(got) != 1 || got[0] != "https://pdh.lan" {
		t.Errorf("cardOrigins ohne Ausweich: %v", got)
	}
	if got := cardOrigins(config.PDHCfg{}); len(got) != 0 {
		t.Errorf("leer: %v", got)
	}

	status := func(p config.PDHCfg, origin string) int {
		h := byOrigin(func() []string { return cardOrigins(p) }, func(o func() string) http.Handler {
			return keyboardPageHandler(o, http.NotFoundHandler(), nil)
		})
		r := httptest.NewRequest("OPTIONS", "/api/keyboard/show", nil)
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code == http.StatusNoContent && w.Header().Get("Access-Control-Allow-Origin") != origin {
			t.Errorf("Allow-Origin %q für %q", w.Header().Get("Access-Control-Allow-Origin"), origin)
		}
		return w.Code
	}
	// Die Liste hängt nicht an der aktiven Adresse: beide Ursprünge gelten, egal wo die Route steht.
	for origin, want := range map[string]int{
		"https://pdh.lan":             http.StatusNoContent,
		"https://pdh.tail1234.ts.net": http.StatusNoContent,
		"https://evil.example":        http.StatusForbidden,
		"":                            http.StatusForbidden,
	} {
		if got := status(with, origin); got != want {
			t.Errorf("mit Ausweich %q: %d, erwartet %d", origin, got, want)
		}
	}
	for origin, want := range map[string]int{
		"https://pdh.lan":             http.StatusNoContent,
		"https://pdh.tail1234.ts.net": http.StatusForbidden,
	} {
		if got := status(without, origin); got != want {
			t.Errorf("ohne Ausweich %q: %d, erwartet %d", origin, got, want)
		}
	}
}
