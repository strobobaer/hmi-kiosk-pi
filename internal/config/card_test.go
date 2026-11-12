package config

import (
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
		{"Typ unbekannt", CardCfg{Type: "nfc"}, "Typ"},
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
