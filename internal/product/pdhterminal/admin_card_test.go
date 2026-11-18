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

func TestAdminCardUSBSelectVsFree(t *testing.T) {
	e := newAdminEnv(t, newFakePDH(t).client())
	save := func(v url.Values) config.CardCfg {
		t.Helper()
		v.Set("type", "usb")
		if r := e.post("/admin/card/settings", v); r.Code != http.StatusOK {
			t.Fatalf("Speichern: %d\n%s", r.Code, r.Body.String())
		}
		c, _, _ := e.store.Load()
		return c.Card
	}
	// Freitext allein
	if c := save(url.Values{"usbDevice": {"sycreader"}}); c.USBDevice != "sycreader" {
		t.Fatalf("Freitext: %q", c.USBDevice)
	}
	// Das Freitextfeld ist mit dem alten Wert vorbelegt (usbPrev), die Auswahl wurde geändert: Auswahl gewinnt.
	if c := save(url.Values{"usbDevice": {"sycreader"}, "usbPrev": {"sycreader"}, "usbSelect": {"046d:c31c"}}); c.USBDevice != "046d:c31c" {
		t.Fatalf("Auswahl ignoriert: %q", c.USBDevice)
	}
	// Freitext von Hand geändert: Freitext gewinnt.
	if c := save(url.Values{"usbDevice": {"neu"}, "usbPrev": {"sycreader"}, "usbSelect": {"046d:c31c"}}); c.USBDevice != "neu" {
		t.Fatalf("Freitext verloren: %q", c.USBDevice)
	}
	// Freitext leer: Auswahl.
	if c := save(url.Values{"usbSelect": {"1234:abcd"}}); c.USBDevice != "1234:abcd" {
		t.Fatalf("Auswahl: %q", c.USBDevice)
	}
}

func TestAdminCardUSBFreeFieldPrefill(t *testing.T) {
	e := newAdminEnv(t, newFakePDH(t).client())
	e.post("/admin/card/settings", url.Values{"type": {"usb"}, "usbDevice": {"sycreader"}})
	if body := e.get("/admin/card").Body.String(); !strings.Contains(body, `name="usbDevice" value="sycreader"`) {
		t.Fatalf("Freitext nicht vorbelegt:\n%s", body)
	}
}

func TestCardSettingsMapping(t *testing.T) {
	got := cardSettings(config.CardCfg{Type: "pn532-i2c", PN532Bus: "spi"})
	if got.Bus != "spi" || got.SPIDev != "/dev/spidev0.0" || got.Unsupported != "" {
		t.Errorf("SPI: %+v", got)
	}
	got = cardSettings(config.CardCfg{Type: "pn532-i2c", PN532Bus: "uart"})
	if got.Bus != "uart" || got.UARTDev != "/dev/ttyAMA0" {
		t.Errorf("UART: %+v", got)
	}
	got = cardSettings(config.CardCfg{Type: "pn7150"})
	if got.Unsupported == "" {
		t.Errorf("pn7150 muss Unsupported setzen: %+v", got)
	}
}

func TestCardSaveSPI(t *testing.T) {
	e := newAdminEnv(t, newFakePDH(t).client())
	r := e.post("/admin/card/settings", url.Values{"type": {"pn532-spi"}, "spiDev": {"/dev/spidev0.1"}, "uartDev": {"/dev/ttyS0"}})
	if r.Code != http.StatusOK {
		t.Fatalf("Speichern: %d\n%s", r.Code, r.Body.String())
	}
	c, _, _ := e.store.Load()
	want := config.CardCfg{Type: "pn532-i2c", PN532Bus: "spi", SPIDev: "/dev/spidev0.1"}
	if c.Card != want {
		t.Fatalf("Konfiguration %+v, erwartet %+v", c.Card, want)
	}
	if body := e.get("/admin/card").Body.String(); !strings.Contains(body, `value="pn532-spi" selected`) {
		t.Fatalf("pn532-spi nicht gewählt:\n%s", body)
	}
	r = e.post("/admin/card/settings", url.Values{"type": {"pn532-uart"}, "uartDev": {"/dev/ttyS0"}})
	if r.Code != http.StatusOK {
		t.Fatalf("UART: %d", r.Code)
	}
	c, _, _ = e.store.Load()
	if want := (config.CardCfg{Type: "pn532-i2c", PN532Bus: "uart", UARTDev: "/dev/ttyS0"}); c.Card != want {
		t.Fatalf("Konfiguration %+v, erwartet %+v", c.Card, want)
	}
}

func TestCardSaveRejectsUnknown(t *testing.T) {
	e := newAdminEnv(t, newFakePDH(t).client())
	r := e.post("/admin/card/settings", url.Values{"type": {"pn7150"}})
	if r.Code != http.StatusBadRequest {
		t.Fatalf("%d, erwartet 400", r.Code)
	}
	c, _, _ := e.store.Load()
	if c.Card != (config.CardCfg{}) {
		t.Fatalf("Konfiguration verändert: %+v", c.Card)
	}
}

func TestCardPageShowsUnsupported(t *testing.T) {
	e := newAdminEnv(t, newFakePDH(t).client())
	c, _, _ := e.store.Load()
	c.Card = config.CardCfg{Type: "pn7150"}
	if err := e.store.Save(c); err != nil {
		t.Fatal(err)
	}
	body := e.get("/admin/card").Body.String()
	_, reason := c.Card.Known()
	if !strings.Contains(body, reason) {
		t.Fatalf("Grund %q fehlt:\n%s", reason, body)
	}
	if !strings.Contains(body, `value="" selected`) {
		t.Fatalf("Auswahl nicht auf Aus:\n%s", body)
	}
}
