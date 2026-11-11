package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"hmikiosk/internal/release"
)

// productFile liegt relativ zur Systemwurzel; install.sh und das Abbild schreiben die Produkt-ID hinein.
const productFile = "etc/hmi-kiosk/product"

// productBinary liefert den Programmnamen des installierten Produkts. Fehlt die Datei oder ist sie
// leer, gilt hmi-kiosk (Geräte vor der Produktlinie); eine unbekannte Kennung ist ein Fehler.
func productBinary(sysRoot string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(sysRoot, productFile))
	if errors.Is(err, os.ErrNotExist) {
		return release.HMIKiosk.Binary, nil
	}
	if err != nil {
		return "", fmt.Errorf("Produktdatei nicht lesbar: %v", err)
	}
	id := strings.TrimSpace(string(raw))
	if id == "" {
		return release.HMIKiosk.Binary, nil
	}
	p, ok := release.ProductByID(id)
	if !ok {
		return "", fmt.Errorf("Unbekanntes Produkt %q in /%s", id, productFile)
	}
	return p.Binary, nil
}
