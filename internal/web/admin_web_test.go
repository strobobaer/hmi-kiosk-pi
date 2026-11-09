package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"hmikiosk/internal/config"
	"hmikiosk/internal/rfb"
	"hmikiosk/internal/session"
)

func webForm(name, u string) url.Values {
	return url.Values{
		"kind": {"web"}, "name": {name}, "url": {u},
		"icon": {"monitor"}, "color": {"green"}, "order": {"1"}, "reach": {"ping"},
		// Felder der anderen Art, wie sie ein Browser ohne Skript mitschickt: ohne Wirkung.
		"host": {""}, "port": {"5900"}, "password": {"soll-nicht-gespeichert-werden"}, "view_only": {"on"},
	}
}

func TestCreateWebTile(t *testing.T) {
	e, b := newLoggedIn(t)
	r := b.post("/admin/hmis/new", webForm("Grafana Linie 1", " http://grafana:3000/d/abc?kiosk&refresh=30s "))
	if r.Code != http.StatusSeeOther {
		t.Fatalf("Status %d: %s", r.Code, r.Body.String())
	}
	h := e.cfg().HMIs[0]
	if h.Kind != config.KindWeb || h.URL != "http://grafana:3000/d/abc?kiosk&refresh=30s" || h.Name != "Grafana Linie 1" {
		t.Fatalf("Kachel %+v", h)
	}
	if h.Host != "" || h.Port != 0 || h.PasswordEnc != "" || h.ViewOnly || h.AllowWatch || h.Mode != nil || h.Reach != config.ReachPing {
		t.Fatalf("VNC-Felder gesetzt: %+v", h)
	}
	list := b.get("/admin/hmis").Body.String()
	wantContains(t, list, "Grafana Linie 1", "http://grafana:3000/d/abc?kiosk&amp;refresh=30s", "Webseite")
	wantNotContains(t, list, "Ohne Passwort")
}

func TestWebTileRejectsBadURL(t *testing.T) {
	e, b := newLoggedIn(t)
	for u, msg := range map[string]string{
		"ftp://grafana/":               "http:// oder https://",
		"http://admin:pw@grafana:3000": "Benutzer",
		"":                             "Adresse fehlt",
		"http://grafana:99999/":        "Port",
	} {
		r := b.post("/admin/hmis/new", webForm("G", u))
		if r.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%q: Status %d", u, r.Code)
		}
		body := r.Body.String()
		wantContains(t, body, msg)
		// Das Formular bleibt bei der Art Webseite mit der Eingabe stehen.
		wantContains(t, body, `<option value="web" selected`)
		if u == "http://admin:pw@grafana:3000" {
			wantNotContains(t, body, "admin:pw@")
		}
	}
	if n := len(e.cfg().HMIs); n != 0 {
		t.Fatalf("%d HMIs gespeichert", n)
	}
}

func TestVNCFormStillWorksWithoutKind(t *testing.T) {
	e, b := newLoggedIn(t)
	if r := b.post("/admin/hmis/new", hmiForm("Presse", "10.0.0.5", "5900", "")); r.Code != http.StatusSeeOther {
		t.Fatalf("Status %d: %s", r.Code, r.Body.String())
	}
	if h := e.cfg().HMIs[0]; h.Kind != "" || h.URL != "" || h.Host != "10.0.0.5" {
		t.Fatalf("Kachel %+v", h)
	}
}

func TestSwitchVNCToWebDropsVNCFields(t *testing.T) {
	e, b := newLoggedIn(t)
	e.addHMI("x", "Alt", "alt-pw")
	if r := b.post("/admin/hmis/x", webForm("Grafana", "https://grafana.werk/d/x")); r.Code != http.StatusSeeOther {
		t.Fatalf("Status %d: %s", r.Code, r.Body.String())
	}
	h := e.hmi("x")
	if !h.IsWeb() || h.PasswordEnc != "" || h.Host != "" || h.Port != 0 {
		t.Fatalf("Kachel %+v", h)
	}
	// Zurück zu VNC verlangt wieder Adresse und Port.
	f := hmiForm("Presse", "", "5900", "")
	f.Set("kind", "vnc")
	if r := b.post("/admin/hmis/x", f); r.Code != http.StatusUnprocessableEntity {
		t.Fatalf("VNC ohne Host: Status %d", r.Code)
	}
	f.Set("host", "10.0.0.9")
	if r := b.post("/admin/hmis/x", f); r.Code != http.StatusSeeOther {
		t.Fatalf("Status %d: %s", r.Code, r.Body.String())
	}
	if h := e.hmi("x"); h.IsWeb() || h.URL != "" || h.Host != "10.0.0.9" {
		t.Fatalf("Kachel %+v", h)
	}
}

