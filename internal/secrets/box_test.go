package secrets

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBoxRoundTripUmlautAndEmpty(t *testing.T) {
	b, err := OpenBox(filepath.Join(t.TempDir(), "box.key"), false)
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range []string{"Pässwörd", ""} {
		sealed, err := b.Seal(in)
		if err != nil {
			t.Fatal(err)
		}
		out, err := b.Open(sealed)
		if err != nil {
			t.Fatal(err)
		}
		if out != in {
			t.Fatalf("got %q, want %q", out, in)
		}
	}
}

func TestBoxReopenKeepsKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "box.key")
	b1, err := OpenBox(path, false)
	if err != nil {
		t.Fatal(err)
	}
	sealed, _ := b1.Seal("geheim")
	b2, err := OpenBox(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := b2.Open(sealed); err != nil || out != "geheim" {
		t.Fatalf("got %q, %v", out, err)
	}
}

func TestBoxOpenRejectsGarbage(t *testing.T) {
	b, _ := OpenBox(filepath.Join(t.TempDir(), "box.key"), false)
	for _, in := range []string{"", "###", "AAAA"} {
		if _, err := b.Open(in); err == nil {
			t.Fatalf("Open(%q) should fail", in)
		}
	}
}

func TestBoxKeyFileMode0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Dateimodi unter Windows nicht aussagekräftig")
	}
	path := filepath.Join(t.TempDir(), "box.key")
	if _, err := OpenBox(path, false); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", fi.Mode().Perm())
	}
}

func TestBoxTreatsTruncatedKeyAsAbsentOnlyWithoutSealedValues(t *testing.T) {
	for _, size := range []int{0, 5, keySize - 1} {
		path := filepath.Join(t.TempDir(), "box.key")
		if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
			t.Fatal(err)
		}
		b, err := OpenBox(path, false)
		if err != nil {
			t.Fatalf("Länge %d ohne verschlüsselte Werte: %v", size, err)
		}
		if raw, _ := os.ReadFile(path); len(raw) != keySize {
			t.Fatalf("Länge %d: neue Datei hat %d Byte", size, len(raw))
		}
		sealed, _ := b.Seal("x")
		if b2, err := OpenBox(path, true); err != nil {
			t.Fatal(err)
		} else if out, err := b2.Open(sealed); err != nil || out != "x" {
			t.Fatalf("neuer Schlüssel nicht dauerhaft: %q %v", out, err)
		}

		if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err = OpenBox(path, true)
		if err == nil || !strings.Contains(err.Error(), "beschädigt") {
			t.Fatalf("Länge %d mit verschlüsselten Werten: %v", size, err)
		}
		if raw, _ := os.ReadFile(path); len(raw) != size {
			t.Fatalf("beschädigte Datei überschrieben: %d Byte", len(raw))
		}
	}
}

func TestBoxRejectsOverlongKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "box.key")
	if err := os.WriteFile(path, make([]byte, keySize+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenBox(path, false); err == nil || !strings.Contains(err.Error(), "beschädigt") {
		t.Fatalf("zu lange Datei: %v", err)
	}
}

func TestBoxWritesKeyWithoutLeftovers(t *testing.T) {
	dir := t.TempDir()
	if _, err := OpenBox(filepath.Join(dir, "box.key"), false); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "box.key" {
		t.Fatalf("Reste im Ordner: %v", entries)
	}
}

func TestBoxMissingKeyFileWithSealedValuesIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "box.key")
	_, err := OpenBox(path, true)
	if !errors.Is(err, ErrKeyMissing) {
		t.Fatalf("fehlende Datei bei verschlüsselten Werten: %v", err)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("Schlüsseldatei trotzdem angelegt")
	}
	if _, err := OpenBox(path, false); err != nil {
		t.Fatalf("erster Start: %v", err)
	}
}

func TestUnavailableBoxFailsCleanlyAndRekeyMakesItWork(t *testing.T) {
	b := Unavailable()
	if b.Available() {
		t.Fatal("Box ohne Schlüssel gilt als verfügbar")
	}
	if _, err := b.Seal("x"); !errors.Is(err, ErrNoKey) {
		t.Fatalf("Seal: %v", err)
	}
	if _, err := b.Open("AAAA"); !errors.Is(err, ErrNoKey) {
		t.Fatalf("Open: %v", err)
	}
	key, err := GenerateKey()
	if err != nil || len(key) != keySize {
		t.Fatalf("GenerateKey: %d Byte, %v", len(key), err)
	}
	if err := b.Rekey(key); err != nil {
		t.Fatal(err)
	}
	enc, err := b.Seal("ä")
	if err != nil || !b.Available() {
		t.Fatalf("Seal nach Rekey: %v", err)
	}
	if out, err := b.Open(enc); err != nil || out != "ä" {
		t.Fatalf("Open: %q %v", out, err)
	}
	if err := b.Rekey([]byte("kurz")); err == nil {
		t.Fatal("zu kurzer Schlüssel angenommen")
	}
}

func TestWriteKeyFileIsAtomicAndReadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "box.key")
	key, _ := GenerateKey()
	if err := WriteKeyFile(path, key); err != nil {
		t.Fatal(err)
	}
	b, err := OpenBox(path, true)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := NewBox(key)
	enc, _ := other.Seal("x")
	if out, err := b.Open(enc); err != nil || out != "x" {
		t.Fatalf("Schlüssel nicht aus Datei geladen: %q %v", out, err)
	}
	if err := WriteKeyFile(filepath.Join(path, "unten"), key); err == nil {
		t.Fatal("Schreiben in einen unmöglichen Pfad meldet keinen Fehler")
	}
}
