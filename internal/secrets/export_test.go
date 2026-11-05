package secrets

import (
	"bytes"
	"testing"
)

func TestExportRoundTrip(t *testing.T) {
	data := []byte(`{"vnc":"Pässwörd"}`)
	blob, err := SealExport(data, "export-pw")
	if err != nil {
		t.Fatal(err)
	}
	got, err := OpenExport(blob, "export-pw")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("got %q", got)
	}
}

func TestExportWrongPasswordFails(t *testing.T) {
	blob, _ := SealExport([]byte("daten"), "richtig")
	if _, err := OpenExport(blob, "falsch"); err == nil {
		t.Fatal("falsches Passwort muss fehlschlagen")
	}
}

func TestExportTamperedFails(t *testing.T) {
	blob, _ := SealExport([]byte("daten"), "pw")
	blob[len(blob)-1] ^= 0xff
	if _, err := OpenExport(blob, "pw"); err == nil {
		t.Fatal("manipulierter Export muss fehlschlagen")
	}
}

func TestExportTooShortFails(t *testing.T) {
	for _, n := range []int{0, 1, 10} {
		if _, err := OpenExport(make([]byte, n), "pw"); err == nil {
			t.Fatalf("len %d should fail", n)
		}
	}
}

func TestCheckPasswordAccepts(t *testing.T) {
	h, err := HashPassword("Pässwörd")
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPassword(h, "Pässwörd") {
		t.Fatal("richtiges Passwort abgelehnt")
	}
}

func TestCheckPasswordRejectsWrong(t *testing.T) {
	h, _ := HashPassword("richtig")
	if CheckPassword(h, "falsch") {
		t.Fatal("falsches Passwort akzeptiert")
	}
	if CheckPassword("kaputt", "richtig") || CheckPassword("", "") {
		t.Fatal("ungültiger Hash akzeptiert")
	}
}
