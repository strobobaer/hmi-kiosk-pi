// Package update liefert signierte Programmpakete aus: prüfen, entpacken,
// umschalten und bei ausbleibender Gesundmeldung zurückschalten.
//
// Ein Paket ist eine 64-Byte-Ed25519-Signatur gefolgt von einem tar.gz. Signiert
// wird der SHA-256 der tar.gz-Bytes. Der Inhalt besteht nur aus der Dienstdatei
// hmi-kiosk und der Datei VERSION; der Helfer gehört zum Abbild. Den Stand der
// Systemteile, den das Programm braucht, trägt der Eintrag hmi-kiosk als PAX-Datensatz
// HMIKIOSK.system-level: Ältere Terminals lesen nur Name und Art des Eintrags und nehmen
// solche Pakete weiter an. Pakete anderer Produkte (pdh-terminal) heißen im Archiv nach
// ihrer Programmdatei und tragen zusätzlich HMIKIOSK.product; HMI-Kiosk-Pakete schreiben
// diesen Datensatz nicht, fehlt er, gilt hmi-kiosk. Windows-Pakete tragen auf dem Programmeintrag
// zusätzlich HMIKIOSK.platform (windows-amd64) und nennen die Programmdatei <name>.exe; Linux-Pakete
// schreiben den Datensatz nicht und bleiben unverändert. Ältere Geräte kennen den Namen mit .exe
// nicht und lehnen solche Pakete als unzulässige Datei ab.
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
	"strconv"

	"hmikiosk/internal/release"
)

const (
	BinaryName  = "hmi-kiosk"
	versionName = "VERSION"
	maxVersion  = 32

	systemLevelPAX = "HMIKIOSK.system-level"
	maxSystemLevel = 9999
	productPAX     = "HMIKIOSK.product"
	platformPAX    = "HMIKIOSK.platform"

	maxBinaryBytes  = 256 << 20
	maxVersionBytes = 64
	// Obergrenze für das entpackte Archiv, schützt vor Dekompressionsbomben.
	maxArchiveBytes = maxBinaryBytes + 4<<10
)

var (
	versionRe  = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	platformRe = regexp.MustCompile(`^windows-[a-z0-9]+$`)
)

// ValidVersion prüft das Versionsformat (x.y.z, höchstens 32 Zeichen).
func ValidVersion(v string) bool { return len(v) <= maxVersion && versionRe.MatchString(v) }

// Package ist ein geprüftes Paket.
type Package struct {
	Version string
	// SystemLevel ist der Stand der Systemteile, den das Programm braucht (0: keine Angabe).
	SystemLevel int
	// Product ist die Kennung des Produkts (ohne Datensatz im Paket: hmi-kiosk).
	Product string
	// Platform ist die Plattform des Pakets (windows-amd64); leer bei Linux-Paketen.
	Platform string
	// Binary ist der Name der Programmdatei im Archiv (unter Windows mit .exe).
	Binary string
	// BinarySum ist der SHA-256 der Programmdatei im Archiv; der Windows-Starter vergleicht ihn mit der Exe im Versionsordner.
	BinarySum [sha256.Size]byte
	sig       []byte
	targz     []byte
}

// binaryFor liefert den Programmnamen des Produkts mit der Kennung id ("" gilt als hmi-kiosk).
func binaryFor(id string) (string, bool) {
	if id == "" {
		id = release.HMIKiosk.ID
	}
	p, ok := release.ProductByID(id)
	return p.Binary, ok
}

func allBinaries() []string {
	var bs []string
	for _, p := range release.Products() {
		bs = append(bs, p.Binary)
	}
	return bs
}

// verifyBinaries sind die Dateinamen, die Verify im Archiv zulässt: Programmnamen und die
// Windows-Namen mit .exe. Ob der Name zu Produkt und Plattform passt, prüft Verify danach.
func verifyBinaries() []string {
	bs := allBinaries()
	for _, p := range release.Products() {
		bs = append(bs, p.BinaryFor(release.PlatformWindowsAMD64))
	}
	return bs
}

// validPlatform: leer (Linux) oder eine Windows-Kennung.
func validPlatform(s string) bool { return s == "" || platformRe.MatchString(s) }

// Pack baut ein signiertes Linux-Paket aus dir/<p.Binary> für Systemteile ab Stand systemLevel.
func Pack(dir, version string, p release.Product, systemLevel int, priv ed25519.PrivateKey) ([]byte, error) {
	return PackFor(dir, version, p, "", systemLevel, priv)
}

