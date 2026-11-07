package helper

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// filesTarget sagt, wo die Arbeitsdatei liegt: dir/name. Im Arbeitsordner der Karte (Download) legt der Helfer
// den Ordner root-eigen an; auf dem Stick (USB) ist dir der Einhängepunkt.
type filesTarget struct{ dir, name string }

// copyPollEvery ist der Takt, in dem der Fortschritt der Kopie aus dem Ziel gelesen wird.
var copyPollEvery = time.Second

const cleanupTimeout = 3 * time.Minute

// unwind führt beim Zurückbauen die Schritte in umgekehrter Reihenfolge aus, auch wenn der Auftrag
// schon abgebrochen ist. Ein Schritt läuft höchstens einmal.
type unwind struct {
	steps []func(ctx context.Context) error
}

func (u *unwind) add(f func(ctx context.Context) error) { u.steps = append(u.steps, f) }

// run baut zurück, bis noch keep Schritte übrig sind, und meldet den ersten Fehler.
func (u *unwind) run(parent context.Context, keep int) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), cleanupTimeout)
	defer cancel()
	var first error
	for len(u.steps) > keep {
		f := u.steps[len(u.steps)-1]
		u.steps = u.steps[:len(u.steps)-1]
		if err := f(ctx); err != nil {
			log.Printf("Aufräumen der Sicherung: %v", err)
			if first == nil {
				first = err
			}
		}
	}
	return first
}

// ensureWorkDir legt den Arbeitsordner der Karte an: ein echter Ordner des Helfer-Benutzers (root), nur für ihn
// zugänglich. Ein Verweis, eine Datei oder ein fremder Besitzer wird abgelehnt, nie verfolgt.
func (s *Server) ensureWorkDir(dir string) (string, error) {
	return ensurePrivateDir(dir, "Der Arbeitsordner der Sicherung")
}

func ensurePrivateDir(dir, what string) (string, error) {
	fi, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(dir, 0o700); err != nil {
			return "", fmt.Errorf("%s ließ sich nicht anlegen: %v", what, err)
		}
		fi, err = os.Lstat(dir)
	}
	if err != nil {
		return "", fmt.Errorf("%s ist nicht lesbar: %v", what, err)
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("%s (%s) ist kein Ordner oder ein Verweis.", what, dir)
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != os.Geteuid() {
		return "", fmt.Errorf("%s (%s) gehört nicht dem Helfer.", what, dir)
	}
	if fi.Mode().Perm() != 0o700 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return "", fmt.Errorf("%s ließ sich nicht absichern: %v", what, err)
		}
	}
	return dir, nil
}

func randomDiskID(not uint32) (uint32, error) {
	for range 8 {
		var b [4]byte
		if _, err := rand.Read(b[:]); err != nil {
			return 0, err
		}
		if id := binary.LittleEndian.Uint32(b[:]); id != 0 && id != not {
			return id, nil
		}
	}
	return 0, errors.New("Keine zufällige Disk-ID erzeugbar.")
}

// step startet ein Programm; bei Abbruch kommt der Abbruchgrund zurück, sonst eine deutsche Meldung.
func (s *Server) step(ctx context.Context, what, name string, args ...string) error {
	err := s.exec(ctx, name, args...)
	if err == nil {
		return nil
	}
	if cerr := ctx.Err(); cerr != nil {
		return cerr
	}
	return fmt.Errorf("%s: %v", what, err)
}

func rsyncOK(err error) bool {
	var ee interface{ ExitCode() int }
	// 24: Dateien verschwanden während des Kopierens (im laufenden Betrieb üblich).
	return errors.As(err, &ee) && ee.ExitCode() == 24
}

func sourceDir(dir string) string { return strings.TrimRight(dir, "/") + "/" }

