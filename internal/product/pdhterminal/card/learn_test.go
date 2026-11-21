package card

import "testing"

func TestSuggestFormat(t *testing.T) {
	for in, want := range map[string]string{
		"04A1B2C3":       "hex",
		"04A1B2C3D4E5F6": "hex",
		"0123456789":     "dec",
		"12345":          "hex",
		"":               "hex",
		"04a1b2c3":       "hex",
	} {
		if got := SuggestFormat(in); got != want {
			t.Errorf("SuggestFormat(%q) = %q, erwartet %q", in, got, want)
		}
	}
}

func TestUIDForms(t *testing.T) {
	// hex getippt: Bytes in Lesereihenfolge
	h, r, d, ok := UIDForms("DEADBEEF")
	if !ok || h != "DEADBEEF" || r != "EFBEADDE" || d != "4022250974" {
		t.Errorf("hex: %q %q %q %v", h, r, d, ok)
	}
	// dezimal getippt: so wie FormatUID "dec" es schreibt (Pi)
	h, r, d, ok = UIDForms("4022250974")
	if !ok || h != "DEADBEEF" || r != "EFBEADDE" || d != "4022250974" {
		t.Errorf("dec: %q %q %q %v", h, r, d, ok)
	}
	for _, bad := range []string{"XYZ", "123", "9999999999"} { // keine Zahl, ungerade, größer als 4 Byte
		if _, _, _, ok := UIDForms(bad); ok {
			t.Errorf("%q darf keine UID sein", bad)
		}
	}
}

// dec bedeutet in beiden Richtungen dasselbe: normalizeUID ist die Umkehrung von FormatUID "dec".
func TestDecRoundTrip(t *testing.T) {
	for _, raw := range [][]byte{
		{0xDE, 0xAD, 0xBE, 0xEF}, {0x04, 0xA1, 0xB2, 0xC3}, {0x00, 0x00, 0x00, 0x01}, {0xFF, 0xFF, 0xFF, 0xFF},
		{0x04, 0xA1, 0xB2, 0xC3, 0xD4, 0xE5, 0xF6}, {0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01},
	} {
		dec := FormatUID(raw, "dec")
		if got, want := normalizeUID(dec, "dec"), FormatUID(raw, "hex"); got != want {
			t.Errorf("%X: dec %q -> %q, erwartet %q", raw, dec, got, want)
		}
	}
}

func TestNormalizeUID(t *testing.T) {
	for _, c := range []struct{ typed, format, want string }{
		{"04a1b2c3", "", "04A1B2C3"},       // leer: wie bisher
		{" 0123456789 ", "", "0123456789"}, // leer: keine Umrechnung
		{"04a1b2c3", "hex", "04A1B2C3"},
		{"C3B2A104", "hex-rev", "04A1B2C3"},
		{"4022250974", "dec", "DEADBEEF"}, // FormatUID(DE AD BE EF, "dec")
		{"0123456789", "dec", "15CD5B07"},
		{"0000000001", "dec", "01000000"},
		{"4294967295", "dec", "FFFFFFFF"},
		{"ZZ", "hex-rev", "ZZ"}, // passt nicht: unverändert
		{"12AB", "dec", "12AB"},
		{"9999999999", "dec", "9999999999"},                     // 10 Ziffern, größer als 4 Byte: unverändert
		{"99999999999999999999", "dec", "99999999999999999999"}, // größer als 7 Byte
	} {
		if got := normalizeUID(c.typed, c.format); got != c.want {
			t.Errorf("normalizeUID(%q, %q) = %q, erwartet %q", c.typed, c.format, got, c.want)
		}
	}
}
