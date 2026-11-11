package main

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"hmikiosk/internal/release"
	"hmikiosk/internal/update"
)

func runCLI(args ...string) (int, string) {
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.String() + errb.String()
}

func TestKeygenPackVerify(t *testing.T) {
	keys := filepath.Join(t.TempDir(), "keys")
	if code, out := runCLI("keygen", "-out", keys); code != 0 {
		t.Fatalf("keygen: %d %s", code, out)
	}
	fi, err := os.Stat(filepath.Join(keys, "release.key"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("Modus %v", fi.Mode().Perm())
	}
	// Kein Überschreiben.
	before, _ := os.ReadFile(filepath.Join(keys, "release.key"))
	if code, _ := runCLI("keygen", "-out", keys); code == 0 {
		t.Fatal("überschrieben")
	}
	after, _ := os.ReadFile(filepath.Join(keys, "release.key"))
	if !bytes.Equal(before, after) {
		t.Fatal("Schlüssel verändert")
	}

	in := t.TempDir()
	os.WriteFile(filepath.Join(in, "hmi-kiosk"), []byte("BIN"), 0o755)
	outFile := filepath.Join(t.TempDir(), "u.pkg")
	if code, out := runCLI("pack", "-key", filepath.Join(keys, "release.key"), "-version", "3.2.1", "-in", in, "-out", outFile); code != 0 {
		t.Fatalf("pack: %d %s", code, out)
	}
	blob, _ := os.ReadFile(outFile)
	pubHex, _ := os.ReadFile(filepath.Join(keys, "release.pub"))
	pub, err := hex.DecodeString(string(bytes.TrimSpace(pubHex)))
	if err != nil {
		t.Fatal(err)
	}
	p, err := update.Verify(blob, pub)
	if err != nil || p.Version != "3.2.1" {
		t.Fatalf("%v %v", p, err)
	}
}

func TestPackRejectsBadInput(t *testing.T) {
	keys := filepath.Join(t.TempDir(), "keys")
	runCLI("keygen", "-out", keys)
	in := t.TempDir()
	os.WriteFile(filepath.Join(in, "hmi-kiosk"), []byte("BIN"), 0o755)
	key := filepath.Join(keys, "release.key")
	out := filepath.Join(t.TempDir(), "u.pkg")
	if code, _ := runCLI("pack", "-key", key, "-version", "x", "-in", in, "-out", out); code == 0 {
		t.Error("Version x angenommen")
	}
	if code, _ := runCLI("pack", "-key", "/nicht/da", "-version", "1.0.0", "-in", in, "-out", out); code == 0 {
		t.Error("fehlender Schlüssel angenommen")
	}
	if code, _ := runCLI("pack", "-version", "1.0.0", "-in", in, "-out", out); code == 0 {
		t.Error("fehlende Flags angenommen")
	}
	if code, _ := runCLI("nonsense"); code == 0 {
		t.Error("unbekannter Befehl angenommen")
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("Ausgabe trotz Fehler")
	}
}

func TestPackProductFlag(t *testing.T) {
	keys := filepath.Join(t.TempDir(), "keys")
	runCLI("keygen", "-out", keys)
	key := filepath.Join(keys, "release.key")
	pubHex, _ := os.ReadFile(filepath.Join(keys, "release.pub"))
	pub, _ := hex.DecodeString(string(bytes.TrimSpace(pubHex)))
	in := t.TempDir()
	os.WriteFile(filepath.Join(in, "pdh-terminal"), []byte("BIN"), 0o755)
	out := filepath.Join(t.TempDir(), "p.pkg")
	if code, o := runCLI("pack", "-key", key, "-version", "0.2.0", "-in", in, "-out", out, "-product", "pdh-terminal"); code != 0 {
		t.Fatalf("pack: %d %s", code, o)
	}
	blob, _ := os.ReadFile(out)
	if p, err := update.Verify(blob, pub); err != nil || p.Product != "pdh-terminal" {
		t.Fatalf("%v %v", p, err)
	}
	// Ordner ohne <Binary> des Produkts und unbekanntes Produkt scheitern.
	if code, _ := runCLI("pack", "-key", key, "-version", "0.2.0", "-in", in, "-out", out+"2"); code == 0 {
		t.Fatal("hmi-kiosk-Paket ohne hmi-kiosk im Ordner")
	}
	if code, _ := runCLI("pack", "-key", key, "-version", "0.2.0", "-in", in, "-out", out+"3", "-product", "evil"); code == 0 {
		t.Fatal("unbekanntes Produkt")
	}
}

// Ohne -system-level gilt der Systemstand des gewählten Produkts, nicht der des HMI-Kiosks.
func TestPackSystemLevelDefaultsToProduct(t *testing.T) {
	old := release.PDHTerminal.SystemLevel
	release.PDHTerminal.SystemLevel = 3
	t.Cleanup(func() { release.PDHTerminal.SystemLevel = old })

	keys := filepath.Join(t.TempDir(), "keys")
	runCLI("keygen", "-out", keys)
	key := filepath.Join(keys, "release.key")
	pubHex, _ := os.ReadFile(filepath.Join(keys, "release.pub"))
	pub, _ := hex.DecodeString(string(bytes.TrimSpace(pubHex)))
	in := t.TempDir()
	os.WriteFile(filepath.Join(in, "pdh-terminal"), []byte("BIN"), 0o755)

	for _, c := range []struct {
		args []string
		want int
	}{
		{nil, 3},
		{[]string{"-system-level", "2"}, 2},
	} {
		out := filepath.Join(t.TempDir(), "p.pkg")
		args := append([]string{"pack", "-key", key, "-version", "0.2.0", "-in", in, "-out", out, "-product", "pdh-terminal"}, c.args...)
		if code, o := runCLI(args...); code != 0 {
			t.Fatalf("pack: %d %s", code, o)
		}
		blob, _ := os.ReadFile(out)
		p, err := update.Verify(blob, pub)
		if err != nil || p.SystemLevel != c.want {
			t.Fatalf("Systemstand %v, erwartet %d (%v)", p, c.want, err)
		}
	}
}
