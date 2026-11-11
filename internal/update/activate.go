package update

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	currentLink  = "current"
	previousLink = "previous"
	pendingFile  = "pending"
)

func appPath(root string) string { return filepath.Join(root, "app") }

// Ops führt Aktivierung und Rückfall unter Root aus (erfüllt helper.UpdateOps).
// Binary ist der Name der Programmdatei des Produkts; leer bedeutet BinaryName (hmi-kiosk).
type Ops struct{ Root, Binary string }

func (o Ops) Activate(version string) error { return ActivateBinary(o.Root, o.Binary, version) }
func (o Ops) Rollback() error               { return RollbackBinary(o.Root, o.Binary) }

// readLink liefert die Version, auf die der Verweis zeigt, oder "" ohne Verweis.
func readLink(root, name string) (string, error) {
	t, err := os.Readlink(filepath.Join(appPath(root), name))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("Verweis %s nicht lesbar: %v", name, err)
	}
	v := filepath.Base(t)
	if !ValidVersion(v) {
		return "", fmt.Errorf("Verweis %s zeigt auf ungültiges Ziel", name)
	}
	return v, nil
}

// setLink ersetzt den Verweis atomar über einen temporären Verweis und Rename.
func setLink(root, name, version string) error {
	dir := appPath(root)
	tmp := filepath.Join(dir, ".link-"+name+"-"+randSuffix())
	if err := os.Symlink(version, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	// CreateTemp legt die Datei mit zufälligem Namen exklusiv an; ein untergeschobener
	// Symlink wird nie verfolgt. Rename ersetzt am Ziel den Eintrag selbst.
	f, err := os.CreateTemp(filepath.Dir(path), ".write-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if err := f.Chmod(mode); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return syncDir(filepath.Dir(path))
}

func hasBinary(root, binary, version string) bool {
	if binary == "" {
		binary = BinaryName
	}
	fi, err := os.Stat(filepath.Join(appPath(root), version, binary))
	return err == nil && fi.Mode().IsRegular()
}

// Activate schaltet current auf version um (previous wird die bisherige Version)
// und legt pending an. pending kommt zuerst: Ein Stromausfall dazwischen lässt den
// Wächter beim nächsten Start aufräumen, statt eine ungeprüfte Version zu hinterlassen.
func Activate(root, version string) error { return ActivateBinary(root, "", version) }

// ActivateBinary wie Activate für ein Produkt mit der Programmdatei binary ("" = hmi-kiosk).
func ActivateBinary(root, binary, version string) error {
	if !ValidVersion(version) {
		return errors.New("Versionsangabe ungültig")
	}
	if !hasBinary(root, binary, version) {
		return fmt.Errorf("Version %s ist nicht installiert", version)
	}
	cur, err := readLink(root, currentLink)
	if err != nil {
		return err
	}
	pending := filepath.Join(appPath(root), pendingFile)
	if err := writeAtomic(pending, []byte(version), 0o644); err != nil {
		return fmt.Errorf("Markierung der Aktivierung nicht schreibbar: %v", err)
	}
	if cur != version {
		if cur != "" {
			if err := setLink(root, previousLink, cur); err != nil {
				os.Remove(pending)
				return fmt.Errorf("Verweis previous nicht setzbar: %v", err)
			}
		}
		if err := setLink(root, currentLink, version); err != nil {
			os.Remove(pending)
			return fmt.Errorf("Verweis current nicht setzbar: %v", err)
		}
	}
	return syncDir(appPath(root))
}

// Rollback schaltet current auf previous zurück und entfernt pending. Steht in
// pending eine Version, auf die current nicht (mehr) zeigt, ist der Rückfall
// schon geschehen und es wird nur pending entfernt.
func Rollback(root string) error { return RollbackBinary(root, "") }

// RollbackBinary wie Rollback für ein Produkt mit der Programmdatei binary ("" = hmi-kiosk).
func RollbackBinary(root, binary string) error {
	cur, err := readLink(root, currentLink)
	if err != nil {
		return err
	}
	pendingPath := filepath.Join(appPath(root), pendingFile)
	pending, _ := readSmall(pendingPath, maxVersionBytes)
	if v := string(pending); ValidVersion(v) && v != cur {
		return removePending(root)
	}
	prev, err := readLink(root, previousLink)
	if err != nil {
		return err
	}
	if prev == "" {
		return errors.New("Kein Vorgänger vorhanden, der Rückfall ist nicht möglich")
	}
	if !hasBinary(root, binary, prev) {
		return fmt.Errorf("Vorgängerversion %s ist unvollständig, der Rückfall ist nicht möglich", prev)
	}
	if cur != prev {
		if err := setLink(root, currentLink, prev); err != nil {
			return fmt.Errorf("Verweis current nicht setzbar: %v", err)
		}
	}
	return removePending(root)
}

func removePending(root string) error {
	if err := os.Remove(filepath.Join(appPath(root), pendingFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncDir(appPath(root))
}

func randSuffix() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
