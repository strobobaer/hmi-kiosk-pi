package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type entry struct {
	name     string
	body     string
	typeflag byte
	link     string
	mode     int64
}

func tarGz(t *testing.T, es []entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range es {
		tf := e.typeflag
		if tf == 0 {
			tf = tar.TypeReg
		}
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		h := &tar.Header{Name: e.name, Typeflag: tf, Mode: mode, Linkname: e.link}
		if tf == tar.TypeReg {
			h.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if tf == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func sign(priv ed25519.PrivateKey, targz []byte) []byte {
	sum := sha256.Sum256(targz)
	return append(ed25519.Sign(priv, sum[:]), targz...)
}

func newKeys(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func goodBlob(t *testing.T, priv ed25519.PrivateKey, version string) []byte {
	t.Helper()
	return sign(priv, tarGz(t, []entry{
		{name: "VERSION", body: version + "\n"},
		{name: "hmi-kiosk", body: "#!binary " + version, mode: 0o755},
	}))
}

func TestPackVerifyRoundTrip(t *testing.T) {
	pub, priv := newKeys(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hmi-kiosk"), []byte("BIN"), 0o755); err != nil {
		t.Fatal(err)
	}
	blob, err := Pack(dir, "1.2.3", 1, priv)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Verify(blob, pub)
	if err != nil {
		t.Fatal(err)
	}
	if p.Version != "1.2.3" {
		t.Fatalf("Version %q", p.Version)
	}
}

func TestPackRejectsBadVersion(t *testing.T) {
	_, priv := newKeys(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "hmi-kiosk"), []byte("BIN"), 0o755)
	for _, v := range []string{"", "1.2", "v1.2.3", "../1.2.3", "1.2.3\n", strings.Repeat("1", 40) + ".0.0"} {
		if _, err := Pack(dir, v, 1, priv); err == nil {
			t.Errorf("Version %q angenommen", v)
		}
	}
	if _, err := Pack(t.TempDir(), "1.0.0", 1, priv); err == nil {
		t.Error("fehlende Binärdatei angenommen")
	}
}

func TestVerifyRejectsBadSignatureAndTamper(t *testing.T) {
	pub, priv := newKeys(t)
	otherPub, _ := newKeys(t)
	blob := goodBlob(t, priv, "1.0.0")
	if _, err := Verify(blob, pub); err != nil {
		t.Fatalf("gültiges Paket abgelehnt: %v", err)
	}
	if _, err := Verify(blob, otherPub); err == nil {
		t.Error("fremder Schlüssel angenommen")
	}
	tampered := append([]byte(nil), blob...)
	tampered[len(tampered)-12] ^= 0xff
	if _, err := Verify(tampered, pub); err == nil {
		t.Error("manipulierte Nutzdaten angenommen")
	}
	badSig := append([]byte(nil), blob...)
	badSig[3] ^= 0x01
	if _, err := Verify(badSig, pub); err == nil {
		t.Error("manipulierte Signatur angenommen")
	}
	for _, short := range [][]byte{nil, blob[:10], blob[:64]} {
		if _, err := Verify(short, pub); err == nil {
			t.Errorf("zu kurzes Paket (%d) angenommen", len(short))
		}
	}
	if _, err := Verify(blob, nil); err == nil {
		t.Error("leerer Schlüssel angenommen")
	}
	if _, err := Verify(blob, ed25519.PublicKey{1, 2, 3}); err == nil {
		t.Error("kurzer Schlüssel angenommen")
	}
}

func TestVerifyRejectsForeignContent(t *testing.T) {
	pub, priv := newKeys(t)
	ver := entry{name: "VERSION", body: "1.0.0\n"}
	bin := entry{name: "hmi-kiosk", body: "x", mode: 0o755}
	cases := map[string][]entry{
		"hmi-kiosk-helper":    {ver, bin, {name: "hmi-kiosk-helper", body: "x"}},
		"Traversal":           {ver, bin, {name: "../../etc/passwd", body: "x"}},
		"Unterverzeichnis":    {ver, {name: "bin/hmi-kiosk", body: "x"}},
		"Punkt-Präfix":        {ver, {name: "./hmi-kiosk", body: "x"}},
		"Symlink":             {ver, bin, {name: "evil", typeflag: tar.TypeSymlink, link: "/etc/passwd"}},
		"Symlink als Binary":  {ver, {name: "hmi-kiosk", typeflag: tar.TypeSymlink, link: "/bin/sh"}},
		"Hardlink":            {ver, {name: "hmi-kiosk", typeflag: tar.TypeLink, link: "/etc/passwd"}},
		"Verzeichnis":         {ver, bin, {name: "dir/", typeflag: tar.TypeDir}},
		"Doppelt":             {ver, bin, bin},
		"ohne VERSION":        {bin},
		"ohne Binary":         {ver},
		"VERSION ungültig":    {{name: "VERSION", body: "latest\n"}, bin},
		"VERSION mit Anhang":  {{name: "VERSION", body: "1.0.0\n../x"}, bin},
		"VERSION zu lang":     {{name: "VERSION", body: strings.Repeat("1", 40) + ".0.0"}, bin},
		"VERSION leere Datei": {{name: "VERSION", body: ""}, bin},
	}
	for name, es := range cases {
		if _, err := Verify(sign(priv, tarGz(t, es)), pub); err == nil {
			t.Errorf("%s: angenommen", name)
		}
	}
	// Kein gzip.
	if _, err := Verify(sign(priv, []byte("kein gzip, aber signiert")), pub); err == nil {
		t.Error("Nicht-gzip angenommen")
	}
}
