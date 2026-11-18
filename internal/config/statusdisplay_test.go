package config

import (
	"strings"
	"testing"
)

func TestValidateStatusDisplay(t *testing.T) {
	tests := []struct {
		name    string
		c       StatusDisplayCfg
		wantErr string
	}{
		{"leer", StatusDisplayCfg{}, ""},
		{"sh1106", StatusDisplayCfg{Type: "sh1106"}, ""},
		{"voll", StatusDisplayCfg{Type: "sh1106", I2CBus: "/dev/i2c-10", I2CAddr: 0x3D}, ""},
		{"Bus fremd", StatusDisplayCfg{Type: "sh1106", I2CBus: "/dev/null"}, "Bus"},
		{"Adresse unten", StatusDisplayCfg{I2CAddr: 0x07}, "Adresse"},
		{"Adresse oben", StatusDisplayCfg{I2CAddr: 0x78}, "Adresse"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, f := range []func(StatusDisplayCfg) error{ValidateStatusDisplay, ValidateStatusDisplayStrict} {
				err := f(tc.c)
				if tc.wantErr == "" {
					if err != nil {
						t.Fatalf("unerwartet: %v", err)
					}
					continue
				}
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Fehler %v, erwartet mit %q", err, tc.wantErr)
				}
			}
		})
	}
}

func TestValidateStatusDisplayLenientUnknownType(t *testing.T) {
	c := StatusDisplayCfg{Type: "ssd1306"}
	if err := ValidateStatusDisplay(c); err != nil {
		t.Fatalf("locker: %v", err)
	}
	if err := ValidateStatusDisplayStrict(c); err == nil {
		t.Fatal("streng: Fehler erwartet")
	}
	ok, reason := c.Known()
	if ok || reason != "Displaytyp „ssd1306“ wird von dieser Version nicht unterstützt" {
		t.Fatalf("Known: %v %q", ok, reason)
	}
	if ok, _ := (StatusDisplayCfg{Type: "sh1106"}).Known(); !ok {
		t.Fatal("bekannt erwartet")
	}
}

func TestStatusDisplaySettings(t *testing.T) {
	if b, a := (StatusDisplayCfg{}).Settings(); b != "/dev/i2c-1" || a != 0x3C {
		t.Fatalf("Standard: %q %#x", b, a)
	}
	if b, a := (StatusDisplayCfg{I2CBus: "/dev/i2c-3", I2CAddr: 0x3D}).Settings(); b != "/dev/i2c-3" || a != 0x3D {
		t.Fatalf("Eigene: %q %#x", b, a)
	}
}

func TestStoreLoadsFutureValues(t *testing.T) {
	s := Open(t.TempDir())
	c := sample()
	c.Card = CardCfg{Type: "pn7150", PN532Bus: "i2c3"}
	c.StatusDisplay = StatusDisplayCfg{Type: "ssd1306"}
	if err := s.Save(c); err != nil {
		t.Fatal(err)
	}
	got, info, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("info: %+v", info)
	if got.Card.Type != "pn7150" || got.Card.PN532Bus != "i2c3" || got.StatusDisplay.Type != "ssd1306" {
		t.Fatalf("Werte verloren: %+v %+v", got.Card, got.StatusDisplay)
	}
}
