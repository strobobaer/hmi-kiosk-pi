package web

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"hmikiosk/internal/config"
	"hmikiosk/internal/rfb"
)

func hmiForm(name, host, port, password string) url.Values {
	return url.Values{
		"name": {name}, "host": {host}, "port": {port}, "password": {password},
		"icon": {"press"}, "color": {"green"}, "order": {"3"},
		"mode": {""}, "takeover": {"confirm"}, "allow_watch": {"on"},
	}
}

func TestSaveHMIEncryptsPassword(t *testing.T) {
	e, b := newLoggedIn(t)
	r := b.post("/admin/hmis/new", hmiForm("Presse 1", "10.0.0.5", "5900", "vnc-klartext"))
	if r.Code != http.StatusSeeOther {
		t.Fatalf("Status %d: %s", r.Code, r.Body.String())
	}
	c := e.cfg()
	if len(c.HMIs) != 1 {
		t.Fatalf("HMIs: %d", len(c.HMIs))
	}
	h := c.HMIs[0]
	if !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(h.ID) {
		t.Errorf("Kennung %q", h.ID)
	}
	if h.Name != "Presse 1" || h.Icon != "press" || h.Color != "green" || h.Order != 3 || !h.AllowWatch || h.ViewOnly || h.Mode != nil {
		t.Errorf("Felder: %+v", h)
	}
	plain, err := e.box.Open(h.PasswordEnc)
	if err != nil || plain != "vnc-klartext" {
		t.Errorf("Passwort %q, %v", plain, err)
	}
	raw, err := os.ReadFile(filepath.Join(e.dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	wantNotContains(t, string(raw), "vnc-klartext")
	if e.appliedCount() != 1 {
		t.Errorf("Apply %d mal", e.appliedCount())
	}
}

func TestHMIEditSetsAllFields(t *testing.T) {
	e, b := newLoggedIn(t)
	e.addHMI("x", "Alt", "alt-pw")
	f := hmiForm("Neu", "presse.local", "5901", "")
	f.Set("mode", "lock")
	f.Set("takeover", "immediate")
	f.Set("view_only", "on")
	f.Del("allow_watch")
	f.Set("icon", "tank")
	f.Set("color", "red")
	if r := b.post("/admin/hmis/x", f); r.Code != http.StatusSeeOther {
		t.Fatalf("Status %d: %s", r.Code, r.Body.String())
	}
	h := e.hmi("x")
	if h.Name != "Neu" || h.Host != "presse.local" || h.Port != 5901 || !h.ViewOnly || h.AllowWatch ||
		h.Mode == nil || *h.Mode != config.ModeLock || h.Takeover != config.StyleImmediate || h.Icon != "tank" || h.Color != "red" {
		t.Errorf("Felder: %+v", h)
	}
}

func TestHMIRejectsValuesOutsideClosedSets(t *testing.T) {
	e, b := newLoggedIn(t)
	for field, val := range map[string]string{"icon": "<script>", "color": "pink"} {
		f := hmiForm("Presse", "10.0.0.5", "5900", "")
		f.Set(field, val)
		if r := b.post("/admin/hmis/new", f); r.Code == http.StatusSeeOther {
			t.Errorf("%s=%q angenommen", field, val)
		}
	}
	if len(e.cfg().HMIs) != 0 {
		t.Error("gespeichert")
	}
}

func TestEmptyPasswordKeepsOldOnEdit(t *testing.T) {
	e, b := newLoggedIn(t)
	e.addHMI("x", "Presse", "bleibt")
	if r := b.post("/admin/hmis/x", hmiForm("Presse 2", "10.0.0.5", "5900", "")); r.Code != http.StatusSeeOther {
		t.Fatalf("Status %d", r.Code)
	}
	h := e.hmi("x")
	if plain, err := e.box.Open(h.PasswordEnc); err != nil || plain != "bleibt" {
		t.Errorf("Passwort %q %v", plain, err)
	}
	if h.Name != "Presse 2" {
		t.Errorf("Name %q", h.Name)
	}
}

func TestRemovePasswordCheckboxClears(t *testing.T) {
	e, b := newLoggedIn(t)
	e.addHMI("x", "Presse", "weg")
	f := hmiForm("Presse", "10.0.0.5", "5900", "")
	f.Set("remove_password", "on")
	if r := b.post("/admin/hmis/x", f); r.Code != http.StatusSeeOther {
		t.Fatalf("Status %d", r.Code)
	}
	if e.hmi("x").PasswordEnc != "" {
		t.Error("nicht entfernt")
	}
	// Beides zugleich ist widersprüchlich: nichts ändern
	e.addHMI("y", "Tank", "bleibt")
	old := e.hmi("y").PasswordEnc
	f.Set("password", "neu")
	if r := b.post("/admin/hmis/y", f); r.Code == http.StatusSeeOther {
		t.Error("widersprüchliche Angabe angenommen")
	}
	if e.hmi("y").PasswordEnc != old {
		t.Error("Passwort geändert")
	}
}

func TestPasswordNeverReturnedInResponses(t *testing.T) {
	e, b := newLoggedIn(t)
	const pw = "streng-geheim-vnc"
	if r := b.post("/admin/hmis/new", hmiForm("Presse", "10.0.0.5", "5900", pw)); r.Code != http.StatusSeeOther {
		t.Fatal(r.Code)
	}
	h := e.cfg().HMIs[0]
	if r := b.post("/admin/terminals/key", url.Values{"shared_key": {"handeingabe-des-schluessels"}}); r.Code != http.StatusSeeOther {
		t.Fatal(r.Code)
	}
	c := e.cfg()
	secretsList := []string{pw, h.PasswordEnc, c.SharedKeyEnc, c.Admin.PasswordHash, testAdminPassword, "handeingabe-des-schluessels"}

	pages := []string{"/admin/hmis", "/admin/hmis/new", "/admin/hmis/" + h.ID, "/admin/client", "/admin/kiosk", "/admin/terminals", "/admin/network", "/admin/system"}
	for _, p := range pages {
		r := b.get(p)
		if r.Code != http.StatusOK {
			t.Errorf("%s: %d", p, r.Code)
		}
		wantNotContains(t, r.Body.String(), secretsList...)
	}
	// Fehlerseiten dürfen die Eingabe nicht zurückgeben
	r := b.post("/admin/hmis/"+h.ID, hmiForm("Presse", "bad host;", "5900", "neues-eingegebenes-pw"))
	if r.Code == http.StatusSeeOther {
		t.Fatal("ungültiger Host angenommen")
	}
	wantNotContains(t, r.Body.String(), append(secretsList, "neues-eingegebenes-pw")...)
	r = b.post("/admin/hmis/new", hmiForm("Presse", "10.0.0.5", "kein-port", "noch-ein-pw"))
	wantNotContains(t, r.Body.String(), "noch-ein-pw")
	r = b.post("/admin/terminals/key", url.Values{"shared_key": {"kurz"}})
	wantNotContains(t, r.Body.String(), `value="kurz"`)
	// Auch das Ergebnis der Verbindungsprüfung enthält keine Geheimnisse
	r = b.post("/admin/hmis/"+h.ID+"/probe", nil)
	wantNotContains(t, r.Body.String(), secretsList...)
	// Anmeldung gibt das eingegebene Passwort nicht zurück
	r = e.browser().postPage("/admin/login", url.Values{"password": {"falsches-eingegebenes-pw"}})
	wantNotContains(t, r.Body.String(), "falsches-eingegebenes-pw")
}

func TestValidateErrorRerendersEnteredValues(t *testing.T) {
	e, b := newLoggedIn(t)
	r := b.post("/admin/hmis/new", hmiForm("Eingegebener Name", "bad host;", "5900", "eingabe-pw"))
	if r.Code != http.StatusUnprocessableEntity {
		t.Fatalf("Status %d", r.Code)
	}
	wantContains(t, r.Body.String(), "ungültig", `value="Eingegebener Name"`, "bad host;")
	wantNotContains(t, r.Body.String(), "eingabe-pw")
	if e.appliedCount() != 0 {
		t.Error("Apply gerufen")
	}
	// Eingabe, die keine Zahl ist, bleibt ebenfalls stehen
	r = b.post("/admin/hmis/new", hmiForm("Name", "10.0.0.5", "abc", ""))
	if r.Code != http.StatusUnprocessableEntity {
		t.Fatalf("Status %d", r.Code)
	}
	wantContains(t, r.Body.String(), "Port", `value="abc"`)
}

func TestSaveFailureKeepsOldConfigAndShowsError(t *testing.T) {
	e, b := newLoggedIn(t)
	e.addHMI("x", "Alt", "")
	breakSave(e)
	r := b.post("/admin/hmis/x", hmiForm("Neu", "10.0.0.5", "5900", ""))
	if r.Code == http.StatusSeeOther {
		t.Fatal("Erfolg gemeldet")
	}
	wantContains(t, r.Body.String(), "bisherigen Einstellungen", `value="Neu"`)
	if e.appliedCount() != 0 {
		t.Error("Apply gerufen")
	}
	if e.hmi("x").Name != "Alt" {
		t.Error("alte Einstellung verloren")
	}
}

func TestDeleteHMI(t *testing.T) {
	e, b := newLoggedIn(t)
	e.addHMI("x", "Presse", "")
	e.addHMI("y", "Tank", "")
	e.mutate(func(c *config.Config) { c.Kiosk.PermanentHMI = "y" })
	r := b.post("/admin/hmis/y/delete", nil)
	if r.Code == http.StatusSeeOther {
		t.Fatal("Dauer-HMI gelöscht")
	}
	wantContains(t, r.Body.String(), "Daueranzeige")
	if len(e.cfg().HMIs) != 2 {
		t.Fatal("HMI fehlt")
	}
	if r := b.post("/admin/hmis/x/delete", nil); r.Code != http.StatusSeeOther {
		t.Fatalf("Status %d", r.Code)
	}
	if hs := e.cfg().HMIs; len(hs) != 1 || hs[0].ID != "y" {
		t.Errorf("HMIs: %+v", hs)
	}
	if r := b.post("/admin/hmis/nix/delete", nil); r.Code != http.StatusNotFound {
		t.Errorf("unbekannt: %d", r.Code)
	}
}

func TestHMIListSortedByOrder(t *testing.T) {
	e, b := newLoggedIn(t)
	e.mutate(func(c *config.Config) {
		c.HMIs = []config.HMI{
			{ID: "a", Name: "Zweite", Host: "h", Port: 1, Order: 2},
			{ID: "b", Name: "Erste", Host: "h", Port: 1, Order: 1},
		}
	})
	body := b.get("/admin/hmis").Body.String()
	if strings.Index(body, "Erste") > strings.Index(body, "Zweite") {
		t.Error("Reihenfolge")
	}
}

func TestProbeShowsResultWithin10Seconds(t *testing.T) {
	e, b := newLoggedIn(t)
	e.addHMI("x", "Presse", "geheim")
	var gotHMI config.HMI
	var left time.Duration
	e.probe = func(ctx context.Context, h config.HMI) rfb.ProbeResult {
		gotHMI = h
		dl, ok := ctx.Deadline()
		if !ok {
			t.Error("keine Frist")
		}
		left = time.Until(dl)
		return rfb.ProbeResult{Reachable: true, AuthOK: true, Width: 1280, Height: 800, Name: "<b>Bedienfeld</b>"}
	}
	r := b.post("/admin/hmis/x/probe", nil)
	if r.Code != http.StatusOK {
		t.Fatalf("Status %d", r.Code)
	}
	if left > 10*time.Second || left < 9*time.Second {
		t.Errorf("Frist %v", left)
	}
	if gotHMI.PasswordEnc == "" {
		t.Error("Prüfung ohne gespeicherte Daten")
	}
	body := r.Body.String()
	wantContains(t, body, "1280×800", "&lt;b&gt;Bedienfeld&lt;/b&gt;", "Erreichbar", "Anmeldung")
	wantNotContains(t, body, "<b>Bedienfeld</b>")
	e.probe = func(context.Context, config.HMI) rfb.ProbeResult {
		return rfb.ProbeResult{Err: "Keine Verbindung: Zeitüberschreitung"}
	}
	wantContains(t, b.post("/admin/hmis/x/probe", nil).Body.String(), "Keine Verbindung: Zeitüberschreitung", "nicht erreichbar")
	if r := b.post("/admin/hmis/nix/probe", nil); r.Code != http.StatusNotFound {
		t.Errorf("unbekannt: %d", r.Code)
	}
}

func TestClientKioskSettings(t *testing.T) {
	e, b := newLoggedIn(t)
	e.addHMI("x", "Presse", "")
	r := b.post("/admin/client", url.Values{"profile": {"tigervnc"}, "quality": {"9"}, "color_depth": {"16"}})
	if r.Code != http.StatusSeeOther {
		t.Fatalf("Client: %d %s", r.Code, r.Body.String())
	}
	if c := e.cfg().Client; c.Profile != "tigervnc" || c.Quality != 9 || c.ColorDepth != 16 || c.Scale {
		t.Errorf("Client: %+v", c)
	}
	for _, f := range []url.Values{
		{"profile": {"rdp"}, "quality": {"2"}, "color_depth": {"32"}},
		{"profile": {"remmina"}, "quality": {"5"}, "color_depth": {"32"}},
		{"profile": {"remmina"}, "quality": {"2"}, "color_depth": {"12"}},
		{"profile": {"remmina"}, "quality": {"2"}, "color_depth": {"24"}},
	} {
		if r := b.post("/admin/client", f); r.Code != http.StatusUnprocessableEntity {
			t.Errorf("%v: %d", f, r.Code)
		}
	}
	r = b.post("/admin/kiosk", url.Values{"inactivity": {"30"}, "layout": {"3"}, "permanent": {"x"}, "hide_bar": {"on"}})
	if r.Code != http.StatusSeeOther {
		t.Fatalf("Kiosk: %d %s", r.Code, r.Body.String())
	}
	if k := e.cfg().Kiosk; k.InactivityMinutes != 30 || k.Layout != "3" || k.PermanentHMI != "x" || !k.HideBar {
		t.Errorf("Kiosk: %+v", k)
	}
	for _, f := range []url.Values{
		{"inactivity": {"-1"}, "layout": {"3"}},
		{"inactivity": {"5"}, "layout": {"7"}},
		{"inactivity": {"5"}, "layout": {"auto"}, "permanent": {"unbekannt"}},
	} {
		if r := b.post("/admin/kiosk", f); r.Code != http.StatusUnprocessableEntity {
			t.Errorf("%v: %d", f, r.Code)
		}
	}
}

func TestTerminalSettings(t *testing.T) {
	e, b := newLoggedIn(t)
	id := e.cfg().Terminal.ID
	r := b.post("/admin/terminals", url.Values{
		"name": {"Halle 2"}, "role": {"primary"}, "mode": {"lock"},
		"peer_name": {"Halle 3", "", "Halle 4"}, "peer_address": {"10.0.0.3", "", "10.0.0.4:8443"},
	})
	if r.Code != http.StatusSeeOther {
		t.Fatalf("Status %d: %s", r.Code, r.Body.String())
	}
	c := e.cfg()
	if c.Terminal.Name != "Halle 2" || c.Terminal.Role != config.RolePrimary || c.DefaultMode != config.ModeLock || c.Terminal.ID != id {
		t.Errorf("Terminal: %+v", c.Terminal)
	}
	if len(c.Peers) != 2 || c.Peers[1].Address != "10.0.0.4:8443" {
		t.Errorf("Peers: %+v", c.Peers)
	}
	r = b.post("/admin/terminals", url.Values{
		"name": {"Halle 2"}, "role": {"normal"}, "mode": {"lock"},
		"peer_name": {"Halle 2"}, "peer_address": {"10.0.0.3"},
	})
	if r.Code != http.StatusUnprocessableEntity {
		t.Errorf("eigener Name als Peer: %d", r.Code)
	}
	wantContains(t, r.Body.String(), "eigenen Terminalnamen")
	if e.cfg().Terminal.Role != config.RolePrimary {
		t.Error("trotz Fehler gespeichert")
	}
}

func TestSharedKeyGeneratedShownOnce(t *testing.T) {
	e, b := newLoggedIn(t)
	r := b.post("/admin/terminals/generate-key", nil)
	if r.Code != http.StatusOK {
		t.Fatalf("Status %d", r.Code)
	}
	c := e.cfg()
	key, err := e.box.Open(c.SharedKeyEnc)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(key) {
		t.Errorf("Schlüssel %q", key)
	}
	wantContains(t, r.Body.String(), key)
	if e.appliedCount() != 1 {
		t.Errorf("Apply %d", e.appliedCount())
	}
	wantNotContains(t, b.get("/admin/terminals").Body.String(), key, c.SharedKeyEnc)
	raw, _ := os.ReadFile(filepath.Join(e.dir, "config.json"))
	wantNotContains(t, string(raw), key)
}

func TestSharedKeyGenerationNotShownWhenSaveFails(t *testing.T) {
	e, b := newLoggedIn(t)
	breakSave(e)
	r := b.post("/admin/terminals/generate-key", nil)
	wantContains(t, r.Body.String(), "bisherigen Einstellungen")
	wantNotContains(t, r.Body.String(), `class="key"`)
}

func TestSharedKeyByHand(t *testing.T) {
	e, b := newLoggedIn(t)
	if r := b.post("/admin/terminals/key", url.Values{"shared_key": {"  von-anderem-terminal-kopiert  "}}); r.Code != http.StatusSeeOther {
		t.Fatalf("Status %d", r.Code)
	}
	if k, err := e.box.Open(e.cfg().SharedKeyEnc); err != nil || k != "von-anderem-terminal-kopiert" {
		t.Errorf("Schlüssel %q %v", k, err)
	}
	if r := b.post("/admin/terminals/key", url.Values{"shared_key": {"kurz"}}); r.Code != http.StatusUnprocessableEntity {
		t.Errorf("zu kurz: %d", r.Code)
	}
}
