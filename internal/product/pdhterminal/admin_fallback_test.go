package pdhterminal

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"hmikiosk/internal/config"
)

func loadPDH(t *testing.T, e *adminEnv) config.PDHCfg {
	t.Helper()
	c, _, err := e.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	return c.PDH
}

func TestAdminFallbackSaved(t *testing.T) {
	e := newAdminEnv(t, Client{})
	r := e.post("/admin/pdh/settings", url.Values{"url": {"https://pdh.firma.de"}, "fallbackUrl": {" https://pdh.t.ts.net "}})
	if r.Code != http.StatusOK {
		t.Fatalf("%d %s", r.Code, r.Body.String())
	}
	if got := loadPDH(t, e).FallbackURL; got != "https://pdh.t.ts.net" {
		t.Fatalf("Ausweichadresse: %q", got)
	}
	if !strings.Contains(r.Body.String(), `value="https://pdh.t.ts.net"`) {
		t.Errorf("Feld nicht befüllt: %s", r.Body.String())
	}
}

func TestAdminFallbackInvalid(t *testing.T) {
	e := newAdminEnv(t, Client{})
	r := e.post("/admin/pdh/settings", url.Values{"url": {"https://pdh.firma.de"}, "fallbackUrl": {"http://pdh.t.ts.net"}})
	if r.Code != http.StatusBadRequest {
		t.Fatalf("Status %d", r.Code)
	}
	b := r.Body.String()
	if !strings.Contains(b, "Ausweichadresse ungültig") || !strings.Contains(b, `value="http://pdh.t.ts.net"`) {
		t.Errorf("Meldung/Formular: %s", b)
	}
	if loadPDH(t, e).FallbackURL != "" {
		t.Error("ungültige Adresse gespeichert")
	}
}

// Speichern ohne Änderung (alle Felder wie angezeigt) behält die Ausweichadresse und die übrigen Felder.
func TestAdminSaveKeepsFallbackAndOtherFields(t *testing.T) {
	e := newAdminEnv(t, Client{})
	want := config.PDHCfg{URL: "https://pdh.firma.de", FallbackURL: "https://pdh.t.ts.net", DeviceName: "Kasse", ExtraOrigins: []string{"https://cdn.firma.de:443"}}
	e.setPDH(want)
	r := e.post("/admin/pdh/settings", url.Values{
		"url": {want.URL}, "fallbackUrl": {want.FallbackURL}, "deviceName": {"Kasse"}, "extraOrigins": {"https://cdn.firma.de:443"},
	})
	if r.Code != http.StatusOK {
		t.Fatalf("%d %s", r.Code, r.Body.String())
	}
	got := loadPDH(t, e)
	if got.FallbackURL != want.FallbackURL || got.DeviceName != "Kasse" || len(got.ExtraOrigins) != 1 || got.URL != want.URL {
		t.Fatalf("verändert: %+v", got)
	}
	// Das Formular zeigt die gespeicherte Ausweichadresse.
	if !strings.Contains(e.get("/admin/pdh").Body.String(), `value="https://pdh.t.ts.net"`) {
		t.Error("Formular zeigt die Ausweichadresse nicht")
	}
}

// Fehlt das Feld ganz (altes Formular), bleibt die gespeicherte Ausweichadresse; ein leeres Feld löscht sie.
func TestAdminFallbackAbsentKeptEmptyClears(t *testing.T) {
	e := newAdminEnv(t, Client{})
	e.setPDH(config.PDHCfg{URL: "https://pdh.firma.de", FallbackURL: "https://pdh.t.ts.net"})
	e.post("/admin/pdh/settings", url.Values{"url": {"https://pdh.firma.de"}})
	if got := loadPDH(t, e).FallbackURL; got != "https://pdh.t.ts.net" {
		t.Fatalf("ohne Feld verloren: %q", got)
	}
	r := e.post("/admin/pdh/settings", url.Values{"url": {"https://pdh.firma.de"}, "fallbackUrl": {""}})
	if r.Code != http.StatusOK {
		t.Fatalf("%d %s", r.Code, r.Body.String())
	}
	if got := loadPDH(t, e).FallbackURL; got != "" {
		t.Fatalf("nicht gelöscht: %q", got)
	}
}

// Ändert sich nur die Ausweichadresse, bleibt die Kopplung; ändert sich die Hauptadresse, geht sie.
func TestAdminFallbackChangeKeepsPairing(t *testing.T) {
	e := newAdminEnv(t, Client{})
	e.setPDH(config.PDHCfg{URL: "https://pdh.firma.de", FallbackURL: "https://pdh.t.ts.net"})
	if err := SaveDevice(e.dir, Device{DeviceID: "1", Secret: "geheim", URL: "https://pdh.firma.de"}); err != nil {
		t.Fatal(err)
	}
	r := e.post("/admin/pdh/settings", url.Values{"url": {"https://pdh.firma.de"}, "fallbackUrl": {"https://pdh2.t.ts.net"}})
	if r.Code != http.StatusOK || !e.deviceFileExists() {
		t.Fatalf("Kopplung weg (%d): %s", r.Code, r.Body.String())
	}
	e.post("/admin/pdh/settings", url.Values{"url": {"https://pdh.neu.de"}, "fallbackUrl": {"https://pdh2.t.ts.net"}})
	if e.deviceFileExists() {
		t.Error("Kopplung bei geänderter Hauptadresse nicht entfernt")
	}
}

// Nach dem Speichern bekommt die Wegwahl die neuen Adressen (über Apply).
func TestAdminSavePassesURLsToRoute(t *testing.T) {
	rt := NewRoute("", "")
	e := newAdminEnvApply(t, Client{Route: rt}, func(*adminProduct) {}, func(c config.Config) { rt.SetURLs(c.PDH.URL, c.PDH.FallbackURL) })
	e.post("/admin/pdh/settings", url.Values{"url": {"https://pdh.firma.de"}, "fallbackUrl": {"https://pdh.t.ts.net"}})
	if !rt.Fail("https://pdh.firma.de", errNet) || rt.Base() != "https://pdh.t.ts.net" {
		t.Fatalf("Route kennt die Adressen nicht: %q", rt.Base())
	}
}

func TestAdminShowsRoute(t *testing.T) {
	rt := NewRoute("https://pdh.firma.de", "https://pdh.t.ts.net")
	at := time.Date(2026, 11, 24, 9, 5, 0, 0, time.Local)
	rt.now = func() time.Time { return at }
	e := newAdminEnv(t, Client{Route: rt})
	e.setPDH(config.PDHCfg{URL: "https://pdh.firma.de", FallbackURL: "https://pdh.t.ts.net"})
	b := e.get("/admin/pdh").Body.String()
	if !strings.Contains(b, `msg ok">verbunden über: LAN`) {
		t.Errorf("LAN: %s", b)
	}
	rt.Fail("https://pdh.firma.de", &netErrText{"dial <b>x</b>"})
	b = e.get("/admin/pdh").Body.String()
	if !strings.Contains(b, `msg warn" title="`) || !strings.Contains(b, "verbunden über: Tailnet seit 09:05") {
		t.Errorf("Tailnet: %s", b)
	}
	if strings.Contains(b, "<b>x</b>") || !strings.Contains(b, "&lt;b&gt;x&lt;/b&gt;") {
		t.Errorf("Fehlertext nicht maskiert: %s", b)
	}
}

type netErrText struct{ s string }

func (e *netErrText) Error() string   { return e.s }
func (e *netErrText) Timeout() bool   { return true }
func (e *netErrText) Temporary() bool { return true }