// openFiles baut das Abbild in der Arbeitsdatei: Partitionstabelle, Loop-Gerät, Dateisysteme, Kopie der
// Dateien, angepasste Verweise. Danach ist alles ausgehängt; zurück kommt ein Strom über die Arbeitsdatei,
// deren Schließen sie löscht. Bei jedem Fehler und bei Abbruch ist alles zurückgebaut.
func (s *Server) openFiles(ctx context.Context, run *backupRun, tgt filesTarget) (rc io.ReadCloser, size int64, err error) {
	plan, err := s.planFiles(ctx)
	if err != nil {
		return nil, 0, err
	}
	dir := tgt.dir
	if dir == s.Backup.cacheDir() {
		if dir, err = s.ensureWorkDir(dir); err != nil {
			return nil, 0, err
		}
		free, ferr := s.Backup.free(dir)
		if ferr != nil {
			return nil, 0, fmt.Errorf("Der freie Platz auf dem Datenträger ist nicht lesbar: %v", ferr)
		}
		if msg := plan.cacheProblem(free); msg != "" {
			return nil, 0, errors.New(msg)
		}
	} else {
		free, ferr := s.Backup.free(dir)
		if ferr != nil {
			return nil, 0, fmt.Errorf("Der freie Platz des USB-Sticks ist nicht lesbar: %v", ferr)
		}
		if need := plan.usbNeed(); free < need {
			return nil, 0, fmt.Errorf("Auf dem USB-Stick ist nicht genug Platz (%s nötig, %s frei).", fmtSize(need), fmtSize(free))
		}
	}
	runDir, err := ensurePrivateDir(s.Backup.imageRunDir(), "Der Einhängeordner der Sicherung")
	if err != nil {
		return nil, 0, err
	}
	newID, err := randomDiskID(plan.oldID)
	if err != nil {
		return nil, 0, err
	}

	run.setPhase(PhasePrepare)
	var uw unwind
	defer func() {
		if err != nil {
			uw.run(ctx, 0)
		}
	}()

	path := filepath.Join(dir, tgt.name)
	if fi, lerr := os.Lstat(path); lerr == nil && !fi.IsDir() {
		os.Remove(path) // Rest eines früheren Laufs unter unserem Namen
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, 0, fmt.Errorf("Die Arbeitsdatei der Sicherung ließ sich nicht anlegen: %v", err)
	}
	detachFailed := false
	uw.add(func(context.Context) error {
		if detachFailed {
			return fmt.Errorf("Die Arbeitsdatei %s bleibt bestehen, weil ihr Loop-Gerät nicht gelöst werden konnte", path)
		}
		if rerr := os.Remove(path); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
			return rerr
		}
		return nil
	})
	// Ein losetup, das nach dem Anhängen scheitert oder beendet wird, liefert den Gerätenamen nicht mehr: die Datei
	// nennt es. Erst wenn kein Gerät mehr an ihr hängt, darf sie gelöscht werden.
	uw.add(func(c context.Context) error {
		if derr := s.detachLoopsOf(c, path); derr != nil {
			detachFailed = true
			return derr
		}
		return nil
	})
	if err = f.Truncate(plan.rawSize); err == nil {
		_, err = f.WriteAt(filesMBR(plan.sector0, newID, filesLeadSectors, plan.bootSectors, plan.p2Start, plan.sysBytes/sectorSize), 0)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, 0, fmt.Errorf("Die Arbeitsdatei der Sicherung ließ sich nicht beschreiben: %v", usbOrPlain(err))
	}

	out, err := s.ExecOut(ctx, "losetup", "-P", "--show", "-f", path)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return nil, 0, cerr
		}
		return nil, 0, fmt.Errorf("Das Loop-Gerät ließ sich nicht einrichten: %v", err)
	}
	loop := strings.TrimSpace(out)
	if !loopDevRe.MatchString(loop) {
		return nil, 0, fmt.Errorf("Unerwartete Antwort von losetup: %q", loop)
	}
	uw.add(func(c context.Context) error {
		if derr := s.exec(c, "losetup", "-d", loop); derr != nil {
			return fmt.Errorf("losetup -d %s: %v", loop, derr)
		}
		return nil
	})
	p1 := filepath.Join(s.Backup.devDir(), filepath.Base(loop)+"p1")
	p2 := filepath.Join(s.Backup.devDir(), filepath.Base(loop)+"p2")
	if err = waitNodes(ctx, p1, p2); err != nil {
		return nil, 0, err
	}

	if err = s.formatFiles(ctx, plan, p1, p2); err != nil {
		return nil, 0, err
	}

	bootMnt, rootMnt := filepath.Join(runDir, "boot"), filepath.Join(runDir, "root")
	for _, d := range []string{bootMnt, rootMnt} {
		if merr := os.Mkdir(d, 0o700); merr != nil && !errors.Is(merr, os.ErrExist) {
			err = fmt.Errorf("Der Einhängepunkt %s ließ sich nicht anlegen: %v", d, merr)
			return nil, 0, err
		}
		uw.add(func(context.Context) error {
			if rerr := os.Remove(d); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
				return rerr
			}
			return nil
		})
	}
	for _, m := range []struct{ typ, dev, dir string }{{"vfat", p1, bootMnt}, {"ext4", p2, rootMnt}} {
		if err = s.step(ctx, "Das neue Dateisystem ließ sich nicht einhängen", "mount", "-t", m.typ, "-o", "nodev,nosuid,noexec", m.dev, m.dir); err != nil {
			return nil, 0, err
		}
		uw.add(func(c context.Context) error {
			if uerr := s.exec(c, "umount", m.dir); uerr != nil {
				return fmt.Errorf("umount %s: %v", m.dir, uerr)
			}
			return nil
		})
	}

	run.setPhase(PhaseCopy)
	run.setTotal(plan.copyBytes)
	stop := s.pollCopy(ctx, run, plan.copyBytes, bootMnt, rootMnt)
	err = s.copyFiles(ctx, bootMnt, rootMnt)
	stop()
	if err != nil {
		return nil, 0, err
	}
	if err = makeMountpoints(rootMnt); err != nil {
		return nil, 0, err
	}
	if err = adjustReferences(bootMnt, rootMnt, plan.oldID, newID); err != nil {
		return nil, 0, err
	}
	if err = s.run(ctx, "sync"); err != nil {
		if cerr := ctx.Err(); cerr != nil {
			err = cerr
		}
		return nil, 0, err
	}
	if err = uw.run(ctx, 1); err != nil { // Aushängen, Loop lösen: nur die Arbeitsdatei bleibt
		return nil, 0, fmt.Errorf("Das Abbild ließ sich nicht abschließen: %v", err)
	}
	if err = s.run(ctx, "sync"); err != nil {
		return nil, 0, err
	}

	img, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, 0, fmt.Errorf("Die Arbeitsdatei der Sicherung ließ sich nicht öffnen: %v", err)
	}
	if fi, serr := img.Stat(); serr != nil || fi.Size() != plan.rawSize {
		img.Close()
		err = errors.New("Die Arbeitsdatei der Sicherung hat eine unerwartete Größe.")
		return nil, 0, err
	}
	run.b.read.Store(0)
	return &filesReader{File: img, path: path}, plan.rawSize, nil
}

