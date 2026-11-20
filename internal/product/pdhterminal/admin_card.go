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

// formWindows: Unter Windows bietet das Formular nur „kein Leser“ und den USB-Tastaturleser an und
// hat den Knopf „Karte vorhalten“; die Prüfung setzt es, um beides ohne Windows zu zeigen.
var formWindows = onWindows

// learnFn liest die Karte beim Anlernen; die Prüfung ersetzt sie.
var learnFn = card.Learn

// learnWait ist die Wartezeit beim Anlernen.
const learnWait = 15 * time.Second

// cardSettings übersetzt die Konfiguration in die Einstellungen des Kartendienstes.
func cardSettings(c config.CardCfg) card.Settings {
	bus, addr := c.Settings()
	return card.Settings{
		Type: c.Type, I2CBus: bus, I2CAddr: addr, USBDevice: c.USBDevice, Format: c.Format, USBFormat: c.USBFormat,
		Bus: c.PN532Bus, SPIDev: c.SPIDevice(), UARTDev: c.UARTDevice(), Unsupported: cardReason(c),
	}
}

// cardReason nennt, warum der Leser nicht läuft: unbekannter Typ oder Plattform.
func cardReason(c config.CardCfg) string {
	if _, reason := c.Known(); reason != "" {
		return reason
	}
	return cardPlatformReason(c)
}

// cardView ist, was die Seite Kartenleser anzeigt.
type cardView struct {
	Type, Bus, Addr, USB, Format string
	USBFormat                    string // Schreibweise des USB-Lesers (nur Windows-Formular)
	SPIDev, UARTDev              string
	Unsupported                  string // Grund, wenn die gespeicherten Werte diese Version überfordern
	USBFree                      string // Vorbelegung des Freitextfelds: leer, wenn die Auswahl das Gerät schon zeigt
	Devices                      []card.InputDevice
	Active                       bool
	Detail                       string
	Last                         string // fertiger Satz zur letzten Karte
	Notice                       string
	Windows                      bool         // Formular nur mit „kein Leser“ und USB-Leser, mit Anlernknopf
	Learned                      *learnedView // Ergebnis von „Karte vorhalten“
}

// learnedView ist, was das Anlernen ergab: Gerät, getippter Text und die UID in allen Schreibweisen.
type learnedView struct {
	Device, Typed, Format string
	Hex, HexRev, Dec      string
	Forms                 bool // Hex, HexRev und Dec sind belegt
}

func (p *adminProduct) renderCard(f web.AdminFrame, w http.ResponseWriter, status int, errMsg, notice string, form *config.CardCfg) {
	p.renderCardLearned(f, w, status, errMsg, notice, form, nil)
}

func (p *adminProduct) renderCardLearned(f web.AdminFrame, w http.ResponseWriter, status int, errMsg, notice string, form *config.CardCfg, learned *learnedView) {
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
	v := cardView{
		Type: formType(shown), Bus: bus, Addr: fmt.Sprintf("%#x", addr), USB: shown.USBDevice, Format: shown.Format, USBFormat: shown.USBFormat,
		SPIDev: shown.SPIDevice(), UARTDev: shown.UARTDevice(), Unsupported: cardReason(shown),
		Devices: card.ListInputDevices(inputDir), Active: st.Active, Detail: st.Detail, Notice: notice,
		Last: "Noch keine Karte gelesen", Windows: formWindows, Learned: learned,
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
		USBFormat: strings.TrimSpace(r.PostFormValue("usbFormat")),
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
	// Das Formular zeigt je Plattform nur eines der beiden Formate; das andere bleibt, wie es war.
	if err := f.Update(func(c *config.Config) error {
		if formWindows {
			in.Format = c.Card.Format
		} else {
			in.USBFormat = c.Card.USBFormat
		}
		c.Card = in
		return nil
	}); err != nil {
		p.renderCard(f, w, http.StatusBadRequest, err.Error(), "", &in)
		return
	}
	p.renderCard(f, w, http.StatusOK, "", "Einstellungen gespeichert.", nil)
}

// cardLearn wartet auf eine Karte am Leser (Tastatur), trägt Gerät und Formatvorschlag ins Formular
// ein und zeigt die UID in allen Schreibweisen. Gespeichert wird erst mit „Speichern“.
func (p *adminProduct) cardLearn(f web.AdminFrame, w http.ResponseWriter, r *http.Request) {
	if !formWindows {
		p.renderCard(f, w, http.StatusBadRequest, "Karte anlernen gibt es nur unter Windows", "", nil)
		return
	}
	dev, typed, err := learnFn(r.Context(), learnWait)
	if err != nil {
		msg := "Keine Karte gelesen: Bitte die Karte innerhalb von 15 Sekunden an den Leser halten"
		if err.Error() != "Keine Karte gelesen" {
			msg = "Karte anlernen nicht möglich: " + err.Error()
		}
		p.renderCard(f, w, http.StatusOK, msg, "", nil)
		return
	}
	cfg, err := f.Load()
	if err != nil {
		p.renderCard(f, w, http.StatusInternalServerError, err.Error(), "", nil)
		return
	}
	in := cfg.Card
	in.Type, in.USBFormat = config.CardTypeUSB, card.SuggestFormat(typed)
	in.USBDevice = dev.ID
	if in.USBDevice == "" {
		in.USBDevice = dev.Name
	}
	l := &learnedView{Device: dev.Name, Typed: typed, Format: in.USBFormat}
	l.Hex, l.HexRev, l.Dec, l.Forms = card.UIDForms(typed)
	p.renderCardLearned(f, w, http.StatusOK, "", "Karte erkannt. Mit „Speichern“ übernehmen.", &in, l)
}

// statusDisplaySettings übersetzt die Konfiguration in die Einstellungen des Displaydienstes.
func statusDisplaySettings(c config.StatusDisplayCfg) oled.Settings {
	bus, addr := c.Settings()
	return oled.Settings{Type: c.Type, Bus: bus, Addr: addr, Unsupported: displayReason(c)}
}

// displayReason nennt, warum das Display nicht läuft: unbekannter Typ oder Plattform.
func displayReason(c config.StatusDisplayCfg) string {
	if _, reason := c.Known(); reason != "" {
		return reason
	}
	return displayPlatformReason(c)
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
