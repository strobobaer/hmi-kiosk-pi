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
	// dezimal getippt: Zahl als Bytefolge, höchstwertiges Byte zuerst
	h, r, d, ok = UIDForms("4022250974")
	if !ok || h != "EFBEADDE" || r != "DEADBEEF" || d != "4022250974" {
		t.Errorf("dec: %q %q %q %v", h, r, d, ok)
	}
	if _, _, _, ok := UIDForms("XYZ"); ok {
		t.Error("kein Hex, keine Zahl")
	}
	if _, _, _, ok := UIDForms("123"); ok {
		t.Error("ungerade Hexlaenge ist keine Byte-UID")
	}
}

func TestNormalizeUID(t *testing.T) {
	for _, c := range []struct{ typed, format, want string }{
		{"04a1b2c3", "", "04A1B2C3"},       // leer: wie bisher
		{" 0123456789 ", "", "0123456789"}, // leer: keine Umrechnung
		{"04a1b2c3", "hex", "04A1B2C3"},
		{"C3B2A104", "hex-rev", "04A1B2C3"},
		{"0123456789", "dec", "075BCD15"},
		{"4022250974", "dec", "EFBEADDE"},
		{"0000000001", "dec", "00000001"},
		{"12345678901234", "dec", "000B3A73CE2FF2"},
		{"ZZ", "hex-rev", "ZZ"}, // passt nicht: unverändert
		{"12AB", "dec", "12AB"},
		{"99999999999999999999", "dec", "99999999999999999999"}, // größer als 7 Byte
	} {
		if got := normalizeUID(c.typed, c.format); got != c.want {
			t.Errorf("normalizeUID(%q, %q) = %q, erwartet %q", c.typed, c.format, got, c.want)
		}
	}
}
