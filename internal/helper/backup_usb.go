package helper

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// minUSBFree: kleiner als dies lehnt die Sicherung vorab ab (ein Abbild der Karte
// ist auch komprimiert meist mehrere GB groß).
const minUSBFree = 4 << 30

const umountTimeout = 30 * time.Second

// ExecOut startet ein Programm ohne Shell und liefert die Standardausgabe.
type ExecOut func(ctx context.Context, name string, args ...string) (string, error)

var fsTypeRe = regexp.MustCompile(`^[a-z0-9_]{1,16}$`)

// usbFS prüft das Dateisystem und liefert Kerneltyp und Anzeigename.
func usbFS(typ string) (kernel, label string, err error) {
	switch typ {
	case "exfat":
		return "exfat", "exFAT", nil
	case "ext4":
		return "ext4", "ext4", nil
	case "ntfs":
		return "ntfs3", "NTFS", nil
	case "vfat":
		return "", "", errors.New("FAT32 kann keine Dateien über 4 GB speichern. Bitte den Stick mit exFAT formatieren.")
	}
	if !fsTypeRe.MatchString(typ) {
		return "", "", errors.New("Das Dateisystem des USB-Sticks ist nicht erkennbar.")
	}
	return "", "", fmt.Errorf("Das Dateisystem %s wird nicht unterstützt (erlaubt: exFAT, ext4, NTFS).", typ)
}

// usbWriteErr übersetzt Fehler beim Schreiben auf den Stick.
func usbWriteErr(err error) error {
	if errors.Is(err, syscall.ENOSPC) {
		return errors.New("Der USB-Stick ist voll.")
	}
	return fmt.Errorf("Schreiben auf den USB-Stick fehlgeschlagen: %v", err)
}

// backupUSB prüft Name, Stick und Platz, hängt den Stick ein und startet das Schreiben
// im Hintergrund. Fehler bis dahin gehen sofort an den Aufrufer.
func (s *Server) backupUSB(ctx context.Context, name string) (err error) {
	if !validBackupName(name) {
		return errors.New("Terminalname ungültig")
	}
	if err := s.precheckSource(ctx); err != nil {
		return err
	}
	run, err := s.backup.begin(s.baseCtx(), "usb", "USB-Stick")
	if err != nil {
		return err
	}
	started := false
	defer func() {
		if !started {
			run.end(err, "")
		}
	}()
	if s.ExecOut == nil {
		return errors.New("blkid ist nicht verfügbar")
	}

	dev, err := s.findUSB(s.diskForExclusion())
	if err != nil {
		return err
	}
	out, err := s.ExecOut(ctx, "blkid", "-o", "value", "-s", "TYPE", dev)
	if err != nil {
		out = "" // blkid meldet "nichts gefunden" mit Fehlercode
	}
	kernel, label, err := usbFS(strings.TrimSpace(out))
	if err != nil {
		return err
	}
	run.setTarget("USB-Stick (" + label + ")")

	mnt := s.Backup.usbMount()
	if err := ensureDir(mnt); err != nil {
		return fmt.Errorf("Der Einhängeort des USB-Sticks ist nicht nutzbar: %v", err)
	}
	if err := s.exec(ctx, "mount", "-t", kernel, "-o", "nosuid,nodev,noexec", dev, mnt); err != nil {
		err = fmt.Errorf("mount fehlgeschlagen: %v", err)
		if kernel == "ntfs3" {
			err = fmt.Errorf("%v (NTFS braucht den Kerneltreiber ntfs3; sonst den Stick mit exFAT formatieren)", err)
		}
		return err
	}
	// Ab hier muss der Stick wieder ausgehängt werden.
	defer func() {
		if !started {
			s.umount(mnt)
		}
	}()

	free, ferr := s.Backup.free(mnt)
	if ferr != nil {
		return fmt.Errorf("Der freie Platz des USB-Sticks ist nicht lesbar: %v", ferr)
	}
	if s.sourceKind() == kindFiles {
		// Rohdatei und komprimierte Datei liegen zugleich auf dem Stick.
		plan, err := s.planFiles(ctx)
		if err != nil {
			return err
		}
		if need := plan.usbNeed(); free < need {
			return fmt.Errorf("Auf dem USB-Stick ist nicht genug Platz (%s nötig, %s frei).", fmtSize(need), fmtSize(free))
		}
	} else if free < minUSBFree {
		return fmt.Errorf("Zu wenig freier Platz auf dem USB-Stick (%d GB frei, mindestens 4 GB nötig).", free>>30)
	}
	file := "hmikiosk-" + name + "-" + s.Backup.now().Format("2006-01-02-1504") + ".img.gz"
	if _, serr := os.Lstat(filepath.Join(mnt, file)); serr == nil {
		return fmt.Errorf("Die Datei %s existiert bereits auf dem USB-Stick.", file)
	}
	part, perr := os.OpenFile(filepath.Join(mnt, file+".part"), os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o644)
	if perr != nil {
		return usbWriteErr(perr)
	}
	// Die Platte der Datenpartition vor dem Kopieren leeren (/data wird nicht eingefroren).
	if err := s.run(ctx, "sync"); err != nil {
		part.Close()
		os.Remove(part.Name())
		return err
	}

	started = true
	s.bgWG.Add(1)
	go func() {
		defer s.bgWG.Done()
		s.writeUSB(run, part, mnt, file)
	}()
	return nil
}

