package web

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hmikiosk/internal/config"
)

const (
	oldHW = "10000000aaaa00001111"
	newHW = "10000000bbbb99998888"
)

// maintenanceEnv richtet ein Terminal ein, dessen Karte auf einem anderen Gerät läuft.
func maintenanceEnv(t *testing.T) (*adminEnv, *browser) {
	t.Helper()
	e := newEnv(t)
	e.mutate(func(c *config.Config) {
		c.Terminal.HardwareID = oldHW
		c.Terminal.Role = config.RolePrimary
		c.Peers = []config.Peer{{Name: "Terminal 2", Address: "10.0.0.2:8443"}}
	})
	e.hw = newHW
	if _, err := LoadOrCreateCert(e.tlsDir); err != nil {
		t.Fatal(err)
	}
	return e, e.login()
}

func readCert(t *testing.T, e *adminEnv) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(e.tlsDir, certFile))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func wantRedirect(t *testing.T, r *httptest.ResponseRecorder, to string) {
	t.Helper()
	if r.Code != http.StatusSeeOther || r.Header().Get("Location") != to {
		t.Errorf("Status %d nach %q, erwartet 303 nach %q", r.Code, r.Header().Get("Location"), to)
	}
}

func TestMaintenanceRedirectsEveryPage(t *testing.T) {
	_, b := maintenanceEnv(t)
	for _, path := range []string{"/admin/", "/admin/hmis", "/admin/hmis/new", "/admin/terminals", "/admin/network", "/admin/system", "/admin/system/backup", "/admin/gibtsnicht"} {
		wantRedirect(t, b.get(path), "/admin/maintenance")
	}
	csrf := b.tokenFrom("/admin/maintenance")
	for _, path := range []string{"/admin/terminals", "/admin/system/reboot", "/admin/hmis/new"} {
		wantRedirect(t, b.postRaw(path, url.Values{"csrf": {csrf}}), "/admin/maintenance")
	}
	wantRedirect(t, b.upload("/admin/system/import", [][2]string{{"csrf", csrf}}, []byte("x")), "/admin/maintenance")
}

