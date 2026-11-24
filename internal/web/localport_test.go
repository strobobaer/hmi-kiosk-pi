package web

import (
	"net/http"
	"net/url"
	"sync/atomic"
	"testing"

	"hmikiosk/internal/config"
	"hmikiosk/internal/localport"
)

// usePort stellt den Port der Startseite für einen Test um (Grundwert 8080 danach wieder).
func usePort(t *testing.T, p int) {
	t.Helper()
	localport.Set(p)
	t.Cleanup(func() { localport.Set(0) })
}

func TestLocalPagesFollowConfiguredPort(t *testing.T) {
	usePort(t, 18080)
	srv := serve(t, newFakeSession(), nil, "auto")
	for _, host := range []string{"127.0.0.1:18080", "localhost:18080"} {
		if code := doReq(t, http.MethodGet, srv.URL+"/", host, nil); code != http.StatusOK {
			t.Errorf("Host %s: %d", host, code)
		}
	}
	// Der Grundwert gilt nicht mehr, und nur dieser Port.
	for _, host := range []string{"127.0.0.1:8080", "localhost:8080", "127.0.0.1:18081", "127.0.0.1", "evil.example:18080", "127.0.0.1:18080.evil.example", ""} {
		if code := doReq(t, http.MethodGet, srv.URL+"/", host, nil); code != http.StatusForbidden {
			t.Errorf("Host %q: %d, erwartet 403", host, code)
		}
	}
}

func TestLocalPostOriginFollowsConfiguredPort(t *testing.T) {
	usePort(t, 18080)
	fs := newFakeSession()
	srv := serve(t, fs, nil, "auto")
	const host = "127.0.0.1:18080"
	for _, hdr := range []map[string]string{
		{"Origin": "http://127.0.0.1:18080"},
		{"Origin": "http://localhost:18080"},
		{"Referer": "http://127.0.0.1:18080/bar"},
	} {
		if code := doReq(t, http.MethodPost, srv.URL+"/api/back", host, hdr); code != http.StatusNoContent {
			t.Errorf("%v: %d", hdr, code)
		}
	}
	for _, hdr := range []map[string]string{
		{"Origin": "http://127.0.0.1:8080"}, // Grundwert
		{"Origin": "http://127.0.0.1:18081"},
		{"Origin": "https://127.0.0.1:18080"},
		{"Origin": "http://evil.example"},
		{"Referer": "http://127.0.0.1:18080.evil.example/"},
		{},
	} {
		if code := doReq(t, http.MethodPost, srv.URL+"/api/back", host, hdr); code != http.StatusForbidden {
			t.Errorf("%v: %d, erwartet 403", hdr, code)
		}
	}
}

func TestNetworkTilesUseConfiguredPort(t *testing.T) {
	usePort(t, 18080)
	var on atomic.Bool
	on.Store(true)
	srv := serveNet(t, newFakeSession(), &on)
	if code := doReq(t, http.MethodGet, srv.URL+"/", "192.168.1.20:18080", nil); code != http.StatusOK {
		t.Errorf("IP mit Port 18080: %d", code)
	}
	for _, host := range []string{"192.168.1.20:8080", "192.168.1.20:18081"} {
		if code := doReq(t, http.MethodGet, srv.URL+"/", host, nil); code != http.StatusForbidden {
			t.Errorf("Host %s: %d, erwartet 403", host, code)
		}
	}
	if !ipHost("192.168.1.20:18080") || ipHost("192.168.1.20:8080") {
		t.Error("ipHost")
	}
}

func TestGearGoesToConfiguredAdminPort(t *testing.T) {
	usePort(t, 18080)
	if adminURLFor("127.0.0.1:18080") != "http://127.0.0.1:18081/admin/" {
		t.Errorf("Zahnrad am Terminal: %q", adminURLFor("127.0.0.1:18080"))
	}
	if LocalAdminURL() != "http://127.0.0.1:18081/admin/" || KioskURL() != "http://127.0.0.1:18080/" {
		t.Errorf("Adressen: %s %s", LocalAdminURL(), KioskURL())
	}
}

