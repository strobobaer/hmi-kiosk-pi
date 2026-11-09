package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)


func packLevelWith(t *testing.T, priv []byte, version string, level int) []byte {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, BinaryName), []byte("BIN "+version), 0o755); err != nil {
		t.Fatal(err)
	}
	blob, err := Pack(dir, version, level, priv)
	if err != nil {
		t.Fatal(err)
	}
	return blob
}

func TestPackCarriesSystemLevel(t *testing.T) {
	pub, priv := newKeys(t)
	p, err := Verify(packLevelWith(t, priv, "1.2.3", 7), pub)
	if err != nil {
		t.Fatal(err)
	}
	if p.SystemLevel != 7 {
		t.Fatalf("SystemLevel = %d, erwartet 7", p.SystemLevel)
	}
}

func TestPackageWithoutSystemLevelIsZero(t *testing.T) {
	pub, priv := newKeys(t)
	p, err := Verify(goodBlob(t, priv, "1.0.0"), pub)
	if err != nil {
		t.Fatal(err)
	}
	if p.SystemLevel != 0 {
		t.Fatalf("SystemLevel = %d, erwartet 0", p.SystemLevel)
	}
}

func TestPackRejectsBadSystemLevel(t *testing.T) {
	_, priv := newKeys(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, BinaryName), []byte("BIN"), 0o755)
	for _, l := range []int{0, -1, maxSystemLevel + 1} {
		if _, err := Pack(dir, "1.0.0", l, priv); err == nil {
			t.Errorf("Stand %d angenommen", l)
		}
	}
}

func TestVerifyRejectsInvalidSystemLevelRecord(t *testing.T) {
	pub, priv := newKeys(t)
	for _, v := range []string{"", "x", "0", "-3", "10000"} {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		tw.WriteHeader(&tar.Header{Name: versionName, Typeflag: tar.TypeReg, Mode: 0o644, Size: 6})
		tw.Write([]byte("1.0.0\n"))
		tw.WriteHeader(&tar.Header{Name: BinaryName, Typeflag: tar.TypeReg, Mode: 0o755, Size: 3,
			Format: tar.FormatPAX, PAXRecords: map[string]string{systemLevelPAX: v}})
		tw.Write([]byte("BIN"))
		tw.Close()
		gz.Close()
		if _, err := Verify(sign(priv, buf.Bytes()), pub); err == nil {
			t.Errorf("Datensatz %q angenommen", v)
		}
	}
}

// Ältere Terminals (bis 0.1.6) lesen das Archiv mit tar.Reader und prüfen nur Name und Art:
// Der PAX-Datensatz darf keinen zusätzlichen Eintrag erzeugen, sonst lehnten sie jedes neue Paket ab.
func TestSystemLevelRecordInvisibleToOldReaders(t *testing.T) {
	_, priv := newKeys(t)
	blob := packLevelWith(t, priv, "1.0.0", 1)
	gz, err := gzip.NewReader(bytes.NewReader(blob[64:]))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var names []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Typeflag != tar.TypeReg {
			t.Fatalf("Eintrag %q hat Art %c", h.Name, h.Typeflag)
		}
		names = append(names, h.Name)
	}
	if strings.Join(names, ",") != "VERSION,hmi-kiosk" {
		t.Fatalf("Einträge %v", names)
	}
}

func TestInstallRejectsNewerSystemLevel(t *testing.T) {
	pub, priv := newKeys(t)
	root := t.TempDir()
	in := newInstaller(root, pub)
	in.SystemLevel = func() int { return 1 }
	_, err := in.Install(context.Background(), packLevelWith(t, priv, "2.0.0", 2))
	if err == nil || !strings.Contains(err.Error(), "neuere Systemteile") || !strings.Contains(err.Error(), "Stand 2") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(appDir(root), "2.0.0")); !os.IsNotExist(err) {
		t.Fatalf("Version trotz Ablehnung entpackt: %v", err)
	}
}

func TestInstallAcceptsSameOrOlderSystemLevel(t *testing.T) {
	pub, priv := newKeys(t)
	in := newInstaller(t.TempDir(), pub)
	in.SystemLevel = func() int { return 2 }
	for i, l := range []int{2, 1} {
		v := []string{"2.0.0", "2.1.0"}[i]
		if _, err := in.Install(context.Background(), packLevelWith(t, priv, v, l)); err != nil {
			t.Fatalf("Stand %d: %v", l, err)
		}
	}
	if _, err := in.Install(context.Background(), goodBlob(t, priv, "2.2.0")); err != nil {
		t.Fatalf("Paket ohne Stand: %v", err)
	}
}
