package web

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"hmikiosk/internal/config"
	"hmikiosk/internal/rfb"
)

func markFailed(e *adminEnv, id string) {
	e.mutate(func(c *config.Config) {
		for i := range c.HMIs {
			if c.HMIs[i].ID == id {
				c.HMIs[i].AuthFailed = true
			}
		}
	})
}

func TestHMIFormOffersReachChoiceWithDefaultPing(t *testing.T) {
	e, b := newLoggedIn(t)
	body := b.get("/admin/hmis/new").Body.String()
	wantContains(t, body, "Erreichbarkeit prüfen", `name="reach"`,
		"Ping (Standard)", "VNC-Port", "aus", "VNC-Port kann bei RealVNC zur Sperre führen")
	if !strings.Contains(body, `<option value="ping" selected>`) {
		t.Fatalf("Ping nicht vorgewählt:\n%s", body)
	}
	e.addHMI("x", "Presse", "")
	e.mutate(func(c *config.Config) { c.HMIs[0].Reach = config.ReachPort })
	if !strings.Contains(b.get("/admin/hmis/x").Body.String(), `<option value="port" selected>`) {
		t.Fatal("gespeicherte Prüfart nicht gewählt")
	}
	// Alte Konfiguration ohne Feld zeigt Ping.
	e.mutate(func(c *config.Config) { c.HMIs[0].Reach = "" })
	if !strings.Contains(b.get("/admin/hmis/x").Body.String(), `<option value="ping" selected>`) {
		t.Fatal("alte Konfiguration zeigt nicht Ping")
	}
}

func TestHMIFormSavesAndValidatesReach(t *testing.T) {
	e, b := newLoggedIn(t)
	for _, mode := range []string{"ping", "port", "off"} {
		f := hmiForm("Presse", "10.0.0.5", "5900", "")
		f.Set("reach", mode)
		if r := b.post("/admin/hmis/new", f); r.Code != http.StatusSeeOther {
			t.Fatalf("%s: Status %d", mode, r.Code)
		}
		cfg := e.cfg()
		if got := cfg.HMIs[len(cfg.HMIs)-1].Reach; got != mode {
			t.Fatalf("gespeichert %q statt %q", got, mode)
		}
	}
	n := len(e.cfg().HMIs)
	f := hmiForm("Presse", "10.0.0.5", "5900", "")
	f.Set("reach", "tcp")
	r := b.post("/admin/hmis/new", f)
	if r.Code != http.StatusUnprocessableEntity || len(e.cfg().HMIs) != n {
		t.Fatalf("ungültige Prüfart: Status %d", r.Code)
	}
	wantContains(t, r.Body.String(), "Erreichbarkeit")
}

func TestPasswordChangeOrRemovalClearsAuthFailed(t *testing.T) {
	e, b := newLoggedIn(t)
	e.addHMI("x", "Presse", "alt")
	markFailed(e, "x")
	// Speichern ohne Passwortänderung behält die Markierung.
	if r := b.post("/admin/hmis/x", hmiForm("Presse", "10.0.0.5", "5900", "")); r.Code != http.StatusSeeOther {
		t.Fatalf("Status %d", r.Code)
	}
	if !e.hmi("x").AuthFailed {
		t.Fatal("Markierung ohne Passwortänderung gelöscht")
	}
	if r := b.post("/admin/hmis/x", hmiForm("Presse", "10.0.0.5", "5900", "neu")); r.Code != http.StatusSeeOther {
		t.Fatalf("Status %d", r.Code)
	}
	if e.hmi("x").AuthFailed {
		t.Fatal("Markierung nach neuem Passwort geblieben")
	}
	markFailed(e, "x")
	f := hmiForm("Presse", "10.0.0.5", "5900", "")
	f.Set("remove_password", "on")
	if r := b.post("/admin/hmis/x", f); r.Code != http.StatusSeeOther {
		t.Fatalf("Status %d", r.Code)
	}
	if e.hmi("x").AuthFailed {
		t.Fatal("Markierung nach entferntem Passwort geblieben")
	}
}

func TestSuccessfulProbeClearsAuthFailedButFailureKeepsIt(t *testing.T) {
	e, b := newLoggedIn(t)
	e.addHMI("x", "Presse", "pw")
	markFailed(e, "x")
	e.probe = func(context.Context, config.HMI) rfb.ProbeResult {
		return rfb.ProbeResult{Reachable: true, AuthRejected: true, Err: "Anmeldung abgelehnt"}
	}
	b.post("/admin/hmis/x/probe", nil)
	if !e.hmi("x").AuthFailed {
		t.Fatal("abgelehnte Prüfung löscht die Markierung")
	}
	e.probe = func(context.Context, config.HMI) rfb.ProbeResult {
		return rfb.ProbeResult{Reachable: true, AuthOK: true, Width: 64, Height: 48}
	}
	r := b.post("/admin/hmis/x/probe", nil)
	if r.Code != http.StatusOK || e.hmi("x").AuthFailed {
		t.Fatalf("Status %d, Markierung %v", r.Code, e.hmi("x").AuthFailed)
	}
}

func TestProbeStillAllowedForMarkedHMIAndShowsLockoutHint(t *testing.T) {
	e, b := newLoggedIn(t)
	e.addHMI("x", "Presse", "pw")
	markFailed(e, "x")
	called := false
	e.probe = func(context.Context, config.HMI) rfb.ProbeResult {
		called = true
		return rfb.ProbeResult{Reachable: true, Locked: true, Err: "Der VNC-Server lehnt die Verbindung ab: „Too many security failures“"}
	}
	body := b.post("/admin/hmis/x/probe", nil).Body.String()
	if !called {
		t.Fatal("bewusster Einzelversuch nicht ausgeführt")
	}
	wantContains(t, body, "vorübergehend gesperrt (zu viele Fehlversuche)", "VNC-Dienst am HMI neu starten", "Too many security failures")
	if !e.hmi("x").AuthFailed {
		t.Fatal("Sperre löscht die Markierung")
	}
}

func TestHMIListShowsRejectedPassword(t *testing.T) {
	e, b := newLoggedIn(t)
	e.addHMI("x", "Presse", "pw")
	wantNotContains(t, b.get("/admin/hmis").Body.String(), "Passwort abgelehnt")
	markFailed(e, "x")
	wantContains(t, b.get("/admin/hmis").Body.String(), "Passwort abgelehnt")
}

func TestImportDoesNotTakeOverAuthFailed(t *testing.T) {
	src, sb := newLoggedIn(t)
	src.addHMI("x", "Presse", "vnc")
	src.mutate(func(c *config.Config) { c.HMIs[0].AuthFailed, c.HMIs[0].Reach = true, config.ReachOff })
	blob := exportBlob(t, sb)
	dst, db := newLoggedIn(t)
	wantContains(t, importBlob(t, db, blob, exportPW), "importiert")
	h := dst.hmi("x")
	if h.AuthFailed || h.Reach != config.ReachOff {
		t.Fatalf("Import: %+v", h)
	}
}

func TestTileShowsWrongPassword(t *testing.T) {
	srv := serve(t, newFakeSession(), []TileView{
		{ID: "a", Name: "Presse", Icon: "press", Color: "blue", Reachable: true, AuthFailed: true},
		{ID: "b", Name: "Ofen", Icon: "press", Color: "blue", Reachable: true},
	}, "auto")
	body, _ := get(t, srv.URL+"/")
	if n := strings.Count(body, "Passwort falsch"); n != 1 {
		t.Fatalf("%d Kacheln mit „Passwort falsch“:\n%s", n, body)
	}
}
