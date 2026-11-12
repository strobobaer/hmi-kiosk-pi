package card

import "testing"

func TestFormatUID(t *testing.T) {
	four := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	seven := []byte{0x04, 0xA1, 0xB2, 0xC3, 0xD4, 0xE5, 0xF6}
	tests := []struct {
		name   string
		raw    []byte
		format string
		want   string
	}{
		{"leer=hex", four, "", "DEADBEEF"},
		{"hex", four, "hex", "DEADBEEF"},
		{"hex-rev", four, "hex-rev", "EFBEADDE"},
		{"dec", four, "dec", "4022250974"},
		{"7 Byte hex", seven, "hex", "04A1B2C3D4E5F6"},
		{"7 Byte hex-rev", seven, "hex-rev", "F6E5D4C3B2A104"},
		{"7 Byte dec", seven, "dec", "69495546249912580"},
		{"unbekannt=hex", four, "foo", "DEADBEEF"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			orig := append([]byte(nil), tc.raw...)
			if got := FormatUID(tc.raw, tc.format); got != tc.want {
				t.Errorf("FormatUID = %q, want %q", got, tc.want)
			}
			if string(orig) != string(tc.raw) {
				t.Errorf("Eingabe wurde veraendert")
			}
		})
	}
}

func TestFormatUIDDecLong(t *testing.T) {
	// 10 Byte: darf nicht ueberlaufen (math/big)
	raw := []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	if got := FormatUID(raw, "dec"); got != "1" {
		t.Errorf("got %q, want 1", got)
	}
	raw = []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	if got := FormatUID(raw, "dec"); got != "4722366482869645213696" {
		t.Errorf("got %q", got)
	}
}
