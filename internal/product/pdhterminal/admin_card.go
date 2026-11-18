package pdhterminal

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"hmikiosk/internal/config"
	"hmikiosk/internal/product/pdhterminal/card"
	"hmikiosk/internal/product/pdhterminal/oled"
	"hmikiosk/internal/web"
)

// inputDir ist der Ordner der Eingabegeräte, aus dem die USB-Leser zur Auswahl stammen.
const inputDir = "/dev/input"

// cardSettings übersetzt die Konfiguration in die Einstellungen des Kartendienstes.
func cardSettings(c config.CardCfg) card.Settings {
	bus, addr := c.Settings()
	_, reason := c.Known()
	return card.Settings{
		Type: c.Type, I2CBus: bus, I2CAddr: addr, USBDevice: c.USBDevice, Format: c.Format,
		Bus: c.PN532Bus, SPIDev: c.SPIDevice(), UARTDev: c.UARTDevice(), Unsupported: reason,
	}
}

// cardView ist, was die Seite Kartenleser anzeigt.
type cardView struct {
	Type, Bus, Addr, USB, Format string
	SPIDev, UARTDev              string
	Unsupported                  string // Grund, wenn die gespeicherten Werte diese Version überfordern
	USBFree                      string // Vorbelegung des Freitextfelds: leer, wenn die Auswahl das Gerät schon zeigt
	Devices                      []card.InputDevice
	Active                       bool
	Detail                       string
	Last                         string // fertiger Satz zur letzten Karte
	Notice                       string
}

func (p *adminProduct) renderCard(f web.AdminFrame, w http.ResponseWriter, status int, errMsg, notice string, form *config.CardCfg) {
	cfg, err := f.Load()
	if err != nil {
		f.Render(w, http.StatusInternalServerError, "card", "card", "Kartenleser", err.Error(), cardView{})
		return
	}
	shown := cfg.Card
	if form != nil {
		shown = *form
	}
	bus, addr := shown.Settings()
	st := p.cards.Status()
	_, reason := shown.Known()
	v := cardView{
		Type: formType(shown), Bus: bus, Addr: fmt.Sprintf("%#x", addr), USB: shown.USBDevice, Format: shown.Format,
		SPIDev: shown.SPIDevice(), UARTDev: shown.UARTDevice(), Unsupported: reason,
		Devices: card.ListInputDevices(inputDir), Active: st.Active, Detail: st.Detail, Notice: notice,
		Last: "Noch keine Karte gelesen",
	}
	v.USBFree = shown.USBDevice
	for _, d := range v.Devices {
		if d.ID == shown.USBDevice {
			v.USBFree = "" // die Auswahl ist vorbelegt
		}
	}
	if e, ok := p.cards.Hub().Last(); ok {
		n := int(time.Since(e.At).Seconds())
		if n < 0 {
			n = 0
		}
		v.Last = fmt.Sprintf("Zuletzt gelesen: %s (vor %d s)", e.UID, n)
	}
	f.Render(w, status, "card", "card", "Kartenleser", errMsg, v)
}

func (p *adminProduct) cardSave(f web.AdminFrame, w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		p.renderCard(f, w, http.StatusBadRequest, "Formular unlesbar", "", nil)
		return
	}
	in := config.CardCfg{
		Type:      strings.TrimSpace(r.PostFormValue("type")),
		Format:    strings.TrimSpace(r.PostFormValue("format")),
		USBDevice: strings.TrimSpace(r.PostFormValue("usbDevice")),
	}
	// Das Formular kennt pn532-spi und pn532-uart; gespeichert wird immer pn532-i2c mit PN532Bus.
	// I²C-Bus und -Adresse gelten nur für pn532-i2c; bei den anderen Arten bleiben sie leer, damit ein
	// (ausgeblendetes) ungültiges Feld das Speichern nicht blockiert.
	switch in.Type {
	case "pn532-i2c":
		in.I2CBus = strings.TrimSpace(r.PostFormValue("i2cBus"))
		if s := strings.TrimSpace(r.PostFormValue("i2cAddr")); s != "" {
			n, err := strconv.ParseInt(s, 0, 0)
			if err != nil {
				p.renderCard(f, w, http.StatusBadRequest, fmt.Sprintf("Adresse ungültig: %q (erwartet z. B. 0x24)", s), "", &in)
				return
			}
			in.I2CAddr = int(n)
		}
	case "pn532-spi":
		in.Type, in.PN532Bus = config.CardTypePN532I2C, config.CardBusSPI
		in.SPIDev = strings.TrimSpace(r.PostFormValue("spiDev"))
	case "pn532-uart":
		in.Type, in.PN532Bus = config.CardTypePN532I2C, config.CardBusUART
		in.UARTDev = strings.TrimSpace(r.PostFormValue("uartDev"))
	}
	// Freitext gilt nur, wenn er geändert wurde (usbPrev ist der angezeigte Stand); sonst gewinnt die Auswahl.
	if sel := strings.TrimSpace(r.PostFormValue("usbSelect")); sel != "" &&
		(in.USBDevice == "" || in.USBDevice == strings.TrimSpace(r.PostFormValue("usbPrev"))) {
		in.USBDevice = sel
	}
	if err := config.ValidateCardStrict(in); err != nil {
		p.renderCard(f, w, http.StatusBadRequest, err.Error(), "", &in)
		return
	}
	if err := f.Update(func(c *config.Config) error { c.Card = in; return nil }); err != nil {
		p.renderCard(f, w, http.StatusBadRequest, err.Error(), "", &in)
		return
	}
	p.renderCard(f, w, http.StatusOK, "", "Einstellungen gespeichert.", nil)
}

// statusDisplaySettings übersetzt die Konfiguration in die Einstellungen des Displaydienstes.
func statusDisplaySettings(c config.StatusDisplayCfg) oled.Settings {
	bus, addr := c.Settings()
	_, reason := c.Known()
	return oled.Settings{Type: c.Type, Bus: bus, Addr: addr, Unsupported: reason}
}

// formType ist der Wert der Auswahl „Leser“: pn532-spi und pn532-uart gibt es nur im Formular.
// Ein Wert, den diese Version nicht kennt, zeigt „Aus“, ohne die Konfiguration zu ändern.
func formType(c config.CardCfg) string {
	if ok, _ := c.Known(); !ok {
		return ""
	}
	if c.Type == config.CardTypePN532I2C {
		switch c.PN532Bus {
		case config.CardBusSPI:
			return "pn532-spi"
		case config.CardBusUART:
			return "pn532-uart"
		}
	}
	return c.Type
}
