package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNetworkTilesDefaultsOffAndIsOmitted(t *testing.T) {
	c := Default()
	if c.Kiosk.NetworkTiles {
		t.Fatal("Kachelseite im Netz ist ab Werk an")
	}
	b, err := json.Marshal(c.Kiosk)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "networkTiles") {
		t.Fatalf("Feld steht ohne Wert in der Datei: %s", b)
	}
	c.Kiosk.NetworkTiles = true
	b, _ = json.Marshal(c.Kiosk)
	if !strings.Contains(string(b), `"networkTiles":true`) {
		t.Fatalf("Feld fehlt: %s", b)
	}
	if err := Validate(c); err != nil {
		t.Fatalf("Validate lehnt ab: %v", err)
	}
}
