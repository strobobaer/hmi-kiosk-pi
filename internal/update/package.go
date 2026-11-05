// Package update liefert signierte Programmpakete aus: prüfen, entpacken,
// umschalten und bei ausbleibender Gesundmeldung zurückschalten.
//
// Ein Paket ist eine 64-Byte-Ed25519-Signatur gefolgt von einem tar.gz. Signiert
// wird der SHA-256 der tar.gz-Bytes. Der Inhalt besteht nur aus der Dienstdatei
// hmi-kiosk und der Datei VERSION; der Helfer gehört zum Abbild.
package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

const (
	BinaryName  = "hmi-kiosk"
	versionName = "VERSION"
	maxVersion  = 32

	maxBinaryBytes  = 256 << 20
	maxVersionBytes = 64
	// Obergrenze für das entpackte Archiv, schützt vor Dekompressionsbomben.
	maxArchiveBytes = maxBinaryBytes + 4<<10
)

var versionRe = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// ValidVersion prüft das Versionsformat (x.y.z, höchstens 32 Zeichen).
func ValidVersion(v string) bool { return len(v) <= maxVersion && versionRe.MatchString(v) }

// Package ist ein geprüftes Paket.
type Package struct {
	Version string
	targz   []byte
}

// Pack baut ein signiertes Paket aus dir/hmi-kiosk.
func Pack(dir, version string, priv ed25519.PrivateKey) ([]byte, error) {
	if !ValidVersion(version) {
		return nil, errors.New("Versionsangabe ungültig (erwartet x.y.z)")
	}
	if len(priv) != ed25519.PrivateKeySize {
		return nil, errors.New("Privater Schlüssel ungültig")
	}
	bin, err := os.ReadFile(filepath.Join(dir, BinaryName))
	if err != nil {
		return nil, fmt.Errorf("%s nicht lesbar: %v", BinaryName, err)
	}
	if len(bin) == 0 || len(bin) > maxBinaryBytes {
		return nil, errors.New("Dienstdatei leer oder zu groß")
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct {
		name string
		mode int64
		body []byte
	}{
		{versionName, 0o644, []byte(version + "\n")},
		{BinaryName, 0o755, bin},
	} {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Typeflag: tar.TypeReg, Mode: f.mode, Size: int64(len(f.body))}); err != nil {
			return nil, err
		}
		if _, err := tw.Write(f.body); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(buf.Bytes())
	return append(ed25519.Sign(priv, sum[:]), buf.Bytes()...), nil
}

// Verify prüft Signatur und Inhalt. Fremde Dateinamen, Verzeichnisse, Links und
// ungültige Versionen werden abgelehnt.
func Verify(blob []byte, pub ed25519.PublicKey) (*Package, error) {
	if len(pub) != ed25519.PublicKeySize {
		return nil, errors.New("Es ist kein Schlüssel zur Prüfung hinterlegt, Updates sind gesperrt")
	}
	if len(blob) <= ed25519.SignatureSize {
		return nil, errors.New("Update-Datei zu kurz")
	}
	sig, targz := blob[:ed25519.SignatureSize], blob[ed25519.SignatureSize:]
	sum := sha256.Sum256(targz)
	if !ed25519.Verify(pub, sum[:], sig) {
		return nil, errors.New("Signatur ungültig, das Paket stammt nicht aus der Freigabe")
	}
	var version string
	err := walk(targz, func(name string, r io.Reader) error {
		if name == versionName {
			v, err := readVersion(r)
			version = v
			return err
		}
		_, err := io.Copy(io.Discard, r)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &Package{Version: version, targz: targz}, nil
}

func readVersion(r io.Reader) (string, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxVersionBytes+1))
	if err != nil {
		return "", err
	}
	if len(b) > maxVersionBytes {
		return "", errors.New("VERSION zu lang")
	}
	v := string(bytes.TrimSuffix(b, []byte("\n")))
	if !ValidVersion(v) {
		return "", errors.New("VERSION im Paket ungültig")
	}
	return v, nil
}

// walk liest das Archiv streng: genau je einmal hmi-kiosk und VERSION, beides
// reguläre Dateien ohne Pfad. Alles andere ist ein Fehler.
func walk(targz []byte, visit func(name string, r io.Reader) error) error {
	gz, err := gzip.NewReader(bytes.NewReader(targz))
	if err != nil {
		return errors.New("Paket ist kein gültiges Archiv")
	}
	defer gz.Close()
	tr := tar.NewReader(io.LimitReader(gz, maxArchiveBytes))
	seen := map[string]bool{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("Archiv beschädigt: %v", err)
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA {
			return fmt.Errorf("Archiv enthält unzulässigen Eintrag %q (Verzeichnis oder Link)", h.Name)
		}
		if h.Name != BinaryName && h.Name != versionName {
			return fmt.Errorf("Archiv enthält unzulässige Datei %q", h.Name)
		}
		if seen[h.Name] {
			return fmt.Errorf("Archiv enthält %q doppelt", h.Name)
		}
		seen[h.Name] = true
		limit := int64(maxBinaryBytes)
		if h.Name == versionName {
			limit = maxVersionBytes
		}
		if h.Size > limit {
			return fmt.Errorf("%s ist zu groß", h.Name)
		}
		if err := visit(h.Name, tr); err != nil {
			return err
		}
	}
	if !seen[BinaryName] || !seen[versionName] {
		return errors.New("Archiv unvollständig (hmi-kiosk oder VERSION fehlt)")
	}
	return nil
}
