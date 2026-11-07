package helper

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// cleanupFilesStale räumt nach einem Absturz der dateiweisen Sicherung auf: Einhängepunkte des neuen Abbilds,
// Loop-Geräte auf unsere Arbeitsdateien, die Dateien selbst und leere Einhängeordner. Es berührt nur eigene
// Namen an eigenen Orten. Ein Ziel, das sich nicht aushängen lässt, wird samt Inhalt in Ruhe gelassen.
func (s *Server) cleanupFilesStale(ctx context.Context) {
	cctx, cancel := context.WithTimeout(ctx, cleanupTimeout)
	defer cancel()
	base := s.Backup.imageRunDir()
	mountDirs := []string{filepath.Join(base, "root"), filepath.Join(base, "boot")}
	busy := false
	for _, d := range mountDirs {
		if !s.mounted(d) {
			continue
		}
		if err := s.exec(cctx, "umount", d); err != nil {
			log.Printf("Aufräumen: umount %s: %v", d, err)
			busy = true
			continue
		}
		log.Printf("Aufräumen: hängengebliebenes Abbild unter %s ausgehängt", d)
	}
	s.detachStaleLoops(cctx)
	if !busy {
		for _, d := range append(mountDirs, base) {
			os.Remove(d) // rmdir: ein nicht leerer Ordner bleibt
		}
	}
	removeCacheFiles(s.Backup.cacheDir())
}

// isOwnRaw: unsere Arbeitsdateien (hmikiosk-*.img auf der Karte, hmikiosk-*.img.part auf dem Stick).
func isOwnRaw(name string) bool {
	return strings.HasPrefix(name, "hmikiosk-") && (strings.HasSuffix(name, ".img") || strings.HasSuffix(name, ".img.part"))
}

// detachStaleLoops löst Loop-Geräte, deren Datei eine Arbeitsdatei in unseren Ordnern ist.
func (s *Server) detachStaleLoops(ctx context.Context) {
	if s.ExecOut == nil {
		return
	}
	out, err := s.ExecOut(ctx, "losetup", "-l", "-n", "-O", "NAME,BACK-FILE")
	if err != nil {
		return
	}
	mine := map[string]bool{filepath.Clean(s.Backup.cacheDir()): true, filepath.Clean(s.Backup.usbMount()): true}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || !loopDevRe.MatchString(f[0]) {
			continue
		}
		file := f[1]
		if !mine[filepath.Dir(file)] || !isOwnRaw(filepath.Base(file)) {
			continue
		}
		if err := s.exec(ctx, "losetup", "-d", f[0]); err != nil {
			log.Printf("Aufräumen: losetup -d %s: %v", f[0], err)
		} else {
			log.Printf("Aufräumen: Loop-Gerät %s gelöst", f[0])
		}
	}
}

// removeCacheFiles löscht unsere Arbeitsdateien im Arbeitsordner der Karte (Verweise ohne ihr Ziel).
func removeCacheFiles(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, en := range entries {
		if !isOwnRaw(en.Name()) || en.IsDir() {
			continue
		}
		if err := os.Remove(filepath.Join(dir, en.Name())); err != nil {
			log.Printf("Aufräumen: %s: %v", en.Name(), err)
		} else {
			log.Printf("Aufräumen: halbe Sicherung %s gelöscht", en.Name())
		}
	}
}