// PackFor wie Pack für die Plattform platform. Leer ergibt das Linux-Paket (byteweise wie bisher);
// unter windows-* liest es dir/<p.Binary>.exe, nennt die Datei im Archiv ebenso und schreibt
// den Datensatz HMIKIOSK.platform.
func PackFor(dir, version string, p release.Product, platform string, systemLevel int, priv ed25519.PrivateKey) ([]byte, error) {
	known, ok := release.ProductByID(p.ID)
	if !ok || known.Binary != p.Binary {
		return nil, errors.New("Produkt unbekannt")
	}
	p = known
	if !validPlatform(platform) {
		return nil, errors.New("Plattform ungültig (erlaubt: windows-amd64 oder leer)")
	}
	if !ValidVersion(version) {
		return nil, errors.New("Versionsangabe ungültig (erwartet x.y.z)")
	}
	if systemLevel < 1 || systemLevel > maxSystemLevel {
		return nil, errors.New("Systemstand ungültig")
	}
	if len(priv) != ed25519.PrivateKeySize {
		return nil, errors.New("Privater Schlüssel ungültig")
	}
	name := p.BinaryFor(platform)
	bin, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return nil, fmt.Errorf("%s nicht lesbar: %v", name, err)
	}
	if len(bin) == 0 || len(bin) > maxBinaryBytes {
		return nil, errors.New("Dienstdatei leer oder zu groß")
	}
	binPAX := map[string]string{systemLevelPAX: strconv.Itoa(systemLevel)}
	if p.ID != release.HMIKiosk.ID {
		binPAX[productPAX] = p.ID
	}
	if platform != "" {
		binPAX[platformPAX] = platform
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct {
		name string
		mode int64
		body []byte
		pax  map[string]string
	}{
		{versionName, 0o644, []byte(version + "\n"), nil},
		{name, 0o755, bin, binPAX},
	} {
		h := &tar.Header{Name: f.name, Typeflag: tar.TypeReg, Mode: f.mode, Size: int64(len(f.body)), PAXRecords: f.pax}
		if f.pax != nil {
			h.Format = tar.FormatPAX
		}
		if err := tw.WriteHeader(h); err != nil {
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
	var version, binary, product, platform string
	var level int
	var binarySum [sha256.Size]byte
	err := walk(targz, verifyBinaries(), func(h *tar.Header, r io.Reader) error {
		if h.Name == versionName {
			v, err := readVersion(r)
			version = v
			return err
		}
		l, err := readSystemLevel(h)
		if err != nil {
			return err
		}
		level = l
		binary = h.Name
		product = release.HMIKiosk.ID
		if id, ok := h.PAXRecords[productPAX]; ok {
			product = id
		}
		platform = h.PAXRecords[platformPAX]
		if !validPlatform(platform) {
			return fmt.Errorf("Plattform im Paket unbekannt: %q", platform)
		}
		hash := sha256.New()
		_, err = io.Copy(hash, r)
		hash.Sum(binarySum[:0])
		return err
	})
	if err != nil {
		return nil, err
	}
	want, ok := binaryFor(product)
	if !ok {
		return nil, fmt.Errorf("Unbekanntes Produkt im Paket: %s", product)
	}
	// Der Name mit .exe gehört nur zu einem Paket mit Windows-Plattform und umgekehrt.
	if platform != "" {
		want += ".exe"
	}
	if binary != want {
		return nil, fmt.Errorf("Programmdatei %q passt nicht zum Produkt %s des Pakets", binary, product)
	}
	return &Package{Version: version, SystemLevel: level, Product: product, Platform: platform, Binary: binary, BinarySum: binarySum, sig: sig, targz: targz}, nil
}

// readSystemLevel liest den Systemstand aus dem PAX-Datensatz; Pakete bis 0.1.6 haben keinen.
func readSystemLevel(h *tar.Header) (int, error) {
	s, ok := h.PAXRecords[systemLevelPAX]
	if !ok {
		return 0, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > maxSystemLevel {
		return 0, errors.New("Systemstand im Paket ungültig")
	}
	return n, nil
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

// walk liest das Archiv streng: genau einmal VERSION und genau einmal eine der Programmdateien
// binaries, beides reguläre Dateien ohne Pfad. Alles andere ist ein Fehler.
func walk(targz []byte, binaries []string, visit func(h *tar.Header, r io.Reader) error) error {
	gz, err := gzip.NewReader(bytes.NewReader(targz))
	if err != nil {
		return errors.New("Paket ist kein gültiges Archiv")
	}
	defer gz.Close()
	tr := tar.NewReader(io.LimitReader(gz, maxArchiveBytes))
	isBinary := func(n string) bool {
		for _, b := range binaries {
			if n == b {
				return true
			}
		}
		return false
	}
	seen := map[string]bool{}
	nBinary := 0
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
		if !isBinary(h.Name) && h.Name != versionName {
			return fmt.Errorf("Archiv enthält unzulässige Datei %q", h.Name)
		}
		if seen[h.Name] {
			return fmt.Errorf("Archiv enthält %q doppelt", h.Name)
		}
		seen[h.Name] = true
		if isBinary(h.Name) {
			nBinary++
			if nBinary > 1 {
				return errors.New("Archiv enthält mehr als eine Programmdatei")
			}
		}
		limit := int64(maxBinaryBytes)
		if h.Name == versionName {
			limit = maxVersionBytes
		}
		if h.Size > limit {
			return fmt.Errorf("%s ist zu groß", h.Name)
		}
		if err := visit(h, tr); err != nil {
			return err
		}
	}
	if nBinary != 1 || !seen[versionName] {
		return errors.New("Archiv unvollständig (Programmdatei oder VERSION fehlt)")
	}
	return nil
}
