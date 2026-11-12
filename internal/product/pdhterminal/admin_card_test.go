package pdhterminal

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"hmikiosk/internal/config"
)

func TestAdminCardSave(t *testing.T) {
	e := newAdminEnv(t, newFakePDH(t).client())
	r := e.post("/admin/card/settings", url.Values{"type": {"pn532-i2c"}, "i2cBus": {"/dev/i2c-1"}, "i2cAddr": {"0x24"}, "format": {"hex-rev"}})
	if r.Code != http.StatusOK {
		t.Fatalf("Speichern: %d\n%s", r.Code, r.Body.String())
	}
	c, _, err := e.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	want := config.CardCfg{Type: "pn532-i2c", I2CBus: "/dev/i2c-1", I2CAddr: 36, Format: "hex-rev"}
	if c.Card != want {
		t.Fatalf("Konfiguration %+v, erwartet %+v", c.Card, want)
	}
}

func TestAdminCardInvalid(t *testing.T) {
	e := newAdminEnv(t, newFakePDH(t).client())
	for name, v := range map[string]url.Values{
		"Adresse unlesbar": {"type": {"pn532-i2c"}, "i2cAddr": {"zz"}},
		"Adresse zu hoch":  {"type": {"pn532-i2c"}, "i2cAddr": {"0x78"}},
		"Bus":              {"type": {"pn532-i2c"}, "i2cBus": {"/dev/i2c-1;rm"}},
		"usb ohne Gerät":   {"type": {"usb"}},
		"Format":           {"type": {"pn532-i2c"}, "format": {"bin"}},
	} {
		r := e.post("/admin/card/settings", v)
		if r.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, erwartet 400", name, r.Code)
		}
	}
	c, _, _ := e.store.Load()
	if c.Card != (config.CardCfg{}) {
		t.Fatalf("Konfiguration verändert: %+v", c.Card)
	}
}

func TestAdminCardShowsLast(t *testing.T) {
	e := newAdminEnv(t, newFakePDH(t).client())
	if body := e.get("/admin/card").Body.String(); !strings.Contains(body, "Noch keine Karte gelesen") {
		t.Fatalf("Leerzustand fehlt:\n%s", body)
	}
	e.cards.Hub().Publish("DEADBEEF")
	r := e.get("/admin/card")
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), "DEADBEEF") || !strings.Contains(r.Body.String(), "Zuletzt gelesen") {
		t.Fatalf("UID fehlt:\n%s", r.Body.String())
	}
}
