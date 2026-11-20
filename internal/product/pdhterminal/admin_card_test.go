package pdhterminal

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"hmikiosk/internal/config"
	"hmikiosk/internal/product/pdhterminal/card"
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
	skipOnWindows(t)
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
	skipOnWindows(t)
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

func TestCardSaveIgnoresI2CFieldsForOtherTypes(t *testing.T) {
	e := newAdminEnv(t, newFakePDH(t).client())
	bad := url.Values{"i2cBus": {"kaputt"}, "i2cAddr": {"zz"}}
	for name, in := range map[string]struct {
		typ   string
		extra url.Values
		want  config.CardCfg
	}{
		"spi":  {"pn532-spi", url.Values{"spiDev": {"/dev/spidev0.0"}}, config.CardCfg{Type: "pn532-i2c", PN532Bus: "spi", SPIDev: "/dev/spidev0.0"}},
		"uart": {"pn532-uart", url.Values{"uartDev": {"/dev/ttyAMA0"}}, config.CardCfg{Type: "pn532-i2c", PN532Bus: "uart", UARTDev: "/dev/ttyAMA0"}},
		"usb":  {"usb", url.Values{"usbDevice": {"1234:5678"}}, config.CardCfg{Type: "usb", USBDevice: "1234:5678"}},
	} {
		v := url.Values{"type": {in.typ}}
		for k, vs := range bad {
			v[k] = vs
		}
		for k, vs := range in.extra {
			v[k] = vs
		}
		if r := e.post("/admin/card/settings", v); r.Code != http.StatusOK {
			t.Fatalf("%s: %d\n%s", name, r.Code, r.Body.String())
		}
		if c, _, _ := e.store.Load(); c.Card != in.want {
			t.Fatalf("%s: Konfiguration %+v, erwartet %+v", name, c.Card, in.want)
		}
	}
	// Bei pn532-i2c bleibt die Prüfung bestehen.
	if r := e.post("/admin/card/settings", url.Values{"type": {"pn532-i2c"}, "i2cAddr": {"zz"}}); r.Code != http.StatusBadRequest {
		t.Fatalf("i2c mit ungültiger Adresse: %d, erwartet 400", r.Code)
	}
}

func TestAdminCardFieldGroups(t *testing.T) {
	skipOnWindows(t)
	e := newAdminEnv(t, newFakePDH(t).client())
	body := e.get("/admin/card").Body.String()
	for _, cls := range []string{"f-i2c", "f-spi", "f-uart", "f-usb", "f-pn532"} {
		if !strings.Contains(body, cls) {
			t.Errorf("Feldgruppe %s fehlt im Formular", cls)
		}
	}
}

// windowsForm schaltet das Formular für die Dauer des Tests auf Windows.
func windowsForm(t *testing.T) {
	t.Helper()
	old := formWindows
	formWindows = true
	t.Cleanup(func() { formWindows = old })
}

func TestCardFormWindowsTypes(t *testing.T) {
	windowsForm(t)
	e := newAdminEnv(t, newFakePDH(t).client())
	body := e.get("/admin/card").Body.String()
	sel := body[strings.Index(body, `<select name="type">`):]
	sel = sel[:strings.Index(sel, "</select>")]
	if n := strings.Count(sel, "<option"); n != 2 {
		t.Fatalf("%d Arten, erwartet 2:\n%s", n, sel)
	}
	for _, want := range []string{"Kein Leser", "USB-Leser (Tastatur)", `value="usb"`} {
		if !strings.Contains(sel, want) {
			t.Errorf("%q fehlt:\n%s", want, sel)
		}
	}
	if strings.Contains(sel, "PN532") || strings.Contains(body, "Schalter am PN532-Modul") {
		t.Errorf("PN532 darf unter Windows nicht erscheinen:\n%s", body)
	}
	if !strings.Contains(body, "Karte vorhalten") {
		t.Error("Anlernknopf fehlt")
	}
}

func TestCardFormLinuxHasNoLearn(t *testing.T) {
	skipOnWindows(t)
	e := newAdminEnv(t, newFakePDH(t).client())
	if body := e.get("/admin/card").Body.String(); strings.Contains(body, "Karte vorhalten") || !strings.Contains(body, "PN532 (SPI)") {
		t.Errorf("Linux zeigt Anlernknopf oder keine PN532:\n%s", body)
	}
	r := e.post("/admin/card/learn", url.Values{})
	if r.Code != http.StatusBadRequest {
		t.Errorf("Anlernen unter Linux: %d, erwartet 400", r.Code)
	}
}

