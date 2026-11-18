package pdhterminal

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"hmikiosk/internal/config"
)

func TestStatusDisplaySaveAndTest(t *testing.T) {
	e := newAdminEnv(t, newFakePDH(t).client())
	if r := e.get("/admin/statusdisplay"); r.Code != http.StatusOK || !strings.Contains(r.Body.String(), "Statusdisplay") {
		t.Fatalf("GET: %d", r.Code)
	}
	r := e.post("/admin/statusdisplay", url.Values{"type": {"sh1106"}, "i2cBus": {"/dev/i2c-1"}, "i2cAddr": {"0x3C"}})
	if r.Code != http.StatusOK {
		t.Fatalf("Speichern: %d\n%s", r.Code, r.Body.String())
	}
	c, _, _ := e.store.Load()
	if want := (config.StatusDisplayCfg{Type: "sh1106", I2CBus: "/dev/i2c-1", I2CAddr: 0x3C}); c.StatusDisplay != want {
		t.Fatalf("Konfiguration %+v, erwartet %+v", c.StatusDisplay, want)
	}
	for name, v := range map[string]url.Values{
		"Typ":      {"type": {"ssd1306"}},
		"Adresse":  {"type": {"sh1106"}, "i2cAddr": {"0x80"}},
		"Bus":      {"type": {"sh1106"}, "i2cBus": {"/dev/null"}},
		"unlesbar": {"type": {"sh1106"}, "i2cAddr": {"zz"}},
	} {
		if r := e.post("/admin/statusdisplay", v); r.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, erwartet 400", name, r.Code)
		}
	}

	var gotBus string
	var gotAddr int
	e.product.probe = func(bus string, addr int) error { gotBus, gotAddr = bus, addr; return nil }
	r = e.post("/admin/statusdisplay/test", url.Values{})
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), "Statusdisplay antwortet (/dev/i2c-1, 0x3c)") {
		t.Fatalf("Test ok: %d\n%s", r.Code, r.Body.String())
	}
	if gotBus != "/dev/i2c-1" || gotAddr != 0x3C {
		t.Fatalf("Probe mit %q %#x", gotBus, gotAddr)
	}
	e.product.probe = func(string, int) error { return errors.New("kein Gerät") }
	r = e.post("/admin/statusdisplay/test", url.Values{})
	if !strings.Contains(r.Body.String(), "kein Gerät") {
		t.Fatalf("Fehler fehlt: %d\n%s", r.Code, r.Body.String())
	}
}

func TestStatusDisplayTestOffAndUnknown(t *testing.T) {
	e := newAdminEnv(t, newFakePDH(t).client())
	probed := false
	e.product.probe = func(string, int) error { probed = true; return nil }
	r := e.post("/admin/statusdisplay/test", url.Values{})
	if !strings.Contains(r.Body.String(), "Statusdisplay aus") || probed {
		t.Fatalf("aus: probe=%v\n%s", probed, r.Body.String())
	}
	c, _, _ := e.store.Load()
	c.StatusDisplay = config.StatusDisplayCfg{Type: "ssd1306"}
	if err := e.store.Save(c); err != nil {
		t.Fatal(err)
	}
	_, reason := c.StatusDisplay.Known()
	r = e.post("/admin/statusdisplay/test", url.Values{})
	if !strings.Contains(r.Body.String(), reason) || probed {
		t.Fatalf("unbekannt: probe=%v, Grund %q fehlt\n%s", probed, reason, r.Body.String())
	}
}
