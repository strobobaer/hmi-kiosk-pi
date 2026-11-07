package helper

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

var partuuidRe = regexp.MustCompile(`(?i)PARTUUID=("?)([0-9a-f]{8})-([0-9a-f]{2})`)

// rewriteIDs ersetzt PARTUUID=<alt>-NN durch <neu>-NN. Andere Formen (LABEL=, UUID=, Gerätenamen) bleiben.
// Eine PARTUUID mit fremder Basis lässt sich nicht abbilden; strict meldet sie als Fehler, sonst bleibt sie.
func rewriteIDs(text string, oldID, newID uint32, strict bool) (out string, n int, err error) {
	out = partuuidRe.ReplaceAllStringFunc(text, func(m string) string {
		sub := partuuidRe.FindStringSubmatch(m)
		base, perr := strconv.ParseUint(sub[2], 16, 32)
		if perr != nil || uint32(base) != oldID {
			if err == nil && strict {
				err = fmt.Errorf("verweist auf die fremde Partitions-Kennung %s", strings.ToLower(sub[2]))
			}
			return m
		}
		n++
		return fmt.Sprintf("PARTUUID=%s%08x-%s", sub[1], newID, sub[3])
	})
	if err != nil {
		return text, 0, err
	}
	return out, n, nil
}

// rewritePartuuid ersetzt die alte Disk-Kennung in cmdline.txt-artigem Text; eine fremde Kennung ist ein Fehler.
func rewritePartuuid(text string, oldID, newID uint32) (string, int, error) {
	return rewriteIDs(text, oldID, newID, true)
}

// safeRegular liefert den Pfad rel unter root, wenn jeder Teil ein echter Ordner und der letzte eine
// gewöhnliche Datei ist. Verweise werden nie verfolgt: ein Verweis im kopierten System würde sonst auf Dateien
// des laufenden Systems zeigen.
func safeRegular(root string, rel ...string) (string, error) {
	p := root
	for i, c := range rel {
		p = filepath.Join(p, c)
		fi, err := os.Lstat(p)
		if err != nil {
			return p, err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return p, fmt.Errorf("%s ist ein Verweis", filepath.Join(rel[:i+1]...))
		}
		if last := i == len(rel)-1; (last && !fi.Mode().IsRegular()) || (!last && !fi.IsDir()) {
			return p, fmt.Errorf("%s hat nicht die erwartete Art", filepath.Join(rel[:i+1]...))
		}
	}
	return p, nil
}

// replaceFile ersetzt eine Datei über eine neue Datei im selben Ordner.
func replaceFile(path string, data []byte) error {
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".neu")
	os.Remove(tmp)
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o644)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if serr := f.Sync(); err == nil {
		err = serr
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

// adjustReferences passt im neuen Abbild die Verweise auf die Disk-ID an: root=PARTUUID in cmdline.txt und
// PARTUUID=-Einträge in fstab. LABEL= und UUID= bleiben, weil die neuen Dateisysteme dieselben tragen.
func adjustReferences(bootDir, rootDir string, oldID, newID uint32) error {
	cmd, err := safeRegular(bootDir, "cmdline.txt")
	if err != nil {
		return fmt.Errorf("Die Datei cmdline.txt im Abbild ist nicht nutzbar: %v", err)
	}
	b, err := os.ReadFile(cmd)
	if err != nil {
		return fmt.Errorf("cmdline.txt ließ sich nicht lesen: %v", err)
	}
	out, n, err := rewritePartuuid(string(b), oldID, newID)
	if err != nil {
		return fmt.Errorf("cmdline.txt %v; das Abbild würde nicht starten.", err)
	}
	if n > 0 {
		if err := replaceFile(cmd, []byte(out)); err != nil {
			return fmt.Errorf("cmdline.txt ließ sich nicht schreiben: %v", err)
		}
	}

	fstab, err := safeRegular(rootDir, "etc", "fstab")
	if errors.Is(err, os.ErrNotExist) {
		return nil // ohne fstab gibt es nichts anzupassen
	}
	if err != nil {
		return fmt.Errorf("Die Datei /etc/fstab im Abbild ist nicht nutzbar: %v", err)
	}
	if b, err = os.ReadFile(fstab); err != nil {
		return fmt.Errorf("/etc/fstab ließ sich nicht lesen: %v", err)
	}
	if out, n, _ = rewriteIDs(string(b), oldID, newID, false); n > 0 {
		if err := replaceFile(fstab, []byte(out)); err != nil {
			return fmt.Errorf("/etc/fstab ließ sich nicht schreiben: %v", err)
		}
	}
	return nil
}
