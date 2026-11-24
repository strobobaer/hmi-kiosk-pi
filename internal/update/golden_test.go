package update

import (
	"bytes"
	"crypto/ed25519"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"hmikiosk/internal/release"
)

var updateGolden = flag.Bool("update-golden", false, "Golden-Dateien neu schreiben")

// goldenPackage packt mit festen Eingaben (Schlüssel aus festem Startwert, feste Programmdatei).
func goldenPackage(t *testing.T, p release.Product) []byte {
	t.Helper()
	priv := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, ed25519.SeedSize))
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, p.Binary), []byte("golden binary "+p.ID), 0o755); err != nil {
		t.Fatal(err)
	}
	blob, err := Pack(dir, "1.2.3", p, p.SystemLevel, priv)
	if err != nil {
		t.Fatal(err)
	}
	return blob
}

// TestLinuxPackageUnchanged: Linux-Pakete (ohne Plattform) bleiben byteweise gleich dem Stand
// vor der Plattformänderung. Die Golden-Dateien stammen aus dem Code vor dieser Änderung.
func TestLinuxPackageUnchanged(t *testing.T) {
	for name, p := range map[string]release.Product{"hmi-kiosk": release.HMIKiosk, "pdh-terminal": release.PDHTerminal} {
		got := goldenPackage(t, p)
		path := filepath.Join("testdata", "golden-"+name+".pkg")
		if *updateGolden {
			if err := os.WriteFile(path, got, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: Paket weicht vom Golden-Stand ab", name)
		}
	}
}