// diskForExclusion ist das Systemgerät; kann es nicht ermittelt werden (Ersatzquelle), bleibt es leer.
func (s *Server) diskForExclusion() string {
	disk, err := s.bootDisk()
	if err != nil {
		return ""
	}
	return disk
}

// writeUSB kopiert in die .part-Datei, schließt sie ab (fsync, umbenennen, sync) und hängt aus.
// Bei Fehler oder Abbruch verschwindet die .part-Datei.
func (s *Server) writeUSB(run *backupRun, part *os.File, mnt, file string) {
	partPath := part.Name()
	finalPath := filepath.Join(mnt, file)
	err := s.copyFromSource(run, part, filesTarget{dir: mnt, name: strings.TrimSuffix(file, ".gz") + ".part"})
	var se sinkError
	if errors.As(err, &se) {
		err = usbWriteErr(se.err)
	}
	if err == nil {
		if serr := part.Sync(); serr != nil {
			err = usbWriteErr(serr)
		}
	}
	if cerr := part.Close(); err == nil && cerr != nil {
		err = usbWriteErr(cerr)
	}
	written := false
	if err == nil {
		if rerr := os.Rename(partPath, finalPath); rerr != nil {
			err = usbWriteErr(rerr)
		} else {
			written = true
		}
	}
	if err != nil {
		os.Remove(partPath)
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(run.ctx), umountTimeout)
	defer cancel()
	if err == nil {
		if serr := s.run(ctx, "sync"); serr != nil {
			err = serr
		}
	}
	if uerr := s.exec(ctx, "umount", mnt); uerr != nil && err == nil {
		err = fmt.Errorf("Die Sicherung ist geschrieben, aber der Stick ließ sich nicht aushängen: %v", uerr)
	}
	if !written {
		file = ""
	}
	run.end(err, file)
}

func (s *Server) umount(mnt string) {
	ctx, cancel := context.WithTimeout(context.Background(), umountTimeout)
	defer cancel()
	if err := s.exec(ctx, "umount", mnt); err != nil {
		// Der Fehler steht nirgends sonst; der nächste Start hängt erneut ein.
		log.Printf("umount %s: %v", mnt, err)
	}
}

// ensureDir legt den Einhängeort an und lehnt Verweise und Dateien ab.
func ensureDir(p string) error {
	fi, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		return os.MkdirAll(p, 0o700)
	}
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return errors.New("kein Ordner")
	}
	return nil
}

// copyFromSource baut die Quelle (das kann dauern) und kopiert sie komprimiert nach w.
func (s *Server) copyFromSource(run *backupRun, w io.Writer, target filesTarget) error {
	octx, cancel := context.WithTimeout(run.ctx, openTimeout)
	card, size, err := s.openCard(octx, run, target)
	cancel()
	if err != nil {
		return err
	}
	defer card.Close()
	run.setTotal(size)
	run.setPhase(PhaseCompress)
	return s.copyCard(run, card, w)
}
