package config

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestLocalPortDefault(t *testing.T) {
	c := Default()
	if c.LocalPort() != 8080 {
		t.Fatalf("Grundwert: %d", c.LocalPort())
	}
	c.Kiosk.LocalPort = 18080
	if c.LocalPort() != 18080 {
		t.Fatalf("eingestellt: %d", c.LocalPort())
	}
	// Alte Dateien bleiben Byte für Byte gleich: Ohne Wert fehlt das Feld.
	b, _ := json.Marshal(Default().Kiosk)
	if strings.Contains(string(b), "local_port") {
		t.Errorf("Feld local_port im Grundwert: %s", b)
	}
}

func TestLocalPortJSONField(t *testing.T) {
	c := Default()
	c.Kiosk.LocalPort = 18080
	b, _ := json.Marshal(c)
	if !strings.Contains(string(b), `"local_port":18080`) {
		t.Fatalf("JSON: %s", b)
	}
	var back Config
	if err := json.Unmarshal(b, &back); err != nil || back.Kiosk.LocalPort != 18080 {
		t.Fatalf("Rückweg: %v %d", err, back.Kiosk.LocalPort)
	}
}

func TestValidateLocalPort(t *testing.T) {
	tests := []struct {
		name  string
		port  int
		admin int
		want  string // Teil der Meldung, leer = gültig
	}{
		{"Grundwert 0", 0, 8443, ""},
		{"8080", 8080, 8443, ""},
		{"unterste Grenze", 1024, 8443, ""},
		{"oberste Grenze", 65534, 8443, ""},
		{"zu klein", 1023, 8443, "zwischen 1024 und 65534"},
		{"negativ", -1, 8443, "zwischen 1024 und 65534"},
		{"zu groß", 65535, 8443, "zwischen 1024 und 65534"},
		{"viel zu groß", 70000, 8443, "zwischen 1024 und 65534"},
		{"gleich Admin-Port", 8443, 8443, "Admin-Seite"},
		{"P+1 gleich Admin-Port", 8442, 8443, "Admin-Seite"},
		{"P-1 an Admin-Port ist frei", 8444, 8443, ""},
		{"Admin-Port anders", 8443, 9443, ""},
		{"Grundwert gegen Admin 8081", 0, 8081, "Admin-Seite"},
		{"Grundwert gegen Admin 8080", 0, 8080, "Admin-Seite"},
	}
	for _, tt := range tests {
		err := ValidateLocalPort(tt.port, tt.admin)
		if tt.want == "" {
			if err != nil {
				t.Errorf("%s: %v", tt.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: %v, erwartet %q", tt.name, err, tt.want)
		}
	}
}

func TestValidateRejectsBadLocalPort(t *testing.T) {
	for _, p := range []int{-5, 80, 1023, 65535, 8443, 8442} {
		c := Default()
		c.Kiosk.LocalPort = p
		if err := Validate(c); err == nil {
			t.Errorf("Port %d wurde angenommen", p)
		}
	}
	c := Default()
	c.Kiosk.LocalPort = 18080
	if err := Validate(c); err != nil {
		t.Errorf("gültiger Port abgelehnt: %v", err)
	}
}

func TestLoadRejectsInvalidLocalPortFile(t *testing.T) {
	dir := t.TempDir()
	st := Open(dir)
	good := Default()
	if err := st.Save(good); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkGood(); err != nil {
		t.Fatal(err)
	}
	bad := Default()
	bad.Kiosk.LocalPort = 80
	b, _ := json.MarshalIndent(bad, "", "  ")
	if err := os.WriteFile(st.path(fileName), b, 0o600); err != nil {
		t.Fatal(err)
	}
	c, info, err := st.Load()
	if err != nil || !info.UsedFallback || c.Kiosk.LocalPort != 0 {
		t.Fatalf("Ungültige Datei muss auf den guten Stand zurückfallen: %v %+v %d", err, info, c.Kiosk.LocalPort)
	}
	if !strings.Contains(info.Reason, "Port der lokalen Startseite") {
		t.Errorf("Grund: %q", info.Reason)
	}
}
