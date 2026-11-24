package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestValidatePDH(t *testing.T) {
	many := make([]string, MaxWebOrigins)
	for i := range many {
		many[i] = fmt.Sprintf("https://h%d.firma.de:443", i)
	}
	tests := []struct {
		name    string
		p       PDHCfg
		wantErr string
	}{
		{"leer", PDHCfg{}, ""},
		{"https", PDHCfg{URL: "https://pdh.firma.de"}, ""},
		{"https mit Port", PDHCfg{URL: "https://pdh.firma.de:8443"}, ""},
		{"pfad", PDHCfg{URL: "https://pdh.firma.de/"}, "nur Adresse ohne Pfad"},
		{"pfad2", PDHCfg{URL: "https://pdh.firma.de/x"}, "nur Adresse ohne Pfad"},
		{"abfrage", PDHCfg{URL: "https://pdh.firma.de?a=1"}, "nur Adresse ohne Pfad"},
		{"anker", PDHCfg{URL: "https://pdh.firma.de#a"}, "nur Adresse ohne Pfad"},
		{"Großbuchstaben", PDHCfg{URL: "https://PDH.firma.de"}, "nur Adresse ohne Pfad"},
		{"http ohne Erlaubnis", PDHCfg{URL: "http://10.0.0.5:8080"}, "unverschlüsselt erlauben"},
		{"http mit Erlaubnis", PDHCfg{URL: "http://10.0.0.5:8080", AllowHTTP: true}, ""},
		{"ftp", PDHCfg{URL: "ftp://x"}, "http"},
		{"Zusatz ungültig", PDHCfg{ExtraOrigins: []string{"foo"}}, "Zusätzliche Adresse"},
		{"zu viele Zusätze", PDHCfg{ExtraOrigins: many}, "Zu viele zusätzliche Adressen"},
		{"Zusatz grenze", PDHCfg{ExtraOrigins: many[:MaxWebOrigins-1]}, ""},
		{"Name ungültig", PDHCfg{DeviceName: "a\tb"}, "Steuerzeichen"},
		{"Name ok", PDHCfg{DeviceName: "Halle 1"}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePDH(tc.p)
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

func TestPDHOrigins(t *testing.T) {
	p := PDHCfg{
		URL:          "https://pdh.firma.de",
		ExtraOrigins: []string{"https://cdn.firma.de:443", "https://pdh.firma.de:443", "https://cdn.firma.de:443"},
	}
	want := []string{"https://pdh.firma.de:443", "https://cdn.firma.de:443"}
	if got := PDHOrigins(p); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if got := PDHOrigins(PDHCfg{}); len(got) != 0 {
		t.Fatalf("leer: %v", got)
	}
}

func TestOldConfigUnchanged(t *testing.T) {
	dir := t.TempDir()
	s := Open(dir)
	if err := s.Save(sample()); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(first), `"pdh"`) {
		t.Fatalf("pdh-Schlüssel in alter Konfiguration:\n%s", first)
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

func TestValidateSplit(t *testing.T) {
	c := sample()
	if err := ValidateCore(c); err != nil {
		t.Fatal(err)
	}
	if err := ValidateHMI(c); err != nil {
		t.Fatal(err)
	}
	c.DefaultMode = "x"
	if ValidateCore(c) != nil {
		t.Error("Kern darf HMI-Regeln nicht prüfen")
	}
	if ValidateHMI(c) == nil || Validate(c) == nil {
		t.Error("HMI-Regel fehlt")
	}
	c = sample()
	c.PDH = PDHCfg{URL: "https://x.de/"}
	if Validate(c) == nil {
		t.Error("Validate prüft PDH nicht")
	}
}

func TestValidatePDHFallback(t *testing.T) {
	many := make([]string, MaxWebOrigins)
	for i := range many {
		many[i] = fmt.Sprintf("https://h%d.firma.de:443", i)
	}
	tests := []struct {
		name    string
		p       PDHCfg
		wantErr string
	}{
		{"leer", PDHCfg{URL: "https://pdh.lan"}, ""},
		{"https", PDHCfg{URL: "https://pdh.lan", FallbackURL: "https://pdh.tail1234.ts.net"}, ""},
		{"https mit Port", PDHCfg{URL: "https://pdh.lan", FallbackURL: "https://pdh.tail1234.ts.net:8443"}, ""},
		{"http trotz Erlaubnis", PDHCfg{URL: "https://pdh.lan", FallbackURL: "http://pdh.ts.net", AllowHTTP: true}, "Ausweichadresse ungültig"},
		{"pfad", PDHCfg{URL: "https://pdh.lan", FallbackURL: "https://pdh.ts.net/x"}, "Ausweichadresse ungültig"},
		{"gleich der Hauptadresse", PDHCfg{URL: "https://pdh.lan", FallbackURL: "https://pdh.lan"}, "Ausweichadresse ungültig"},
		{"gleich mit Standardport", PDHCfg{URL: "https://pdh.lan:443", FallbackURL: "https://pdh.lan"}, "Ausweichadresse ungültig"},
		{"Großbuchstaben", PDHCfg{URL: "https://pdh.lan", FallbackURL: "https://PDH.ts.net"}, "Ausweichadresse ungültig"},
		{"Zusatz grenze mit Ausweich", PDHCfg{URL: "https://pdh.lan", FallbackURL: "https://pdh.ts.net", ExtraOrigins: many[:MaxWebOrigins-2]}, ""},
		{"zu viele mit Ausweich", PDHCfg{URL: "https://pdh.lan", FallbackURL: "https://pdh.ts.net", ExtraOrigins: many[:MaxWebOrigins-1]}, "Zu viele zusätzliche Adressen"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePDH(tc.p)
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

func TestPDHOriginsFallback(t *testing.T) {
	p := PDHCfg{URL: "https://pdh.lan", FallbackURL: "https://pdh.t.ts.net", ExtraOrigins: []string{"https://cdn.x:443"}}
	want := []string{"https://pdh.lan:443", "https://pdh.t.ts.net:443", "https://cdn.x:443"}
	if got := PDHOrigins(p); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	p.FallbackURL = ""
	want = []string{"https://pdh.lan:443", "https://cdn.x:443"}
	if got := PDHOrigins(p); !reflect.DeepEqual(got, want) {
		t.Fatalf("ohne Ausweichadresse: got %v want %v", got, want)
	}
}
