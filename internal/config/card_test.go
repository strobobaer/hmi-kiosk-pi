package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateCard(t *testing.T) {
	long := strings.Repeat("a", 65)
	tests := []struct {
		name    string
		c       CardCfg
		wantErr string
	}{
		{"leer", CardCfg{}, ""},
		{"pn532 Standard", CardCfg{Type: "pn532-i2c"}, ""},
		{"pn532 voll", CardCfg{Type: "pn532-i2c", I2CBus: "/dev/i2c-1", I2CAddr: 0x24, Format: "hex-rev"}, ""},
		{"Bus zweistellig", CardCfg{Type: "pn532-i2c", I2CBus: "/dev/i2c-10"}, ""},
		{"Bus Einschleusung", CardCfg{Type: "pn532-i2c", I2CBus: "/dev/i2c-1;rm"}, "Bus"},
		{"Bus fremd", CardCfg{Type: "pn532-i2c", I2CBus: "/dev/null"}, "Bus"},
		{"Adresse unten", CardCfg{Type: "pn532-i2c", I2CAddr: 0x07}, "Adresse"},
		{"Adresse Grenze unten", CardCfg{Type: "pn532-i2c", I2CAddr: 0x08}, ""},
		{"Adresse Grenze oben", CardCfg{Type: "pn532-i2c", I2CAddr: 0x77}, ""},
		{"Adresse oben", CardCfg{Type: "pn532-i2c", I2CAddr: 0x78}, "Adresse"},
		{"usb", CardCfg{Type: "usb", USBDevice: "ffff:0035"}, ""},
		{"usb ohne Gerät", CardCfg{Type: "usb"}, "Gerät"},
		{"usb zu lang", CardCfg{Type: "usb", USBDevice: long}, "Gerät"},
		{"usb Steuerzeichen", CardCfg{Type: "usb", USBDevice: "a\nb"}, "Steuerzeichen"},
		{"Format hex", CardCfg{Type: "usb", USBDevice: "x", Format: "hex"}, ""},
		{"Format dec", CardCfg{Format: "dec"}, ""},
		{"Format bin", CardCfg{Format: "bin"}, "Format"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateCard(tc.c)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unerwartet: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Fehler %v, erwartet mit %q", err, tc.wantErr)
			}
		})
	}
}

func TestCardSettingsDefaults(t *testing.T) {
	if b, a := (CardCfg{}).Settings(); b != "/dev/i2c-1" || a != 0x24 {
		t.Fatalf("Standard: %q %#x", b, a)
	}
	if b, a := (CardCfg{I2CBus: "/dev/i2c-3", I2CAddr: 0x30}).Settings(); b != "/dev/i2c-3" || a != 0x30 {
		t.Fatalf("Eigene: %q %#x", b, a)
	}
}

func TestConfigWithoutCardUnchanged(t *testing.T) {
	dir := t.TempDir()
	s := Open(dir)
	if err := s.Save(sample()); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(first), `"card"`) {
		t.Fatalf("card-Schlüssel in alter Konfiguration:\n%s", first)
	}
	c, _, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(c); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	if string(first) != string(second) {
		t.Fatalf("Bytes geändert:\n%s\n---\n%s", first, second)
	}
}

func TestValidateCardStrictUnknownType(t *testing.T) {
	err := ValidateCardStrict(CardCfg{Type: "nfc"})
	if err == nil || !strings.Contains(err.Error(), "nicht unterstützt") {
		t.Fatalf("Fehler %v", err)
	}
}

func TestValidateCardLenientUnknownType(t *testing.T) {
	c := CardCfg{Type: "pn7150"}
	if err := ValidateCard(c); err != nil {
		t.Fatalf("locker: %v", err)
	}
	if err := ValidateCardStrict(c); err == nil {
		t.Fatal("streng: Fehler erwartet")
	}
	ok, reason := c.Known()
	if ok || reason != "Lesertyp „pn7150“ wird von dieser Version nicht unterstützt" {
		t.Fatalf("Known: %v %q", ok, reason)
	}
	if ok, _ := (CardCfg{Type: "pn532-i2c", PN532Bus: "uart"}).Known(); !ok {
		t.Fatal("bekannt erwartet")
	}
}

func TestValidateCardBus(t *testing.T) {
	for _, b := range []string{"", "spi", "uart"} {
		if err := ValidateCardStrict(CardCfg{Type: "pn532-i2c", PN532Bus: b}); err != nil {
			t.Fatalf("%q streng: %v", b, err)
		}
	}
	c := CardCfg{Type: "pn532-i2c", PN532Bus: "i2c3"}
	if err := ValidateCard(c); err != nil {
		t.Fatalf("locker: %v", err)
	}
	if err := ValidateCardStrict(c); err == nil {
		t.Fatal("streng: Fehler erwartet")
	}
	ok, reason := c.Known()
	if ok || reason != "PN532-Anschluss „i2c3“ wird von dieser Version nicht unterstützt" {
		t.Fatalf("Known: %v %q", ok, reason)
	}
}

func TestValidateCardDevices(t *testing.T) {
	good := []CardCfg{
		{SPIDev: "/dev/spidev0.1"}, {UARTDev: "/dev/ttyAMA0"}, {UARTDev: "/dev/ttyUSB12"},
	}
	for _, c := range good {
		if err := ValidateCard(c); err != nil {
			t.Fatalf("%+v locker: %v", c, err)
		}
		if err := ValidateCardStrict(c); err != nil {
			t.Fatalf("%+v streng: %v", c, err)
		}
	}
	bad := []CardCfg{
		{SPIDev: "/dev/spidev0"}, {SPIDev: "/dev/sda"}, {UARTDev: "/dev/ttyAMA0;x"}, {UARTDev: "/dev/sda"},
	}
	for _, c := range bad {
		if err := ValidateCard(c); err == nil {
			t.Fatalf("%+v locker: Fehler erwartet", c)
		}
		if err := ValidateCardStrict(c); err == nil {
			t.Fatalf("%+v streng: Fehler erwartet", c)
		}
	}
}

func TestCardDefaults(t *testing.T) {
	if (CardCfg{}).SPIDevice() != "/dev/spidev0.0" || (CardCfg{}).UARTDevice() != "/dev/ttyAMA0" {
		t.Fatal("Standardwerte")
	}
	c := CardCfg{SPIDev: "/dev/spidev1.0", UARTDev: "/dev/ttyUSB0"}
	if c.SPIDevice() != "/dev/spidev1.0" || c.UARTDevice() != "/dev/ttyUSB0" {
		t.Fatal("gesetzte Werte")
	}
}

func TestOldCardConfigRoundTrip(t *testing.T) {
	c := Default()
	c.Card = CardCfg{Type: "pn532-i2c", I2CBus: "/dev/i2c-1"}
	first, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	var back Config
	if err := json.Unmarshal(first, &back); err != nil {
		t.Fatal(err)
	}
	second, err := json.MarshalIndent(back, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("Ausgaben unterscheiden sich:\n%s\n---\n%s", first, second)
	}
	for _, k := range []string{"statusDisplay", "pn532Bus", "spiDev", "uartDev"} {
		if strings.Contains(string(first), k) {
			t.Fatalf("Schlüssel %q in alter Konfiguration", k)
		}
	}
}
