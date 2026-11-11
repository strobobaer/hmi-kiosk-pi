// Package pdhterminal ist das Produktpaket des PDH-Terminals: Kopplung mit dem
// PDH-Server und die zugehörigen Admin-Seiten.
package pdhterminal

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Device ist die Kopplung dieses Terminals an einen PDH-Server. Das Geheimnis
// liegt nur in <data>/pdh/device.json und wird weder angezeigt noch protokolliert.
type Device struct {
	DeviceID, Secret, User, URL string
	PairedAt                    time.Time
}

// String und GoString nennen das Geheimnis nie, auch nicht bei %v, %+v oder %#v.
func (d Device) String() string {
	return fmt.Sprintf("Device{id=%s user=%s url=%s}", d.DeviceID, d.User, d.URL)
}

// GoString siehe String.
func (d Device) GoString() string { return d.String() }

type deviceFile struct {
	DeviceID string    `json:"device_id"`
	Secret   string    `json:"secret"`
	User     string    `json:"user"`
	URL      string    `json:"url"`
	PairedAt time.Time `json:"paired_at"`
}

func devicePath(dataDir string) string { return filepath.Join(dataDir, "pdh", "device.json") }

var errSave = errors.New("Kopplung konnte nicht gespeichert werden")

// LoadDevice liest die Kopplung; fehlt die Datei, ist das Ergebnis nil, nil.
func LoadDevice(dataDir string) (*Device, error) {
	b, err := os.ReadFile(devicePath(dataDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("Kopplungsdatei nicht lesbar")
	}
	var f deviceFile
	if err := json.Unmarshal(b, &f); err != nil || f.DeviceID == "" || f.Secret == "" {
		return nil, errors.New("Kopplungsdatei ist beschädigt")
	}
	return &Device{DeviceID: f.DeviceID, Secret: f.Secret, User: cleanText(f.User), URL: f.URL, PairedAt: f.PairedAt}, nil
}

// SaveDevice schreibt die Kopplung atomar (Ordner 0700, Datei 0600).
func SaveDevice(dataDir string, d Device) error {
	dir := filepath.Dir(devicePath(dataDir))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return errSave
	}
	b, err := json.Marshal(deviceFile{d.DeviceID, d.Secret, d.User, d.URL, d.PairedAt})
	if err != nil {
		return errSave
	}
	tmp, err := os.CreateTemp(dir, ".device-*")
	if err != nil {
		return errSave
	}
	name := tmp.Name()
	done := false
	defer func() {
		if !done {
			_ = os.Remove(name)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return errSave
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return errSave
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return errSave
	}
	if err := tmp.Close(); err != nil {
		return errSave
	}
	if err := os.Rename(name, devicePath(dataDir)); err != nil {
		return errSave
	}
	done = true
	return nil
}

// RemoveDevice löscht die Kopplung lokal; fehlt sie, ist das kein Fehler.
func RemoveDevice(dataDir string) error {
	if err := os.Remove(devicePath(dataDir)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("Kopplung konnte nicht entfernt werden")
	}
	return nil
}
