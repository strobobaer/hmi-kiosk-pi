package helper

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	devNameRe = regexp.MustCompile(`^[A-Za-z0-9]+$`)
	usbDiskRe = regexp.MustCompile(`^sd[a-z]+$`)
)

// bootDisk liefert den Namen des Blockgeräts (z. B. mmcblk0), auf dem /boot/firmware liegt:
// aus /proc/self/mountinfo die Partition, über /sys/class/block deren Elterngerät.
func (s *Server) bootDisk() (string, error) {
	raw, err := os.ReadFile(s.Backup.mountInfo())
	if err != nil {
		return "", errors.New("Das Systemlaufwerk ließ sich nicht ermitteln: " + err.Error())
	}
	source := ""
	for _, line := range strings.Split(string(raw), "\n") {
		left, right, ok := strings.Cut(line, " - ")
		if !ok {
			continue
		}
		lf, rf := strings.Fields(left), strings.Fields(right)
		if len(lf) >= 5 && lf[4] == s.Backup.bootMount() && len(rf) >= 2 {
			source = rf[1] // der zuletzt eingehängte Eintrag gilt
		}
	}
	name, ok := strings.CutPrefix(source, "/dev/")
	if !ok || !devNameRe.MatchString(name) {
		return "", errors.New("Das Systemlaufwerk ist nicht erkennbar.")
	}
	return s.parentDisk(name)
}

// parentDisk löst eine Partition zu ihrem Gerät auf. Ein Gerät ohne Partitionstabelle gilt als sich selbst.
func (s *Server) parentDisk(name string) (string, error) {
	real, err := filepath.EvalSymlinks(filepath.Join(s.Backup.sysClass(), name))
	if err != nil {
		return "", errors.New("Das Systemlaufwerk ist in /sys nicht zu finden.")
	}
	if _, err := os.Stat(filepath.Join(real, "partition")); err == nil {
		real = filepath.Dir(real)
	}
	disk := filepath.Base(real)
	if !devNameRe.MatchString(disk) {
		return "", errors.New("Das Systemlaufwerk ist nicht erkennbar.")
	}
	return disk, nil
}

// systemDevNums liefert die Gerätenummern (major:minor), auf denen /, der Bootordner, /data oder die
// untere Overlay-Ebene (/media/root-ro) eingehängt sind. ok ist falsch, wenn mountinfo nicht lesbar ist.
func (s *Server) systemDevNums() (map[string]bool, bool) {
	raw, err := os.ReadFile(s.Backup.mountInfo())
	if err != nil {
		return nil, false
	}
	set := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		lf := strings.Fields(line)
		if len(lf) < 5 {
			continue
		}
		mp := lf[4]
		if mp == "/" || mp == s.Backup.bootMount() || mp == "/data" || mp == "/media/root-ro" || strings.HasPrefix(mp, "/media/root-ro/") {
			set[lf[2]] = true
		}
	}
	return set, true
}

// diskHoldsSystem sagt, ob das Gerät oder eine seiner Partitionen eine der Gerätenummern trägt.
func (s *Server) diskHoldsSystem(realDir, disk string, nums map[string]bool) bool {
	names := []string{disk}
	if entries, err := os.ReadDir(realDir); err == nil {
		for _, en := range entries {
			if strings.HasPrefix(en.Name(), disk) {
				names = append(names, en.Name())
			}
		}
	}
	for _, n := range names {
		if b, err := os.ReadFile(filepath.Join(s.Backup.sysClass(), n, "dev")); err == nil && nums[strings.TrimSpace(string(b))] {
			return true
		}
	}
	return false
}

// findUSB sucht genau einen USB-Speicher (sdX, dessen /sys-Pfad über USB läuft, nicht das
// Systemgerät system und kein Laufwerk, das /, /boot/firmware, /data oder die Overlay-Unterlage trägt) und liefert dessen erste Partition, sonst das Gerät selbst.
func (s *Server) findUSB(system string) (string, error) {
	nums, mountsOK := s.systemDevNums()
	if system == "" && (!mountsOK || len(nums) == 0) {
		return "", errors.New("Das Systemlaufwerk ließ sich nicht ermitteln. Deshalb wird kein USB-Laufwerk als Ziel angeboten.")
	}
	sysBlock := s.Backup.sysBlock()
	entries, err := os.ReadDir(sysBlock)
	if err != nil {
		return "", errors.New("Die Geräteliste ist nicht lesbar: " + err.Error())
	}
	sysRoot := filepath.Dir(sysBlock)
	var found []string
	for _, en := range entries {
		name := en.Name()
		if name == system || !usbDiskRe.MatchString(name) {
			continue
		}
		real, err := filepath.EvalSymlinks(filepath.Join(sysBlock, name))
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(sysRoot, real)
		if err != nil || !strings.Contains("/"+filepath.ToSlash(rel), "/usb") {
			continue
		}
		if s.diskHoldsSystem(real, name, nums) {
			continue
		}
		found = append(found, filepath.Join(s.Backup.devDir(), firstPartition(real, name)))
	}
	sort.Strings(found)
	switch len(found) {
	case 0:
		return "", errors.New("Kein USB-Stick gefunden. Bitte einen Stick einstecken.")
	case 1:
		return found[0], nil
	}
	return "", errors.New("Mehrere USB-Speicher gefunden. Bitte nur einen Stick einstecken.")
}

// firstPartition gibt den Namen der Partition 1 unter dem Gerät zurück, sonst den des Geräts.
func firstPartition(realDir, disk string) string {
	entries, err := os.ReadDir(realDir)
	if err != nil {
		return disk
	}
	for _, en := range entries {
		n := en.Name()
		if !strings.HasPrefix(n, disk) || !devNameRe.MatchString(n) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(realDir, n, "partition"))
		if err == nil && strings.TrimSpace(string(b)) == "1" {
			return n
		}
	}
	return disk
}
