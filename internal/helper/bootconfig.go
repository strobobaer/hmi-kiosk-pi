package helper

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func isSection(line string) bool {
	return strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]")
}

// isOverlayLine erkennt "dtoverlay=<name>" mit optionalen Parametern.
// Auskommentierte Zeilen beginnen mit "#" und zählen deshalb nicht.
func isOverlayLine(line, overlay string) bool {
	rest, ok := strings.CutPrefix(line, "dtoverlay="+overlay)
	return ok && (rest == "" || rest[0] == ',')
}

// SetOverlay schaltet "dtoverlay=<overlay>" im Abschnitt [all] von config.txt ein
// oder aus. Es arbeitet zeilenweise, ändert nichts außerhalb von [all], ist
// idempotent und gibt LF-Zeilenenden zurück.
//
// Die Namen disable-wifi und disable-bt gelten laut Dokumentation auch für den
// Pi 5; die Bestätigung an echter Hardware gehört zur Selbstprüfungsliste.
func SetOverlay(content, overlay string, enabled bool) string {
	var lines []string
	if content != "" {
		lines = strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	}

	section := ""
	lastAllStart, present := -1, false
	var out []string
	for _, l := range lines {
		l = strings.TrimRight(l, "\r")
		t := strings.TrimSpace(l)
		if isSection(t) {
			section = t
			if t == "[all]" {
				lastAllStart = len(out)
			}
		}
		if section == "[all]" && isOverlayLine(t, overlay) {
			if !enabled {
				continue
			}
			present = true
		}
		out = append(out, l)
	}

	if enabled && !present {
		line := "dtoverlay=" + overlay
		if lastAllStart < 0 {
			if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
				out = append(out, "")
			}
			out = append(out, "[all]", line)
		} else {
			end := len(out)
			for i := lastAllStart + 1; i < len(out); i++ {
				if isSection(strings.TrimSpace(out[i])) {
					end = i
					break
				}
			}
			for end > lastAllStart+1 && strings.TrimSpace(out[end-1]) == "" {
				end--
			}
			out = append(out[:end], append([]string{line}, out[end:]...)...)
		}
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n") + "\n"
}

// writeWithBackup sichert den alten Inhalt als <datei>.bak und ersetzt dann die
// Datei. FAT bietet keine atomaren Garantien, darum zuerst die Sicherung.
func writeWithBackup(path string, old, data []byte) error {
	if err := writeAtomic(path+".bak", old, 0o644); err != nil {
		return fmt.Errorf("Sicherung: %w", err)
	}
	return writeAtomic(path, data, 0o644)
}

// writeAtomic schreibt in dasselbe Verzeichnis, synchronisiert Datei und
// Verzeichnis und benennt dann um.
func writeAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	done := false
	defer func() {
		if !done {
			f.Close()
			os.Remove(tmp)
		}
	}()
	if _, err := f.Write(data); err != nil {
		return err
	}
	_ = f.Chmod(perm) // FAT kennt keine Rechte
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	done = true
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
