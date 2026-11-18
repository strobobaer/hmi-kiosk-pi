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
	// PN532Bus ist der Anschluss des PN532: "" (I²C), "spi" oder "uart".
	PN532Bus string `json:"pn532Bus,omitempty"`
	// SPIDev ist das SPI-Gerät des PN532, z. B. /dev/spidev0.0 (leer: Standard).
	SPIDev string `json:"spiDev,omitempty"`
	// UARTDev ist die serielle Schnittstelle des PN532, z. B. /dev/ttyAMA0 (leer: Standard).
	UARTDev string `json:"uartDev,omitempty"`
	// Format bestimmt die Schreibweise der UID: hex, hex-rev oder dec (leer: hex).
	Format string `json:"format,omitempty"`
}

// Erlaubte Lesertypen der Konfiguration.
const (
	// CardTypePN532I2C ist der gespeicherte Wert für jeden PN532; der Bus steht in PN532Bus
	// (Rückwärtskompatibilität, nicht umbenennen).
	CardTypePN532I2C = "pn532-i2c"
	CardTypeUSB      = "usb"
)

// Anschlüsse des PN532 (PN532Bus); leer bedeutet I²C.
const (
	CardBusSPI  = "spi"
	CardBusUART = "uart"
)

const (
	defaultCardBus  = "/dev/i2c-1"
	defaultCardAddr = 0x24
	maxCardDevice   = 64

	defaultCardSPI  = "/dev/spidev0.0"
	defaultCardUART = "/dev/ttyAMA0"
)

var (
	cardBusRe  = regexp.MustCompile(`^/dev/i2c-[0-9]{1,2}$`)
	cardSPIRe  = regexp.MustCompile(`^/dev/spidev[0-9]\.[0-9]$`)
	cardUARTRe = regexp.MustCompile(`^/dev/tty(AMA|S|USB|ACM)[0-9]{1,2}$`)
)

// Known meldet, ob diese Version Lesertyp und PN532-Anschluss kennt; sonst steht in reason
// der Grund. Werte einer neueren Version bleiben in der Datei, der Leser bleibt aus.
func (c CardCfg) Known() (ok bool, reason string) {
	switch c.Type {
	case "", CardTypePN532I2C, CardTypeUSB:
	default:
		return false, fmt.Sprintf("Lesertyp „%s“ wird von dieser Version nicht unterstützt", c.Type)
	}
	if c.Type == CardTypePN532I2C { // der Anschluss gilt nur für den PN532
		switch c.PN532Bus {
		case "", CardBusSPI, CardBusUART:
		default:
			return false, fmt.Sprintf("PN532-Anschluss „%s“ wird von dieser Version nicht unterstützt", c.PN532Bus)
		}
	}
	return true, ""
}

// ValidateCardStrict ist ValidateCard und lässt zusätzlich nur bekannte Typen und
// Anschlüsse zu; die Admin-Seite speichert nur solche.
func ValidateCardStrict(c CardCfg) error {
	if ok, reason := c.Known(); !ok {
		return fmt.Errorf("Kartenleser: %s", reason)
	}
	return ValidateCard(c)
}

// ValidateCard prüft den Kartenleser beim Laden; ein leerer Abschnitt ist gültig. Unbekannte
// Typen und Anschlüsse (aus einer neueren Version) sind kein Fehler, alle Formate schon.
func ValidateCard(c CardCfg) error {
	if c.I2CBus != "" && !cardBusRe.MatchString(c.I2CBus) {
		return fmt.Errorf("Kartenleser: I²C-Bus ungültig: %q (erwartet z. B. /dev/i2c-1)", c.I2CBus)
	}
	if c.I2CAddr != 0 && (c.I2CAddr < 0x08 || c.I2CAddr > 0x77) {
		return fmt.Errorf("Kartenleser: I²C-Adresse %#x ungültig (erlaubt 0x08 bis 0x77)", c.I2CAddr)
	}
	if c.SPIDev != "" && !cardSPIRe.MatchString(c.SPIDev) {
		return fmt.Errorf("Kartenleser: SPI-Gerät ungültig: %q (erwartet z. B. /dev/spidev0.0)", c.SPIDev)
	}
	if c.UARTDev != "" && !cardUARTRe.MatchString(c.UARTDev) {
		return fmt.Errorf("Kartenleser: serielle Schnittstelle ungültig: %q (erwartet z. B. /dev/ttyAMA0)", c.UARTDev)
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

// SPIDevice liefert das SPI-Gerät des PN532, Standard /dev/spidev0.0.
func (c CardCfg) SPIDevice() string {
	if c.SPIDev == "" {
		return defaultCardSPI
	}
	return c.SPIDev
}

// UARTDevice liefert die serielle Schnittstelle des PN532, Standard /dev/ttyAMA0.
func (c CardCfg) UARTDevice() string {
	if c.UARTDev == "" {
		return defaultCardUART
	}
	return c.UARTDev
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