func TestLocalAdminFollowsConfiguredPort(t *testing.T) {
	usePort(t, 18080)
	e := newEnv(t)
	for _, host := range []string{"127.0.0.1:18081", "localhost:18081"} {
		if w := serveLocal(e, localReq(http.MethodGet, host, "/admin/login", nil)); w.Code != http.StatusOK {
			t.Errorf("Host %s: %d", host, w.Code)
		}
	}
	for _, host := range []string{"127.0.0.1:8081", "localhost:8081", "127.0.0.1:18080", "127.0.0.1:18082", "evil.example:18081", ""} {
		if w := serveLocal(e, localReq(http.MethodGet, host, "/admin/login", nil)); w.Code != http.StatusForbidden {
			t.Errorf("Host %q: %d, erwartet 403", host, w.Code)
		}
	}
	// Schreibzugriffe brauchen den Ursprung des neuen Ports.
	post := func(origin string) int {
		hdr := map[string]string{}
		if origin != "" {
			hdr["Origin"] = origin
		}
		return serveLocal(e, localReq(http.MethodPost, "127.0.0.1:18081", "/admin/local/back", hdr)).Code
	}
	if c := post("http://127.0.0.1:18081"); c != http.StatusSeeOther {
		t.Errorf("richtiger Ursprung: %d", c)
	}
	for _, o := range []string{"http://127.0.0.1:8081", "http://127.0.0.1:18080", "http://evil.example", ""} {
		if c := post(o); c != http.StatusForbidden {
			t.Errorf("Ursprung %q: %d, erwartet 403", o, c)
		}
	}
	// Zurück führt zur Startseite am neuen Port.
	w := serveLocal(e, localReq(http.MethodPost, "127.0.0.1:18081", "/admin/local/back", map[string]string{"Origin": "http://127.0.0.1:18081"}))
	if loc := w.Header().Get("Location"); loc != "http://127.0.0.1:18080/" {
		t.Errorf("Rückweg: %q", loc)
	}
	// Anmeldung am Terminal über den neuen Port.
	form := url.Values{"password": {testAdminPassword}}
	b := e.terminal()
	if r := b.postPage("/admin/login", form); r.Code != http.StatusSeeOther {
		t.Errorf("Anmeldung: %d", r.Code)
	}
}

// Der Port gehört zum Rechner: Weder das Speichern des Kiosk-Formulars noch der Import ändert ihn.
func TestKioskSaveAndImportKeepLocalPort(t *testing.T) {
	e, b := newLoggedIn(t)
	e.mutate(func(c *config.Config) { c.Kiosk.LocalPort = 18080 })
	r := b.post("/admin/kiosk", url.Values{"inactivity": {"15"}, "layout": {"auto"}, "permanent": {""}, "input_mode": {"touch"}})
	if r.Code != http.StatusSeeOther || e.cfg().Kiosk.LocalPort != 18080 {
		t.Fatalf("Kiosk-Formular: %d, Port %d", r.Code, e.cfg().Kiosk.LocalPort)
	}

	src, sb := newLoggedIn(t)
	src.mutate(func(c *config.Config) { c.Kiosk.LocalPort = 19000; c.Kiosk.Layout = "4" })
	blob := exportBlob(t, sb)
	wantContains(t, importBlob(t, b, blob, exportPW), "importiert")
	if k := e.cfg().Kiosk; k.LocalPort != 18080 || k.Layout != "4" {
		t.Errorf("Import: %+v", k)
	}
	// Ein Gerät ohne eigenen Wert bekommt keinen aus der Datei.
	dst, db := newLoggedIn(t)
	wantContains(t, importBlob(t, db, blob, exportPW), "importiert")
	if dst.cfg().Kiosk.LocalPort != 0 {
		t.Errorf("Port aus dem Import übernommen: %d", dst.cfg().Kiosk.LocalPort)
	}
}