func TestCardLearnFillsForm(t *testing.T) {
	windowsForm(t)
	old := learnFn
	t.Cleanup(func() { learnFn = old })
	learnFn = func(ctx context.Context, d time.Duration) (card.InputDevice, string, error) {
		if d != 15*time.Second {
			t.Errorf("Wartezeit %v, erwartet 15 s", d)
		}
		return card.InputDevice{Name: "SYC Reader", ID: "1a2b:3c4d"}, "DEADBEEF", nil
	}
	e := newAdminEnv(t, newFakePDH(t).client())
	r := e.post("/admin/card/learn", url.Values{})
	if r.Code != http.StatusOK {
		t.Fatalf("%d\n%s", r.Code, r.Body.String())
	}
	body := r.Body.String()
	for _, want := range []string{
		`value="usb" selected`, `name="usbDevice" value="1a2b:3c4d"`, `value="hex" selected`,
		"DEADBEEF", "EFBEADDE", "4022250974", "SYC Reader",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("%q fehlt:\n%s", want, body)
		}
	}
	// gespeichert ist noch nichts
	if c, _, _ := e.store.Load(); c.Card != (config.CardCfg{}) {
		t.Fatalf("Anlernen darf nicht speichern: %+v", c.Card)
	}
}

func TestCardLearnDecimalSuggestion(t *testing.T) {
	windowsForm(t)
	old := learnFn
	t.Cleanup(func() { learnFn = old })
	learnFn = func(context.Context, time.Duration) (card.InputDevice, string, error) {
		return card.InputDevice{Name: "Leser ohne Kennung"}, "0123456789", nil
	}
	e := newAdminEnv(t, newFakePDH(t).client())
	body := e.post("/admin/card/learn", url.Values{}).Body.String()
	if !strings.Contains(body, `value="dec" selected`) || !strings.Contains(body, `name="usbDevice" value="Leser ohne Kennung"`) {
		t.Errorf("Vorschlag dec oder Name als Gerät fehlt:\n%s", body)
	}
}

func TestCardLearnTimeout(t *testing.T) {
	windowsForm(t)
	old := learnFn
	t.Cleanup(func() { learnFn = old })
	learnFn = func(context.Context, time.Duration) (card.InputDevice, string, error) {
		return card.InputDevice{}, "", errors.New("Keine Karte gelesen")
	}
	e := newAdminEnv(t, newFakePDH(t).client())
	r := e.post("/admin/card/learn", url.Values{})
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), "Keine Karte gelesen") {
		t.Fatalf("%d\n%s", r.Code, r.Body.String())
	}
}

// skipOnWindows überspringt Tests, die das Linux-Formular oder den PN532 voraussetzen.
func skipOnWindows(t *testing.T) {
	t.Helper()
	if onWindows {
		t.Skip("setzt das Linux-Formular voraus")
	}
}

func TestCardSettingsUSBFormat(t *testing.T) {
	// Altes Linux-Gerät: usb mit übrig gebliebenem format. Der USB-Leser bekommt kein Format.
	got := cardSettings(config.CardCfg{Type: "usb", USBDevice: "ffff:0035", Format: "dec"})
	if got.USBFormat != "" || got.Format != "dec" {
		t.Errorf("Format dec: %+v", got)
	}
	got = cardSettings(config.CardCfg{Type: "usb", USBDevice: "ffff:0035", USBFormat: "dec"})
	if got.USBFormat != "dec" {
		t.Errorf("USBFormat dec: %+v", got)
	}
}

func TestCardSaveKeepsOtherPlatformFormat(t *testing.T) {
	e := newAdminEnv(t, newFakePDH(t).client())
	if err := e.store.Save(func() config.Config {
		c, _, _ := e.store.Load()
		c.Card = config.CardCfg{Type: "usb", USBDevice: "x", Format: "hex-rev", USBFormat: "dec"}
		return c
	}()); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"type": {"usb"}, "usbDevice": {"x"}}
	if formWindows {
		form.Set("usbFormat", "hex")
	} else {
		form.Set("format", "hex")
	}
	if r := e.post("/admin/card/settings", form); r.Code != http.StatusOK {
		t.Fatalf("Speichern: %d\n%s", r.Code, r.Body.String())
	}
	c, _, _ := e.store.Load()
	want := config.CardCfg{Type: "usb", USBDevice: "x", Format: "hex", USBFormat: "dec"}
	if formWindows {
		want = config.CardCfg{Type: "usb", USBDevice: "x", Format: "hex-rev", USBFormat: "hex"}
	}
	if c.Card != want {
		t.Fatalf("Konfiguration %+v, erwartet %+v", c.Card, want)
	}
}
