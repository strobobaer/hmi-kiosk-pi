package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hmikiosk/internal/release"
)

func packWith(t *testing.T, priv ed25519.PrivateKey, version string, p release.Product) []byte {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, p.Binary), []byte("BIN "+p.ID), 0o755); err != nil {
		t.Fatal(err)
	}
	blob, err := Pack(dir, version, p, p.SystemLevel, priv)
	if err != nil {
		t.Fatal(err)
	}
	return blob
}

// archiveEntries liefert Name und PAX-Datensätze der Einträge eines Pakets.
func archiveEntries(t *testing.T, blob []byte) (names []string, pax map[string]map[string]string) {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(blob[64:]))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	pax = map[string]map[string]string{}
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		names = append(names, h.Name)
		pax[h.Name] = h.PAXRecords
	}
	return names, pax
}

func TestPackHMIKioskUnchanged(t *testing.T) {
	_, priv := newKeys(t)
	names, pax := archiveEntries(t, packWith(t, priv, "1.0.0", release.HMIKiosk))
	if strings.Join(names, ",") != "VERSION,hmi-kiosk" {
		t.Fatalf("Einträge %v", names)
	}
	if _, ok := pax["hmi-kiosk"][productPAX]; ok {
		t.Fatalf("HMI-Kiosk-Paket trägt einen Produktdatensatz: %v", pax["hmi-kiosk"])
	}
	if pax["hmi-kiosk"][systemLevelPAX] != "1" {
		t.Fatalf("Systemstand fehlt: %v", pax["hmi-kiosk"])
	}
}

func TestPackPDHTerminal(t *testing.T) {
	pub, priv := newKeys(t)
	blob := packWith(t, priv, "0.2.0", release.PDHTerminal)
	p, err := Verify(blob, pub)
	if err != nil {
		t.Fatal(err)
	}
	if p.Product != "pdh-terminal" || p.Version != "0.2.0" {
		t.Fatalf("%+v", p)
	}
	names, pax := archiveEntries(t, blob)
	if strings.Join(names, ",") != "VERSION,pdh-terminal" || pax["pdh-terminal"][productPAX] != "pdh-terminal" {
		t.Fatalf("%v %v", names, pax)
	}
	p, err = Verify(goodBlob(t, priv, "1.0.0"), pub)
	if err != nil || p.Product != "hmi-kiosk" {
		t.Fatalf("%+v %v", p, err)
	}
}

func productBlob(t *testing.T, priv ed25519.PrivateKey, binary, productRecord string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: versionName, Typeflag: tar.TypeReg, Mode: 0o644, Size: 6})
	tw.Write([]byte("1.0.0\n"))
	rec := map[string]string{systemLevelPAX: "1"}
	if productRecord != "" {
		rec[productPAX] = productRecord
	}
	tw.WriteHeader(&tar.Header{Name: binary, Typeflag: tar.TypeReg, Mode: 0o755, Size: 3, Format: tar.FormatPAX, PAXRecords: rec})
	tw.Write([]byte("BIN"))
	tw.Close()
	gz.Close()
	return sign(priv, buf.Bytes())
}

func TestVerifyRejectsWrongBinaryName(t *testing.T) {
	pub, priv := newKeys(t)
	for _, c := range []struct{ binary, product string }{
		{"hmi-kiosk", "pdh-terminal"},
		{"pdh-terminal", ""},
		{"pdh-terminal", "hmi-kiosk"},
	} {
		if _, err := Verify(productBlob(t, priv, c.binary, c.product), pub); err == nil {
			t.Errorf("Programm %q mit Produkt %q angenommen", c.binary, c.product)
		}
	}
	if _, err := Verify(productBlob(t, priv, "pdh-terminal", "pdh-terminal"), pub); err != nil {
		t.Errorf("gültiges PDH-Paket abgelehnt: %v", err)
	}
}

func TestVerifyRejectsUnknownProduct(t *testing.T) {
	pub, priv := newKeys(t)
	_, err := Verify(productBlob(t, priv, "hmi-kiosk", "evil"), pub)
	if err == nil || !strings.Contains(err.Error(), "Unbekanntes Produkt im Paket: evil") {
		t.Fatalf("%v", err)
	}
	if _, err := Pack(t.TempDir(), "1.0.0", release.Product{ID: "evil", Binary: "evil"}, 1, priv); err == nil {
		t.Fatal("Pack mit unbekanntem Produkt")
	}
}

func TestVerifyRejectsTwoBinaries(t *testing.T) {
	pub, priv := newKeys(t)
	blob := sign(priv, tarGz(t, []entry{
		{name: "VERSION", body: "1.0.0\n"},
		{name: "hmi-kiosk", body: "a", mode: 0o755},
		{name: "pdh-terminal", body: "b", mode: 0o755},
	}))
	if _, err := Verify(blob, pub); err == nil {
		t.Fatal("zwei Programmdateien angenommen")
	}
}

func TestInstallRejectsForeignProduct(t *testing.T) {
	pub, priv := newKeys(t)
	for _, c := range []struct {
		device, pkg release.Product
		want        string
	}{
		{release.HMIKiosk, release.PDHTerminal, "Paket gehört zu PDH-Terminal, dieses Gerät ist HMI-Kiosk."},
		{release.PDHTerminal, release.HMIKiosk, "Paket gehört zu HMI-Kiosk, dieses Gerät ist PDH-Terminal."},
	} {
		root := t.TempDir()
		in := newInstaller(root, pub)
		in.Product = c.device
		_, err := in.Install(context.Background(), packWith(t, priv, "1.4.0", c.pkg))
		if err == nil || err.Error() != c.want {
			t.Errorf("%s: %v", c.device.ID, err)
		}
		if ents, _ := os.ReadDir(appDir(root)); len(ents) != 0 {
			t.Errorf("Reste: %v", ents)
		}
	}
}

func TestInstallOwnProductUsesItsBinaryName(t *testing.T) {
	pub, priv := newKeys(t)
	root := t.TempDir()
	p, err := Verify(packWith(t, priv, "0.2.0", release.PDHTerminal), pub)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Install(root, p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(appDir(root), "0.2.0", "pdh-terminal")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(appDir(root), "0.2.0", "hmi-kiosk")); err == nil {
		t.Fatal("hmi-kiosk statt pdh-terminal entpackt")
	}
}

// Pakete ohne Produktdatensatz (alle bisherigen) installiert HMI-Kiosk unverändert,
// auch ohne gesetztes Installer.Product.
func TestOldPackageAcceptedByHMI(t *testing.T) {
	pub, priv := newKeys(t)
	for _, prod := range []release.Product{{}, release.HMIKiosk} {
		root := t.TempDir()
		in := newInstaller(root, pub)
		in.Product = prod
		v, err := in.Install(context.Background(), goodBlob(t, priv, "1.4.0"))
		if err != nil || v != "1.4.0" {
			t.Fatalf("%v %v", v, err)
		}
	}
}
