package web

import (
	"net/http"
	"net/url"
	"testing"

	"hmikiosk/internal/config"
)

// pdhProduct ist ein Produkt ohne HMI-Teile mit einer Zusatzseite.
func pdhProduct() *AdminProduct {
	return &AdminProduct{
		ID: "x", Name: "PDH-Terminal", Home: "/admin/pdh", HMI: false,
		Nav:   []NavItem{{Section: "pdh", Label: "PDH", Path: "/admin/pdh"}, {Section: "network", Label: "Netzwerk", Path: "/admin/network"}, {Section: "system", Label: "System", Path: "/admin/system"}},
		Pages: map[string]string{"pdh": `{{define "content"}}PDH-Seite{{end}}`},
		Routes: []ProductRoute{
			{Method: http.MethodGet, Path: "/admin/pdh", Handle: func(f AdminFrame, w http.ResponseWriter, _ *http.Request) {
				f.Render(w, http.StatusOK, "pdh", "pdh", "PDH", "", nil)
			}},
			{Method: http.MethodPost, Path: "/admin/pdh/tun", Handle: func(_ AdminFrame, w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}},
		},
	}
}

func envWithProduct(t *testing.T, p *AdminProduct) *browser {
	t.Helper()
	e := newEnv(t)
	d := e.a.d
	d.Product = p
	e.a = newAdmin(d)
	return e.login()
}

func TestHMIKioskNavUnchanged(t *testing.T) {
	_, b := newLoggedIn(t)
	body := b.get("/admin/network").Body.String()
	wantContains(t, body,
		`<div class="brand">HMI-Kiosk<span class="terminal">Terminal 1</span></div>`,
		`<a href="/admin/hmis">HMIs</a>`,
		`<a href="/admin/client">Client</a>`,
		`<a href="/admin/kiosk">Kiosk</a>`,
		`<a href="/admin/terminals">Terminals</a>`,
		`<a href="/admin/network" aria-current="page">Netzwerk</a>`,
		`<a href="/admin/system">System</a>`,
		`<title>Netzwerk – HMI-Kiosk</title>`,
		"Fernsteuerung (PDH)",
	)
	if r := b.get("/admin/"); r.Code != http.StatusSeeOther || r.Header().Get("Location") != "/admin/hmis" {
		t.Errorf("/admin/: %d %q", r.Code, r.Header().Get("Location"))
	}
}

func TestProductWithoutHMI(t *testing.T) {
	b := envWithProduct(t, pdhProduct())
	if r := b.get("/admin/"); r.Code != http.StatusSeeOther || r.Header().Get("Location") != "/admin/pdh" {
		t.Errorf("/admin/: %d %q", r.Code, r.Header().Get("Location"))
	}
	for _, p := range []string{"/admin/hmis", "/admin/client", "/admin/kiosk", "/admin/terminals", "/admin/hmis/new"} {
		if r := b.get(p); r.Code != http.StatusNotFound {
			t.Errorf("%s: %d, erwartet 404", p, r.Code)
		}
	}
	r := b.get("/admin/pdh")
	if r.Code != http.StatusOK {
		t.Fatalf("/admin/pdh: %d", r.Code)
	}
	wantContains(t, r.Body.String(), "PDH-Seite", ">PDH</a>", ">Netzwerk</a>", "<title>PDH – PDH-Terminal</title>", `<div class="brand">PDH-Terminal`)
	wantNotContains(t, r.Body.String(), "HMIs", "HMI-Kiosk")

	net := b.get("/admin/network")
	if net.Code != http.StatusOK {
		t.Fatalf("Netzwerk: %d", net.Code)
	}
	wantContains(t, net.Body.String(), "WLAN", "Zeitserver")
	wantNotContains(t, net.Body.String(), "Fernsteuerung (PDH)")

	sys := b.get("/admin/system")
	if sys.Code != http.StatusOK {
		t.Fatalf("System: %d", sys.Code)
	}
	wantContains(t, sys.Body.String(), "SD-Sicherung", "Update-Schlüssel")
	wantNotContains(t, sys.Body.String(), "Schlüssel der anderen Terminals", "Dauerprüfung", "Kachelansicht", "Touch-Seite")
}

func TestProductHidesHMIBanner(t *testing.T) {
	e := newEnv(t)
	e.mutate(func(c *config.Config) { c.Kiosk.NetworkTiles = true })
	d := e.a.d
	d.Product = pdhProduct()
	e.a = newAdmin(d)
	b := e.login()
	wantNotContains(t, b.get("/admin/pdh").Body.String(), "Kachelseite im Netz freigegeben")
}

func TestProductRouteCSRF(t *testing.T) {
	b := envWithProduct(t, pdhProduct())
	if r := b.postRaw("/admin/pdh/tun", url.Values{}); r.Code != http.StatusForbidden {
		t.Errorf("ohne CSRF: %d, erwartet 403", r.Code)
	}
	if r := b.postVia("/admin/pdh", "/admin/pdh/tun", nil); r.Code != http.StatusNoContent {
		t.Errorf("mit CSRF: %d, erwartet 204", r.Code)
	}
}

func TestProductRouteNeedsLogin(t *testing.T) {
	e := newEnv(t)
	d := e.a.d
	d.Product = pdhProduct()
	e.a = newAdmin(d)
	r := e.browser().get("/admin/pdh")
	if r.Code != http.StatusSeeOther || r.Header().Get("Location") != "/admin/login" {
		t.Errorf("ohne Anmeldung: %d %q", r.Code, r.Header().Get("Location"))
	}
}

func TestProductPageCollision(t *testing.T) {
	for _, name := range []string{"network", "system", "login", "network.html"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("Seite %q: kein panic", name)
				}
			}()
			e := newEnv(t)
			p := pdhProduct()
			p.Pages = map[string]string{name: `{{define "content"}}x{{end}}`}
			d := e.a.d
			d.Product = p
			newAdmin(d)
		}()
	}
}

func TestProductIdleSecondsAndFrame(t *testing.T) {
	var got struct {
		logged bool
		cfgOK  bool
		helper HelperAPI
	}
	p := pdhProduct()
	p.IdleSeconds = 120
	p.Routes = append(p.Routes, ProductRoute{Method: http.MethodGet, Path: "/admin/pdh/frame", Handle: func(f AdminFrame, w http.ResponseWriter, _ *http.Request) {
		_, err := f.Load()
		got.cfgOK = err == nil
		got.helper = f.Helper()
		f.Logf("Test %d", 1)
		got.logged = true
		w.WriteHeader(http.StatusNoContent)
	}})
	e := newEnv(t)
	d := e.a.d
	d.Product = p
	e.a = newAdmin(d)
	b := e.login()
	if r := b.get("/admin/pdh/frame"); r.Code != http.StatusNoContent || !got.logged || !got.cfgOK || got.helper != e.helper {
		t.Errorf("Rahmen: %d %+v", r.Code, got)
	}
	// Am Terminal gilt die Leerlaufzeit des Produkts.
	tb := e.browser()
	tb.onTerminal = true
	if r := tb.postPage("/admin/login", url.Values{"password": {testAdminPassword}}); r.Code != http.StatusSeeOther {
		t.Fatalf("Anmeldung am Terminal: %d", r.Code)
	}
	wantContains(t, tb.get("/admin/pdh").Body.String(), `data-idle="120"`)
}
