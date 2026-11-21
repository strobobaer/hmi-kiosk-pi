package web

import (
	"context"
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
	if strings.Contains(body, `href="/admin/network"`) {
		t.Error("Netzwerk im Menü")
	}
	for _, gone := range []string{"Uhrzeit setzen", "SD-Sicherung öffnen", "Dienst neu starten", "Terminal neu starten", "Grafikprofil", "Wartungsmodus"} {
		if strings.Contains(body, gone) {
			t.Errorf("%q trotz fehlendem Helfer", gone)
		}
	}
	wantContains(t, body, NoHelperText)
	wantContains(t, b.get("/admin/network").Body.String(), NoHelperText)
}

// Ohne Grafikprofil im Formular bleibt der gespeicherte Wert beim Speichern der Anzeige erhalten.
func TestNoHelperDisplayKeepsGraphicsProfile(t *testing.T) {
	_, b := noHelperEnv(t)
	body := b.get("/admin/system").Body.String()
	wantContains(t, body, `name="graphics" value="`)
}
