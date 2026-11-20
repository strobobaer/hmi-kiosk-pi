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
	// dezimal getippt: dec ist die umgekehrte Bytefolge als Zahl
	h, r, d, ok = UIDForms("4022250974")
	if !ok || h != "DEADBEEF" || r != "EFBEADDE" || d != "4022250974" {
		t.Errorf("dec: %q %q %q %v", h, r, d, ok)
	}
	if _, _, _, ok := UIDForms("XYZ"); ok {
		t.Error("kein Hex, keine Zahl")
	}
	if _, _, _, ok := UIDForms("123"); ok {
		t.Error("ungerade Hexlaenge ist keine Byte-UID")
	}
}
