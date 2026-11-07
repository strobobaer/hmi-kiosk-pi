package helper

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// CleanupStale räumt nach einem Absturz auf: ein hängengebliebener USB-Mount samt .part-Datei
// und Arbeitsordner der kompakten Sicherung unter /run. Fehler werden nur protokolliert.
func (s *Server) CleanupStale(ctx context.Context) {
	mnt := s.Backup.usbMount()
	removeParts(mnt)
	if s.mounted(mnt) {
		cctx, cancel := context.WithTimeout(ctx, umountTimeout)
		defer cancel()
		if err := s.exec(cctx, "umount", mnt); err != nil {
			log.Printf("Aufräumen: umount %s: %v", mnt, err)
		} else {
			log.Printf("Aufräumen: hängengebliebenen Stick unter %s ausgehängt", mnt)
		}
	}
	dir := s.Backup.runDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, en := range entries {
		if !strings.HasPrefix(en.Name(), tempPrefix) {
			continue
		}
		// RemoveAll folgt keinen Verweisen.
		if err := os.RemoveAll(filepath.Join(dir, en.Name())); err != nil {
			log.Printf("Aufräumen: %s: %v", en.Name(), err)
		} else {
			log.Printf("Aufräumen: Arbeitsordner %s entfernt", en.Name())
		}
	}
}

// removeParts löscht .part-Einträge (Dateien oder Verweise, nie deren Ziele) im Ordner dir.
func removeParts(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, en := range entries {
		if !strings.HasSuffix(en.Name(), ".part") || en.IsDir() {
			continue
		}
		if err := os.Remove(filepath.Join(dir, en.Name())); err != nil {
			log.Printf("Aufräumen: %s: %v", en.Name(), err)
		} else {
			log.Printf("Aufräumen: halbe Sicherung %s gelöscht", en.Name())
		}
	}
}

// mounted sagt, ob unter /proc/self/mountinfo ein Dateisystem auf dem Pfad liegt.
func (s *Server) mounted(path string) bool {
	raw, err := os.ReadFile(s.Backup.mountInfo())
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if f := strings.Fields(line); len(f) >= 5 && f[4] == path {
			return true
		}
	}
	return false
}
