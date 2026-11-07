package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHardwareIDRoundTripsAndIsOptional(t *testing.T) {
	c := Default()
	if c.Terminal.HardwareID != "" {
		t.Fatalf("Grundeinstellung hat Kennung %q", c.Terminal.HardwareID)
	}
	if err := Validate(c); err != nil {
		t.Fatalf("leere Kennung muss erlaubt sein: %v", err)
	}
	c.Terminal.HardwareID = "10000000abcdef01"
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"hardwareId":"10000000abcdef01"`) {
		t.Fatalf("JSON-Name falsch: %s", raw)
	}
	var back Config
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.Terminal.HardwareID != c.Terminal.HardwareID {
		t.Fatalf("got %q", back.Terminal.HardwareID)
	}
	if err := Validate(back); err != nil {
		t.Fatal(err)
	}
}

func TestOldConfigWithoutHardwareIDStillLoads(t *testing.T) {
	var c Config
	old := `{"schemaVersion":1,"terminal":{"id":"abc","name":"T","role":"normal"},"defaultMode":"takeover","client":{"profile":"remmina","quality":2,"colorDepth":32}}`
	if err := json.Unmarshal([]byte(old), &c); err != nil {
		t.Fatal(err)
	}
	if err := Validate(c); err != nil {
		t.Fatal(err)
	}
}

func TestHardwareChanged(t *testing.T) {
	cases := []struct {
		stored, current string
		want            bool
	}{
		{"", "", false},
		{"", "A", false}, // Altbestand, wird beim Start gesetzt
		{"A", "", false}, // keine lesbare Kennung: keine Klon-Erkennung
		{"A", "A", false},
		{"A", "B", true},
	}
	for _, tc := range cases {
		if got := HardwareChanged(tc.stored, tc.current); got != tc.want {
			t.Errorf("HardwareChanged(%q,%q)=%v", tc.stored, tc.current, got)
		}
	}
}

func TestNewTerminalIDIsFreshAndValid(t *testing.T) {
	a, b := NewTerminalID(), NewTerminalID()
	if a == b || len(a) != 16 {
		t.Fatalf("%q %q", a, b)
	}
}