func TestHMIFormOffersBothKinds(t *testing.T) {
	_, b := newLoggedIn(t)
	body := b.get("/admin/hmis/new").Body.String()
	wantContains(t, body, `name="kind"`, `value="vnc"`, `value="web"`, `name="url"`, "?kiosk", `data-kind="web"`, `data-kind="vnc"`)
	// Ohne Wahl ist VNC aktiv, der Web-Teil verborgen und abgeschaltet (kein required im Weg).
	if !strings.Contains(body, `data-kind="web" hidden disabled`) {
		t.Fatalf("Web-Teil nicht verborgen:\n%s", body)
	}
	body = b.get("/admin/hmis/new?kind=web").Body.String()
	wantContains(t, body, `<option value="web" selected`, `data-kind="vnc" hidden disabled`)
	list := b.get("/admin/hmis").Body.String()
	wantContains(t, list, `href="/admin/hmis/new?kind=web"`)
}

func TestEditWebTileShowsURL(t *testing.T) {
	e, b := newLoggedIn(t)
	e.mutate(func(c *config.Config) {
		c.HMIs = append(c.HMIs, config.HMI{ID: "w", Name: "Grafana", Kind: config.KindWeb, URL: "http://grafana:3000/d/a?kiosk", Icon: "monitor", Color: "blue"})
	})
	body := b.get("/admin/hmis/w").Body.String()
	wantContains(t, body, `value="http://grafana:3000/d/a?kiosk"`, `<option value="web" selected`)
}

func TestWebTileProbeShowsReachabilityOnly(t *testing.T) {
	e, b := newLoggedIn(t)
	e.mutate(func(c *config.Config) {
		c.HMIs = append(c.HMIs, config.HMI{ID: "w", Name: "Grafana", Kind: config.KindWeb, URL: "http://grafana:3000/", Icon: "monitor", Color: "blue"})
	})
	e.probe = func(_ context.Context, h config.HMI) rfb.ProbeResult { return rfb.ProbeResult{Reachable: true} }
	body := b.post("/admin/hmis/w/probe", nil).Body.String()
	wantContains(t, body, "Erreichbar")
	wantNotContains(t, body, "Anmeldung")
}

func TestClearWebLogins(t *testing.T) {
	e, b := newLoggedIn(t)
	calls := 0
	var answer error
	e.a.d.ClearWebLogins = func() error { calls++; return answer }
	page := b.get("/admin/kiosk").Body.String()
	wantContains(t, page, "Gespeicherte Web-Anmeldungen löschen", `action="/admin/kiosk/web-logins/delete"`)
	r := b.postVia("/admin/kiosk", "/admin/kiosk/web-logins/delete", nil)
	wantContains(t, b.follow(r), "Gespeicherte Web-Anmeldungen gelöscht")
	answer = session.ErrWebOpen
	r = b.postVia("/admin/kiosk", "/admin/kiosk/web-logins/delete", nil)
	if r.Code != http.StatusConflict {
		t.Fatalf("Status %d", r.Code)
	}
	wantContains(t, r.Body.String(), "Web-Kachel ist geöffnet")
	answer = errors.New("Platte voll")
	r = b.postVia("/admin/kiosk", "/admin/kiosk/web-logins/delete", nil)
	if r.Code != http.StatusInternalServerError {
		t.Fatalf("Status %d", r.Code)
	}
	if calls != 3 {
		t.Fatalf("%d Aufrufe", calls)
	}
}

func TestClearWebLoginsHiddenWithoutSupport(t *testing.T) {
	_, b := newLoggedIn(t)
	wantNotContains(t, b.get("/admin/kiosk").Body.String(), "web-logins")
	if r := b.postVia("/admin/kiosk", "/admin/kiosk/web-logins/delete", nil); r.Code != http.StatusNotFound {
		t.Fatalf("Status %d", r.Code)
	}
}

func TestExportImportCarriesWebTiles(t *testing.T) {
	src, sb := newLoggedIn(t)
	src.addHMI("x", "Presse", "vnc-geheim")
	src.mutate(func(c *config.Config) {
		c.HMIs = append(c.HMIs, config.HMI{ID: "w", Name: "Grafana", Kind: config.KindWeb, URL: "http://grafana:3000/d/a?kiosk", Icon: "monitor", Color: "blue", Reach: config.ReachOff})
		c.Kiosk.PermanentHMI = "w"
	})
	blob := exportBlob(t, sb)
	dst, db := newLoggedIn(t)
	wantContains(t, importBlob(t, db, blob, exportPW), "importiert")
	w := dst.hmi("w")
	if !w.IsWeb() || w.URL != "http://grafana:3000/d/a?kiosk" || w.Reach != config.ReachOff || w.PasswordEnc != "" {
		t.Fatalf("Web-Kachel %+v", w)
	}
	if dst.cfg().Kiosk.PermanentHMI != "w" {
		t.Fatal("Daueranzeige nicht übernommen")
	}
	if got, err := dst.box.Open(dst.hmi("x").PasswordEnc); err != nil || got != "vnc-geheim" {
		t.Fatalf("VNC-Passwort: %q %v", got, err)
	}
}
