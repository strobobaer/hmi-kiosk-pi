package secrets

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestBoxRoundTripUmlautAndEmpty(t *testing.T) {
	b, err := OpenBox(filepath.Join(t.TempDir(), "box.key"))
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
	b1, err := OpenBox(path)
	if err != nil {
		t.Fatal(err)
	}
	sealed, _ := b1.Seal("geheim")
	b2, err := OpenBox(path)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := b2.Open(sealed); err != nil || out != "geheim" {
		t.Fatalf("got %q, %v", out, err)
	}
}

func TestBoxOpenRejectsGarbage(t *testing.T) {
	b, _ := OpenBox(filepath.Join(t.TempDir(), "box.key"))
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
	if _, err := OpenBox(path); err != nil {
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
