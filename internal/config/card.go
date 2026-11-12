package config

import (
	"fmt"
	"regexp"
	"unicode"
	"unicode/utf8"
)

// CardCfg ist der Kartenleser (NFC/RFID) des PDH-Terminals. Der Abschnitt fehlt in der
// Datei, solange nichts eingestellt ist (omitzero). Das Paket kennt den Kartendienst
// nicht; die Zeichenketten hier entsprechen dessen Lesertypen.
type CardCfg struct {
	// Type ist "" (kein Leser), "pn532-i2c" oder "usb".
	Type string `json:"type,omitempty"`
	// I2CBus ist das Gerät des PN532, z. B. /dev/i2c-1 (leer: Standard).
	I2CBus string `json:"i2cBus,omitempty"`
	// I2CAddr ist die 7-Bit-Adresse des PN532 (0: Standard 0x24).
	I2CAddr int `json:"i2cAddr,omitempty"`
	// USBDevice wählt den USB-Leser: vvvv:pppp oder ein Teil des Namens.
	USBDevice string `json:"usbDevice,omitempty"`
	// Format bestimmt die Schreibweise der UID: hex, hex-rev oder dec (leer: hex).
	Format string `json:"format,omitempty"`
}

// Erlaubte Lesertypen der Konfiguration.
const (
	CardTypePN532I2C = "pn532-i2c"
	CardTypeUSB      = "usb"
)

const (
	defaultCardBus  = "/dev/i2c-1"
	defaultCardAddr = 0x24
	maxCardDevice   = 64
)

var cardBusRe = regexp.MustCompile(`^/dev/i2c-[0-9]{1,2}$`)

// ValidateCard prüft den Kartenleser; ein leerer Abschnitt ist gültig.
func ValidateCard(c CardCfg) error {
	switch c.Type {
	case "", CardTypePN532I2C, CardTypeUSB:
	default:
		return fmt.Errorf("Kartenleser: Typ ungültig: %q", c.Type)
	}
	if c.I2CBus != "" && !cardBusRe.MatchString(c.I2CBus) {
		return fmt.Errorf("Kartenleser: I²C-Bus ungültig: %q (erwartet z. B. /dev/i2c-1)", c.I2CBus)
	}
	if c.I2CAddr != 0 && (c.I2CAddr < 0x08 || c.I2CAddr > 0x77) {
		return fmt.Errorf("Kartenleser: I²C-Adresse %#x ungültig (erlaubt 0x08 bis 0x77)", c.I2CAddr)
	}
	if c.Type == CardTypeUSB && c.USBDevice == "" {
		return fmt.Errorf("Kartenleser: Für den USB-Leser fehlt das Gerät")
	}
	if utf8.RuneCountInString(c.USBDevice) > maxCardDevice {
		return fmt.Errorf("Kartenleser: Gerät ist länger als %d Zeichen", maxCardDevice)
	}
	for _, r := range c.USBDevice {
		if unicode.IsControl(r) {
			return fmt.Errorf("Kartenleser: Gerät enthält Steuerzeichen")
		}
	}
	// Der Kartendienst fiele bei einem unbekannten Format still auf hex zurück.
	switch c.Format {
	case "", "hex", "hex-rev", "dec":
	default:
		return fmt.Errorf("Kartenleser: Format ungültig: %q (erlaubt hex, hex-rev, dec)", c.Format)
	}
	return nil
}

// Settings liefert Bus und Adresse des PN532 mit den Standardwerten /dev/i2c-1 und 0x24.
func (c CardCfg) Settings() (bus string, addr int) {
	bus, addr = c.I2CBus, c.I2CAddr
	if bus == "" {
		bus = defaultCardBus
	}
	if addr == 0 {
		addr = defaultCardAddr
	}
	return bus, addr
}
