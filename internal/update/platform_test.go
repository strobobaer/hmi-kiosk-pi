package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hmikiosk/internal/release"
)

func winKeys(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

// platformBlob packt pdh-terminal mit der Plattform (die Programmdatei heißt dann pdh-terminal.exe).
func platformBlob(t *testing.T, priv ed25519.PrivateKey, version, platform string) []byte {
	t.Helper()
	dir := t.TempDir()
	name := release.PDHTerminal.Binary
	if platform != "" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("BIN "+platform), 0o755); err != nil {
		t.Fatal(err)
	}
	blob, err := PackFor(dir, version, release.PDHTerminal, platform, release.PDHTerminal.SystemLevel, priv)
	if err != nil {
		t.Fatal(err)
	}
	return blob
}

func TestVerifyWindowsPackage(t *testing.T) {
	pub, priv := winKeys(t)
	blob := platformBlob(t, priv, "1.2.3", release.PlatformWindowsAMD64)
	names, pax := archiveEntries(t, blob)
	if strings.Join(names, ",") != "VERSION,pdh-terminal.exe" {
		t.Fatalf("Einträge %v", names)
	}
	if pax["pdh-terminal.exe"]["HMIKIOSK.platform"] != "windows-amd64" {
		t.Fatalf("PAX %v", pax["pdh-terminal.exe"])
	}
	p, err := Verify(blob, pub)
	if err != nil {
		t.Fatal(err)
	}
	if p.Platform != "windows-amd64" || p.Binary != "pdh-terminal.exe" || p.Product != "pdh-terminal" || p.Version != "1.2.3" {
		t.Fatalf("%+v", p)
	}
	lin, err := Verify(platformBlob(t, priv, "1.2.3", ""), pub)
	if err != nil || lin.Platform != "" || lin.Binary != "pdh-terminal" {
		t.Fatalf("Linux: %+v %v", lin, err)
	}
}

// Alte Geräte kennen nur die Programmnamen ohne .exe. Sie prüfen das Archiv mit walk und
// ihrer Namensliste; das Windows-Paket scheitert dort an der unzulässigen Datei.
func TestLegacyRejectsWindowsPackage(t *testing.T) {
	_, priv := winKeys(t)
	blob := platformBlob(t, priv, "1.2.3", release.PlatformWindowsAMD64)
	legacy := []string{"hmi-kiosk", "pdh-terminal"}
	err := walk(blob[64:], legacy, func(*tar.Header, io.Reader) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "unzulässige Datei") {
		t.Fatalf("alter Prüfpfad: %v", err)
	}
}

func TestVerifyExeNameNeedsWindowsPlatform(t *testing.T) {
	pub, priv := winKeys(t)
	cases := []struct{ name, platform string }{
		{"pdh-terminal.exe", ""},                // .exe ohne Plattformeintrag
		{"pdh-terminal", "windows-amd64"},       // Plattform windows ohne .exe
		{"pdh-terminal", "plan9"},               // fremde Plattform
		{"pdh-terminal.exe", "plan9"},           // fremde Plattform mit .exe
		{"hmi-kiosk.exe", "windows-amd64"},      // Name passt nicht zum Produkt
		{"pdh-terminal.exe", "windows-amd64\n"}, // ungültige Schreibweise
	}
	for _, c := range cases {
		if _, err := Verify(platformPAXBlob(t, priv, c.name, c.platform), pub); err == nil {
			t.Errorf("%q mit Plattform %q angenommen", c.name, c.platform)
		}
	}
}

// platformPAXBlob baut ein Paket mit frei wählbarem Namen und Plattform-Datensatz.
func platformPAXBlob(t *testing.T, priv ed25519.PrivateKey, name, platform string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	pax := map[string]string{systemLevelPAX: "1", productPAX: "pdh-terminal"}
	if platform != "" {
		pax["HMIKIOSK.platform"] = platform
	}
	for _, f := range []struct {
		n   string
		b   string
		pax map[string]string
	}{{"VERSION", "1.0.0\n", nil}, {name, "x", pax}} {
		h := &tar.Header{Name: f.n, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(f.b)), PAXRecords: f.pax}
		if f.pax != nil {
			h.Format = tar.FormatPAX
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(f.b))
	}
	tw.Close()
	gz.Close()
	return sign(priv, buf.Bytes())
}

func TestInstallerRejectsOtherPlatform(t *testing.T) {
	pub, priv := winKeys(t)
	win := platformBlob(t, priv, "1.2.3", release.PlatformWindowsAMD64)
	lin := platformBlob(t, priv, "1.2.4", "")
	cases := []struct {
		name, devPlatform string
		blob              []byte
	}{
		{"Windows-Gerät, Linux-Paket", release.PlatformWindowsAMD64, lin},
		{"Linux-Gerät, Windows-Paket", "", win},
	}
	for _, c := range cases {
		root := t.TempDir()
		in := newInstaller(root, pub)
		in.Product = release.PDHTerminal
		in.Platform = c.devPlatform
		in.Activate = func(context.Context, string) error { t.Error("aktiviert"); return nil }
		_, err := in.Install(context.Background(), c.blob)
		if err == nil || !strings.Contains(err.Error(), "anderes Betriebssystem") {
			t.Errorf("%s: %v", c.name, err)
		} else if err.Error() != "Paket ist für ein anderes Betriebssystem" {
			t.Errorf("%s: Text %q", c.name, err)
		}
	}
}

func TestInstallWindowsWritesExeAndPackage(t *testing.T) {
	pub, priv := winKeys(t)
	win := platformBlob(t, priv, "1.2.3", release.PlatformWindowsAMD64)
	root := t.TempDir()
	in := newInstaller(root, pub)
	in.Product = release.PDHTerminal
	in.Platform = release.PlatformWindowsAMD64
	in.Activate = func(context.Context, string) error { return nil }
	v, err := in.Install(context.Background(), win)
	if err != nil || v != "1.2.3" {
		t.Fatalf("%q %v", v, err)
	}
	dir := filepath.Join(appDir(root), "1.2.3")
	if b, err := os.ReadFile(filepath.Join(dir, "pdh-terminal.exe")); err != nil || string(b) != "BIN windows-amd64" {
		t.Fatalf("exe: %q %v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "package.pkg")); err != nil || !bytes.Equal(b, win) {
		t.Fatalf("package.pkg weicht ab: %v", err)
	}
	// Linux legt kein package.pkg an.
	root2 := t.TempDir()
	in2 := newInstaller(root2, pub)
	in2.Product = release.PDHTerminal
	in2.Activate = func(context.Context, string) error { return nil }
	if _, err := in2.Install(context.Background(), platformBlob(t, priv, "1.2.4", "")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(appDir(root2), "1.2.4", "package.pkg")); !os.IsNotExist(err) {
		t.Fatalf("package.pkg unter Linux: %v", err)
	}
}

func TestPackRejectsBadPlatform(t *testing.T) {
	_, priv := winKeys(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "pdh-terminal"), []byte("x"), 0o755)
	if _, err := PackFor(dir, "1.0.0", release.PDHTerminal, "plan9", 1, priv); err == nil {
		t.Fatal("fremde Plattform gepackt")
	}
}
