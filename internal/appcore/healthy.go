package appcore

import (
	"os"
	"path/filepath"
)

// HealthyFile ist die Gesundmarke im Laufzeitordner; das Update-Wächter-Programm liest sie.
const HealthyFile = "healthy"

// WriteHealthy schreibt die Gesundmarke (Inhalt: Version) atomar nach runDir.
func WriteHealthy(runDir, version string) error {
	return WriteAtomic(filepath.Join(runDir, HealthyFile), []byte(version), 0o644)
}

// WriteAtomic schreibt über eine temporäre Datei, damit Leser nie einen halben
// Inhalt sehen und ein vorhandener Verweis ersetzt statt verfolgt wird.
func WriteAtomic(path string, data []byte, perm os.FileMode) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Chmod(perm); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