func usbOrPlain(err error) error {
	if errors.Is(err, syscall.ENOSPC) {
		return errors.New("der Speicher ist voll")
	}
	return err
}

// waitNodes wartet, bis die Partitionsknoten des Loop-Geräts da sind (udev legt sie kurz nach losetup an).
func waitNodes(ctx context.Context, nodes ...string) error {
	deadline := time.Now().Add(15 * time.Second)
	for {
		missing := ""
		for _, n := range nodes {
			if _, err := os.Stat(n); err != nil {
				missing = n
				break
			}
		}
		if missing == "" {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("Das Loop-Gerät lieferte keine Partitionen (%s fehlt).", missing)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// formatFiles legt FAT und ext4 mit den Beschriftungen und Kennungen der Quelle an: so stimmen
// LABEL=- und UUID=-Einträge in fstab und cmdline.txt ohne Änderung. Solange das Loop-Gerät besteht, tragen
// das Abbild und das laufende System dieselbe UUID und Beschriftung; der Helfer spricht darum nur Gerätepfade an
// (nie LABEL=/UUID=), und das Abbild wird vor dem Ende wieder gelöst.
// Während des Baus kann udev die Links unter /dev/disk/by-uuid und by-label auf das Loop-Gerät umlegen.
func (s *Server) formatFiles(ctx context.Context, p *filesPlan, p1, p2 string) error {
	vfat := []string{"-F", "32"}
	if p.boot.label != "" {
		vfat = append(vfat, "-n", p.boot.label)
	}
	vfat = append(vfat, "-i", strings.ReplaceAll(p.boot.uuid, "-", ""), p1)
	if err := s.step(ctx, "Die Bootpartition ließ sich nicht formatieren", "mkfs.vfat", vfat...); err != nil {
		return err
	}
	ext := []string{"-q", "-F"}
	if p.root.label != "" {
		ext = append(ext, "-L", p.root.label)
	}
	ext = append(ext, "-U", p.root.uuid)
	if p.inodes > 0 {
		ext = append(ext, "-N", strconv.FormatInt(p.inodes, 10))
	} else {
		ext = append(ext, "-i", strconv.Itoa(filesInodeRatio))
	}
	ext = append(ext, "-E", "lazy_itable_init=1,lazy_journal_init=1", p2)
	return s.step(ctx, "Die Systempartition ließ sich nicht formatieren", "mkfs.ext4", ext...)
}

// copyFiles kopiert die Bootpartition (FAT: ohne Besitzer und Rechte) und danach / mit rsync.
func (s *Server) copyFiles(ctx context.Context, bootMnt, rootMnt string) error {
	fail := func(err error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		return fmt.Errorf("Das Kopieren der Dateien ist fehlgeschlagen: %v", err)
	}
	if err := s.exec(ctx, "rsync", "-rtx", "--no-owner", "--no-group", "--no-perms", "--omit-dir-times", "-q",
		sourceDir(s.Backup.bootMount()), sourceDir(bootMnt)); err != nil && !rsyncOK(err) {
		return fail(err)
	}
	args := []string{"-aHAXx", "-S", "--numeric-ids", "-q"}
	for _, e := range filesExcludes {
		args = append(args, "--exclude="+e)
	}
	args = append(args, sourceDir(s.Backup.rootDir()), sourceDir(rootMnt))
	if err := s.exec(ctx, "rsync", args...); err != nil && !rsyncOK(err) {
		return fail(err)
	}
	return nil
}

// pollCopy meldet den Fortschritt der Kopie: Belegung des Ziels gegen die belegte Größe der Quelle.
// Die Belegung eines frisch formatierten Dateisystems (Journal, Tabellen) zählt nicht mit.
func (s *Server) pollCopy(ctx context.Context, run *backupRun, total int64, dirs ...string) (stop func()) {
	pctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	usage := func() int64 {
		var sum int64
		for _, d := range dirs {
			if b, _, err := s.fsUsage(d); err == nil {
				sum += b
			}
		}
		return sum
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		base := usage()
		t := time.NewTicker(copyPollEvery)
		defer t.Stop()
		for {
			select {
			case <-pctx.Done():
				return
			case <-t.C:
				n := max(usage()-base, 0)
				if total > 0 {
					n = min(n, total)
				}
				run.b.read.Store(n)
			}
		}
	}()
	return func() { cancel(); wg.Wait() }
}

// makeMountpoints legt die Ordner an, die die Kopie ausgelassen hat, leer und mit den üblichen Rechten.
func makeMountpoints(root string) error {
	for _, m := range filesMountpoints {
		p := filepath.Join(root, m.name)
		if err := os.Mkdir(p, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("Der Ordner /%s ließ sich im Abbild nicht anlegen: %v", m.name, err)
		}
		if err := os.Chmod(p, m.mode); err != nil {
			return fmt.Errorf("Die Rechte von /%s ließen sich im Abbild nicht setzen: %v", m.name, err)
		}
	}
	return nil
}

// filesReader liest die fertige Arbeitsdatei; das Schließen löscht sie.
type filesReader struct {
	*os.File
	path string
	once sync.Once
}

func (r *filesReader) Close() error {
	r.once.Do(func() {
		r.File.Close()
		if err := os.Remove(r.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("Arbeitsdatei %s nicht gelöscht: %v", r.path, err)
		}
	})
	return nil
}

// detachLoopsOf löst alle Loop-Geräte, die an der Datei hängen (losetup -j).
func (s *Server) detachLoopsOf(ctx context.Context, path string) error {
	if s.ExecOut == nil {
		return errors.New("losetup ist nicht verfügbar")
	}
	out, err := s.ExecOut(ctx, "losetup", "-j", path)
	if err != nil {
		return fmt.Errorf("losetup -j %s: %v", path, err)
	}
	var first error
	for _, line := range strings.Split(out, "\n") {
		dev, _, ok := strings.Cut(line, ":")
		if !ok || !loopDevRe.MatchString(dev) {
			continue
		}
		if derr := s.exec(ctx, "losetup", "-d", dev); derr != nil && first == nil {
			first = fmt.Errorf("losetup -d %s: %v", dev, derr)
		}
	}
	return first
}
