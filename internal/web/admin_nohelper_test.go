package web

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// noHelperEnv ist ein angemeldeter Browser an einem Admin ohne Helfer (Windows).
func noHelperEnv(t *testing.T) (*adminEnv, *browser) {
	t.Helper()
	e := newEnv(t)
	e.a.d.Helper, e.a.d.Backup, e.a.d.Net, e.a.noHelper = noHelperAPI{}, nil, nil, true
	return e, e.login()
}

func TestNewAdminWithoutHelperUsesNoHelperAPI(t *testing.T) {
	a := newAdmin(AdminDeps{})
	if !a.noHelper {
		t.Fatal("noHelper nicht gesetzt")
	}
	if err := a.d.Helper.Call(context.Background(), "reboot", ""); err == nil || !strings.Contains(err.Error(), NoHelperText) {
		t.Errorf("Fehler: %v", err)
	}
}

func TestNoHelperHidesNetworkNavAndSections(t *testing.T) {
	_, b := noHelperEnv(t)
	r := b.get("/admin/system")
	body := r.Body.String()
	if r.Code != 200 {
		t.Fatalf("Status %d", r.Code)
	}
	wantContains(t, body, `href="/admin/network"`) // HMI: Fingerabdruck und Schlüssel bleiben
	for _, gone := range []string{"Uhrzeit setzen", "SD-Sicherung öffnen", "Dienst neu starten", "Terminal neu starten", "Grafikprofil", "Wartungsmodus"} {
		if strings.Contains(body, gone) {
			t.Errorf("%q trotz fehlendem Helfer", gone)
		}
	}
	wantContains(t, body, NoHelperText)
	wantContains(t, b.get("/admin/network").Body.String(), NoHelperText)
}

func TestNoHelperNetworkPageKeepsFingerprintAndKey(t *testing.T) {
	e, b := noHelperEnv(t)
	e.a.d.TLSFingerprint = func() string { return "ab:cd" }
	page := b.get("/admin/network").Body.String()
	wantContains(t, page, NoHelperText, "Fernsteuerung (PDH)", "ab:cd", `action="/admin/network/remote/generate"`)
	wantNotContains(t, page, "Ethernet (IPv4)", "<h2>WLAN</h2>", "<h2>Bluetooth</h2>", "Zeitserver", `action="/admin/network/ntp"`)
}

func TestNoHelperRemoteKeyGenerationWorks(t *testing.T) {
	_, b := noHelperEnv(t)
	r := b.post("/admin/network/remote/generate", nil)
	if r.Code != http.StatusSeeOther {
		t.Fatalf("Status %d", r.Code)
	}
	page := b.get(r.Header().Get("Location"))
	if !remoteKeyRe.MatchString(page.Body.String()) {
		t.Fatalf("kein Schlüssel angezeigt: %s", page.Body.String())
	}
	wantContains(t, page.Body.String(), "Status: an")
}

func TestLocalRootRedirectsToAdmin(t *testing.T) {
	e := newEnv(t)
	for _, m := range []string{http.MethodGet, http.MethodHead} {
		w := serveLocal(e, localReq(m, "127.0.0.1:8081", "/", nil))
		if w.Code != http.StatusFound || w.Header().Get("Location") != "/admin/" {
			t.Errorf("%s /: %d %q", m, w.Code, w.Header().Get("Location"))
		}
	}
}

// Ohne Grafikprofil im Formular bleibt der gespeicherte Wert beim Speichern der Anzeige erhalten.
func TestNoHelperDisplayKeepsGraphicsProfile(t *testing.T) {
	_, b := noHelperEnv(t)
	body := b.get("/admin/system").Body.String()
	wantContains(t, body, `name="graphics" value="`)
}
