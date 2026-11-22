package pdhterminal

import (
	"hmikiosk/internal/web"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestAdminFunctions(t *testing.T) {
	e := newAdminEnv(t, newFakePDH(t).client())
	r := e.get("/admin/functions")
	body := r.Body.String()
	if r.Code != http.StatusOK || !strings.Contains(body, "Funktionen") {
		t.Fatalf("GET: %d", r.Code)
	}
	for _, d := range FuncDefs {
		if !strings.Contains(body, `name="f_`+d.Key+`"`) || !strings.Contains(body, d.Label) {
			t.Errorf("Schalter %s fehlt", d.Key)
		}
	}
	if !strings.Contains(body, `href="/admin/functions"`) || !strings.Contains(body, `/admin/theme.css`) {
		t.Error("Menüpunkt oder Farben fehlen")
	}
	// Nur Ticket und Wartungen angehakt, alle anderen aus; Störung, Rüsten, … werden damit ausgeschaltet.
	r = e.post("/admin/functions", url.Values{"f_ticket": {"1"}, "f_wartungen": {"1"}})
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), "Gespeichert") {
		t.Fatalf("POST: %d\n%s", r.Code, r.Body.String())
	}
	for key, want := range map[string]bool{FuncTicket: true, FuncMaint: true, FuncFault: false, FuncSetup: false, FuncHMI: false, FuncSaverSlide: false} {
		if got := e.product.funcs.Get(key).Enabled; got != want {
			t.Errorf("%s = %v, erwartet %v", key, got, want)
		}
	}
	if !e.product.funcs.Pending(FuncFault) || e.product.funcs.Pending(FuncTicket) {
		t.Error("nur geänderte Schalter sind vorgemerkt")
	}
	if r := e.post("/admin/functions", url.Values{"f_ticket": {"1"}, "f_wartungen": {"1"}}); !strings.Contains(r.Body.String(), "Keine Änderung") {
		t.Error("zweites Speichern ohne Änderung")
	}
}

func TestAdminThemeCSS(t *testing.T) {
	e := newAdminEnv(t, newFakePDH(t).client())
	r := e.get("/admin/theme.css")
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), "--bg:#0f1117") || !strings.Contains(r.Header().Get("Content-Type"), "text/css") {
		t.Fatalf("theme.css: %d %q", r.Code, r.Body.String())
	}
	if err := SaveTheme(e.dir, LightTheme()); err != nil {
		t.Fatal(err)
	}
	if r := e.get("/admin/theme.css"); !strings.Contains(r.Body.String(), "--bg:#f3f3f3") {
		t.Fatalf("helles Theme: %q", r.Body.String())
	}
}

type fakeKbd struct{ calls []string }

func (k *fakeKbd) Show() error   { k.calls = append(k.calls, "show"); return nil }
func (k *fakeKbd) Hide() error   { k.calls = append(k.calls, "hide"); return nil }
func (k *fakeKbd) Toggle() error { k.calls = append(k.calls, "toggle"); return nil }

func TestKeyboardPageHandler(t *testing.T) {
	kb := &fakeKbd{}
	origin := func() string { return "https://pdh.firma.de" }
	mux := http.NewServeMux()
	mux.Handle("/api/keyboard/{action}", keyboardPageHandler(origin, web.KeyboardHandler(kb)))
	do := func(method, path, org string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		if org != "" {
			r.Header.Set("Origin", org)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	if w := do(http.MethodPost, "/api/keyboard/show", "https://fremd.de"); w.Code != http.StatusForbidden {
		t.Errorf("fremder Ursprung: %d", w.Code)
	}
	if w := do(http.MethodPost, "/api/keyboard/show", ""); w.Code != http.StatusForbidden {
		t.Errorf("ohne Ursprung: %d", w.Code)
	}
	if w := do(http.MethodPost, "/api/keyboard/show", "https://pdh.firma.de"); w.Code != http.StatusNoContent || w.Header().Get("Access-Control-Allow-Origin") != "https://pdh.firma.de" {
		t.Errorf("show: %d %v", w.Code, w.Header())
	}
	if w := do(http.MethodPost, "/api/keyboard/hide", "https://pdh.firma.de"); w.Code != http.StatusNoContent {
		t.Errorf("hide: %d", w.Code)
	}
	if w := do(http.MethodPost, "/api/keyboard/toggle", "https://pdh.firma.de"); w.Code != http.StatusNotFound {
		t.Errorf("toggle ist für PDH-Seiten nicht erlaubt: %d", w.Code)
	}
	if w := do(http.MethodOptions, "/api/keyboard/show", "https://pdh.firma.de"); w.Code != http.StatusNoContent {
		t.Errorf("Preflight: %d", w.Code)
	}
	if w := do(http.MethodGet, "/api/keyboard/show", "https://pdh.firma.de"); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", w.Code)
	}
	if strings.Join(kb.calls, ",") != "show,hide" {
		t.Errorf("Aufrufe: %v", kb.calls)
	}
}
