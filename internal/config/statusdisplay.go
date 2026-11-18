package config

import "fmt"

// StatusDisplayCfg ist das OLED-Statusdisplay des PDH-Terminals. Der Abschnitt fehlt in der
// Datei, solange nichts eingestellt ist (omitzero).
type StatusDisplayCfg struct {
	// Type ist "" (kein Display) oder "sh1106".
	Type string `json:"type,omitempty"`
	// I2CBus ist das Gerät des Displays, z. B. /dev/i2c-1 (leer: Standard).
	I2CBus string `json:"i2cBus,omitempty"`
	// I2CAddr ist die 7-Bit-Adresse des Displays (0: Standard 0x3C).
	I2CAddr int `json:"i2cAddr,omitempty"`
}

// Erlaubte Displaytypen der Konfiguration.
const StatusDisplaySH1106 = "sh1106"

const (
	defaultStatusDisplayBus  = "/dev/i2c-1"
	defaultStatusDisplayAddr = 0x3C
)

// Known meldet, ob diese Version den Displaytyp kennt; sonst steht in reason der Grund.
func (c StatusDisplayCfg) Known() (bool, string) {
	switch c.Type {
	case "", StatusDisplaySH1106:
		return true, ""
	}
	return false, fmt.Sprintf("Displaytyp „%s“ wird von dieser Version nicht unterstützt", c.Type)
}

// ValidateStatusDisplayStrict ist ValidateStatusDisplay und lässt zusätzlich nur bekannte
// Typen zu; die Admin-Seite speichert nur solche.
func ValidateStatusDisplayStrict(c StatusDisplayCfg) error {
	if ok, reason := c.Known(); !ok {
		return fmt.Errorf("Statusdisplay: %s", reason)
	}
	return ValidateStatusDisplay(c)
}

// ValidateStatusDisplay prüft das Statusdisplay beim Laden; ein leerer Abschnitt ist gültig,
// ein unbekannter Typ (aus einer neueren Version) kein Fehler.
func ValidateStatusDisplay(c StatusDisplayCfg) error {
	if c.I2CBus != "" && !cardBusRe.MatchString(c.I2CBus) {
		return fmt.Errorf("Statusdisplay: I²C-Bus ungültig: %q (erwartet z. B. /dev/i2c-1)", c.I2CBus)
	}
	if c.I2CAddr != 0 && (c.I2CAddr < 0x08 || c.I2CAddr > 0x77) {
		return fmt.Errorf("Statusdisplay: I²C-Adresse %#x ungültig (erlaubt 0x08 bis 0x77)", c.I2CAddr)
	}
	return nil
}

// Settings liefert Bus und Adresse des Displays mit den Standardwerten /dev/i2c-1 und 0x3C.
func (c StatusDisplayCfg) Settings() (bus string, addr int) {
	bus, addr = c.I2CBus, c.I2CAddr
	if bus == "" {
		bus = defaultStatusDisplayBus
	}
	if addr == 0 {
		addr = defaultStatusDisplayAddr
	}
	return bus, addr
}
