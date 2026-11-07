package helper

import (
	"context"
	"errors"
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

// isOwnPart: nur unsere halben Sicherungen (hmikiosk-*.img.gz.part), nie fremde Dateien auf dem Stick.
func isOwnPart(name string) bool {
	return strings.HasPrefix(name, "hmikiosk-") && strings.HasSuffix(name, ".img.gz.part")
}

// removeParts löscht .part-Einträge (Dateien oder Verweise, nie deren Ziele) im Ordner dir.
func removeParts(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, en := range entries {
		if !isOwnPart(en.Name()) || en.IsDir() {
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

const errWritable = "Der Schreibschutz ist ausgeschaltet. Eine SD-Sicherung ist nur mit eingeschaltetem Schreibschutz möglich, sonst wäre die Kopie unbrauchbar."

// checkReadOnly lehnt ab, wenn Partition 1 oder 2 der Karte irgendwo schreibbar eingehängt ist
// (Schreibschutz aus): Dann wäre die Rohkopie nicht stimmig. Unter dem Overlay ist die untere
// Ebene der Wurzel nur lesbar eingehängt. Verglichen wird die Gerätenummer (major:minor) aus
// mountinfo, nicht der Quellname: die Wurzel erscheint auf dem Pi auch als /dev/root.
func (s *Server) checkReadOnly(devPath string) error {
	base := filepath.Base(devPath)
	sep := ""
	if base != "" && base[len(base)-1] >= '0' && base[len(base)-1] <= '9' {
		sep = "p"
	}
	want := map[string]bool{}
	for _, n := range []string{"1", "2"} {
		b, err := os.ReadFile(filepath.Join(s.Backup.sysClass(), base+sep+n, "dev"))
		if err != nil {
			return errors.New("Die Partitionen der SD-Karte ließen sich nicht prüfen: " + err.Error())
		}
		want[strings.TrimSpace(string(b))] = true
	}
	raw, err := os.ReadFile(s.Backup.mountInfo())
	if err != nil {
		return errors.New("Die Einhängepunkte der SD-Karte ließen sich nicht prüfen: " + err.Error())
	}
	for _, line := range strings.Split(string(raw), "\n") {
		lf := strings.Fields(line)
		if len(lf) < 6 || !want[lf[2]] {
			continue
		}
		for _, o := range strings.Split(lf[5], ",") {
			if o == "rw" {
				return errors.New(errWritable)
			}
		}
	}
	return nil
}