func TestMaintenanceKeepsLogoutAssetsAndLogReachable(t *testing.T) {
	e, b := maintenanceEnv(t)
	if err := os.WriteFile(filepath.Join(e.logDir, "kiosk.log"), []byte("Zeile\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/admin/admin.css", "/admin/admin.js", "/admin/maintenance"} {
		if r := b.get(path); r.Code != http.StatusOK {
			t.Errorf("%s: %d", path, r.Code)
		}
	}
	if r := b.get("/admin/system/log"); r.Code == http.StatusSeeOther {
		t.Errorf("Protokoll umgeleitet nach %s", r.Header().Get("Location"))
	}
	wantRedirect(t, b.postVia("/admin/maintenance", "/admin/logout", nil), "/admin/login")
}

func TestMaintenanceLoginLandsOnMaintenancePage(t *testing.T) {
	e, _ := maintenanceEnv(t)
	b := e.browser()
	wantRedirect(t, b.postPage("/admin/login", url.Values{"password": {testAdminPassword}}), "/admin/hmis")
	wantRedirect(t, b.get("/admin/hmis"), "/admin/maintenance")
}

func TestMaintenanceNotRequiredWithoutMismatch(t *testing.T) {
	for name, hw := range map[string]string{"gleich": oldHW, "nicht lesbar": ""} {
		t.Run(name, func(t *testing.T) {
			e, b := maintenanceEnv(t)
			e.hw = hw
			wantRedirect(t, b.get("/admin/maintenance"), "/admin/")
			if r := b.get("/admin/hmis"); r.Code != http.StatusOK {
				t.Fatalf("HMIs: %d", r.Code)
			}
		})
	}
}

func TestMaintenancePageShowsOldAndNewIdentity(t *testing.T) {
	_, b := maintenanceEnv(t)
	body := b.get("/admin/maintenance").Body.String()
	wantContains(t, body,
		"Terminal 1", "Primär",
		"…00001111", "…99998888",
		"Als Ersatz für Terminal 1 übernehmen",
		"Das alte Gerät muss außer Betrieb sein, sonst arbeiten zwei Terminals mit derselben Kennung.",
		"Als neues Terminal einrichten",
		`action="/admin/maintenance/replace"`, `action="/admin/maintenance/new"`,
		`name="confirm"`, `name="name"`, `name="role"`)
	wantNotContains(t, body, oldHW, newHW, `href="/admin/hmis"`)
}

func TestMaintenanceReplaceKeepsIdentityAndTakesNewHardware(t *testing.T) {
	e, b := maintenanceEnv(t)
	before := e.cfg()
	oldCert := readCert(t, e)
	appliedBefore := e.appliedCount()

	r := b.postVia("/admin/maintenance", "/admin/maintenance/replace", url.Values{"confirm": {"ja"}})
	if r.Code != http.StatusSeeOther {
		t.Fatalf("Status %d: %s", r.Code, r.Body.String())
	}
	after := e.cfg()
	if after.Terminal.HardwareID != newHW {
		t.Errorf("Kennung %q", after.Terminal.HardwareID)
	}
	if after.Terminal.ID != before.Terminal.ID || after.Terminal.Name != "Terminal 1" || after.Terminal.Role != config.RolePrimary {
		t.Errorf("Identität geändert: %+v", after.Terminal)
	}
	if len(after.Peers) != 1 || after.Peers[0].Name != "Terminal 2" {
		t.Errorf("Peers: %+v", after.Peers)
	}
	if e.appliedCount() != appliedBefore+1 {
		t.Errorf("Apply %d -> %d", appliedBefore, e.appliedCount())
	}
	if readCert(t, e) == oldCert {
		t.Error("Zertifikat nicht erneuert")
	}
	if calls := e.helper.got(); len(calls) != 1 || calls[0].cmd != "restart-service" {
		t.Errorf("Helfer: %+v", calls)
	}
	if g := b.get("/admin/hmis"); g.Code != http.StatusOK {
		t.Errorf("Wartungsmodus dauert an: %d", g.Code)
	}
}

func TestMaintenanceReplaceNeedsConfirmation(t *testing.T) {
	e, b := maintenanceEnv(t)
	oldCert := readCert(t, e)
	for _, v := range []url.Values{nil, {"confirm": {"nein"}}} {
		r := b.postVia("/admin/maintenance", "/admin/maintenance/replace", v)
		if r.Code != http.StatusUnprocessableEntity {
			t.Fatalf("Status %d", r.Code)
		}
		wantContains(t, r.Body.String(), "bestätigen")
	}
	if e.cfg().Terminal.HardwareID != oldHW || readCert(t, e) != oldCert || len(e.helper.got()) != 0 {
		t.Fatal("trotz fehlender Bestätigung geändert")
	}
}

func TestMaintenanceNewTerminalGetsFreshIDNameAndRole(t *testing.T) {
	e, b := maintenanceEnv(t)
	before := e.cfg()
	oldCert := readCert(t, e)

	r := b.postVia("/admin/maintenance", "/admin/maintenance/new", url.Values{"name": {"  Presse Nord "}, "role": {"normal"}})
	if r.Code != http.StatusSeeOther {
		t.Fatalf("Status %d: %s", r.Code, r.Body.String())
	}
	after := e.cfg()
	if after.Terminal.HardwareID != newHW || after.Terminal.Name != "Presse Nord" || after.Terminal.Role != config.RoleNormal {
		t.Errorf("Terminal: %+v", after.Terminal)
	}
	if after.Terminal.ID == before.Terminal.ID || len(after.Terminal.ID) != 16 {
		t.Errorf("Kennung nicht neu: %q (vorher %q)", after.Terminal.ID, before.Terminal.ID)
	}
	if len(after.Peers) != 1 || after.Peers[0].Name != "Terminal 2" {
		t.Errorf("Der alte Name darf nicht automatisch Peer werden: %+v", after.Peers)
	}
	if readCert(t, e) == oldCert {
		t.Error("Zertifikat nicht erneuert")
	}
	if calls := e.helper.got(); len(calls) != 1 || calls[0].cmd != "restart-service" {
		t.Errorf("Helfer: %+v", calls)
	}
}

func TestMaintenanceNewTerminalRejectsBadInput(t *testing.T) {
	cases := map[string]url.Values{
		"Name leer":         {"name": {"  "}, "role": {"normal"}},
		"Name wie ein Peer": {"name": {"Terminal 2"}, "role": {"normal"}},
		"Rolle unbekannt":   {"name": {"Neu"}, "role": {"chef"}},
		"Steuerzeichen":     {"name": {"A\x01B"}, "role": {"normal"}},
		"Rolle fehlt":       {"name": {"Neu"}},
		"Name zu lang":      {"name": {strings.Repeat("x", 121)}, "role": {"normal"}},
	}
	for name, v := range cases {
		t.Run(name, func(t *testing.T) {
			e, b := maintenanceEnv(t)
			before, oldCert := e.cfg(), readCert(t, e)
			r := b.postVia("/admin/maintenance", "/admin/maintenance/new", v)
			if r.Code != http.StatusUnprocessableEntity {
				t.Fatalf("Status %d: %s", r.Code, r.Body.String())
			}
			if e.cfg().Terminal != before.Terminal || readCert(t, e) != oldCert || len(e.helper.got()) != 0 {
				t.Fatal("trotz Fehler geändert")
			}
			if g := b.get("/admin/hmis"); g.Code != http.StatusSeeOther {
				t.Fatal("Wartungsmodus verlassen")
			}
		})
	}
}

func TestMaintenancePostsNeedCSRF(t *testing.T) {
	e, b := maintenanceEnv(t)
	for _, path := range []string{"/admin/maintenance/replace", "/admin/maintenance/new"} {
		r := b.postRaw(path, url.Values{"confirm": {"ja"}, "name": {"X"}, "role": {"normal"}})
		if r.Code != http.StatusForbidden {
			t.Errorf("%s ohne CSRF: %d", path, r.Code)
		}
		r = b.postRaw(path, url.Values{"csrf": {"falsch"}, "confirm": {"ja"}, "name": {"X"}, "role": {"normal"}})
		if r.Code != http.StatusForbidden {
			t.Errorf("%s mit falschem CSRF: %d", path, r.Code)
		}
	}
	if e.cfg().Terminal.HardwareID != oldHW {
		t.Fatal("geändert")
	}
}

func TestMaintenancePostsNeedLogin(t *testing.T) {
	e, _ := maintenanceEnv(t)
	anon := e.browser()
	for _, path := range []string{"/admin/maintenance/replace", "/admin/maintenance/new"} {
		wantRedirect(t, anon.postRaw(path, url.Values{"confirm": {"ja"}, "name": {"X"}, "role": {"normal"}}), "/admin/login")
	}
	wantRedirect(t, anon.get("/admin/maintenance"), "/admin/login")
	if e.cfg().Terminal.HardwareID != oldHW {
		t.Fatal("geändert")
	}
}

func TestMaintenancePostOutsideMaintenanceIsRefused(t *testing.T) {
	e, b := maintenanceEnv(t)
	e.hw = oldHW
	before := e.cfg().Terminal
	for path, v := range map[string]url.Values{
		"/admin/maintenance/replace": {"confirm": {"ja"}},
		"/admin/maintenance/new":     {"name": {"Neu"}, "role": {"normal"}},
	} {
		wantRedirect(t, b.postVia("/admin/hmis", path, v), "/admin/")
	}
	if e.cfg().Terminal != before {
		t.Fatal("außerhalb des Wartungsmodus geändert")
	}
}

func TestMaintenanceRestartFailureStillSavesAndAdvises(t *testing.T) {
	e, b := maintenanceEnv(t)
	e.helper.err = os.ErrPermission
	defer quietLog(t)()
	r := b.postVia("/admin/maintenance", "/admin/maintenance/replace", url.Values{"confirm": {"ja"}})
	if r.Code != http.StatusSeeOther {
		t.Fatalf("Status %d: %s", r.Code, r.Body.String())
	}
	if e.cfg().Terminal.HardwareID != newHW {
		t.Fatal("nicht gespeichert")
	}
	wantContains(t, b.follow(r), "neu starten")
}

func TestMaintenanceSaveFailureKeepsModeAndCert(t *testing.T) {
	e, b := maintenanceEnv(t)
	oldCert := readCert(t, e)
	token := b.tokenFrom("/admin/maintenance")
	breakSave(e)
	r := b.postRaw("/admin/maintenance/replace", url.Values{"csrf": {token}, "confirm": {"ja"}})
	if r.Code != http.StatusInternalServerError {
		t.Fatalf("Status %d: %s", r.Code, r.Body.String())
	}
	if readCert(t, e) != oldCert || len(e.helper.got()) != 0 {
		t.Fatal("Zertifikat oder Dienst trotz Fehler angefasst")
	}
}

func TestImportKeepsLocalHardwareID(t *testing.T) {
	e := newEnv(t)
	e.mutate(func(c *config.Config) { c.Terminal.HardwareID = oldHW })
	ex := exportFile{Format: exportFormat, Config: config.Default()}
	ex.Config.Terminal.HardwareID = "fremdes-geraet"
	next, err := e.a.merge(ex, e.cfg())
	if err != nil {
		t.Fatal(err)
	}
	if next.Terminal.HardwareID != oldHW {
		t.Fatalf("Import übernahm die Kennung: %q", next.Terminal.HardwareID)
	}
}

func quietLog(t *testing.T) func() {
	t.Helper()
	log.SetOutput(io.Discard)
	return func() { log.SetOutput(os.Stderr) }
}
