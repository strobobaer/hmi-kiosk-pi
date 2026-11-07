package helper

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Echter Lauf der dateiweisen Sicherung mit Loop-Geräten. Braucht root, Loop-Geräte und echte Geräteknoten
// unter /dev, darum nur im privilegierten Container (test/backup-files/run.sh), nicht im gewöhnlichen
// Testlauf (HMI_LOOP_TEST=1).

func sh(t *testing.T, name string, args ...string) string {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func realExecOut(ctx context.Context, name string, args ...string) (string, error) {
	p, err := exec.LookPath(name)
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, p, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

type loopSource struct {
	loop, root string
}

// makeLoopSource baut das nachgebaute Quellsystem: eine "Karte" (Loop) mit FAT und ext4 im Pi-OS-Layout.
func makeLoopSource(t *testing.T, tmp string) loopSource {
	t.Helper()
	card := filepath.Join(tmp, "quelle.img")
	f, err := os.Create(card)
	if err != nil {
		t.Fatal(err)
	}
	f.Truncate(300 << 20)
	f.Close()
	script := "label: dos\nlabel-id: 0xe5c4a53d\nunit: sectors\n\nstart=8192, size=131072, type=c, bootable\nstart=139264, size=400000, type=83\n"
	cmd := exec.Command("sfdisk", "--quiet", card)
	cmd.Stdin = strings.NewReader(script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sfdisk: %v\n%s", err, out)
	}
	loop := sh(t, "losetup", "-P", "--show", "-f", card)
	t.Cleanup(func() { exec.Command("losetup", "-d", loop).Run() })
	waitFor(t, loop+"p1", loop+"p2")
	sh(t, "mkfs.vfat", "-F", "32", "-n", "bootfs", "-i", "ABCD1234", loop+"p1")
	sh(t, "mkfs.ext4", "-q", "-F", "-L", "rootfs", "-U", fRootUUID, loop+"p2")
	root := filepath.Join(tmp, "quelle")
	mkdirs(t, root)
	sh(t, "mount", loop+"p2", root)
	t.Cleanup(func() { exec.Command("umount", "-l", root).Run() })
	mkdirs(t, filepath.Join(root, "boot", "firmware"))
	sh(t, "mount", loop+"p1", filepath.Join(root, "boot", "firmware"))
	t.Cleanup(func() { exec.Command("umount", "-l", filepath.Join(root, "boot", "firmware")).Run() })
	return loopSource{loop: loop, root: root}
}

func waitFor(t *testing.T, paths ...string) {
	t.Helper()
	for range 100 {
		ok := true
		for _, p := range paths {
			if _, err := os.Stat(p); err != nil {
				ok = false
			}
		}
		if ok {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("Geräteknoten fehlen: %v", paths)
}

// fill legt Dateien an, die erhalten bleiben (oder ausgeschlossen sein) müssen.
func (ls loopSource) fill(t *testing.T) (bigSum [32]byte) {
	t.Helper()
	r := ls.root
	writeFile(t, filepath.Join(r, "boot", "firmware", "cmdline.txt"), piCmdline)
	writeFile(t, filepath.Join(r, "boot", "firmware", "config.txt"), "[all]\narm_64bit=1\n")
	writeFile(t, filepath.Join(r, "etc", "fstab"), piFstab)
	big := make([]byte, 24<<20)
	for i := range big {
		big[i] = byte(i * 7 % 251)
	}
	writeFile(t, filepath.Join(r, "usr", "lib", "gross.bin"), string(big))
	writeFile(t, filepath.Join(r, "var", "lib", "hmi-kiosk", "config.json"), `{"a":1}`)
	os.Chmod(filepath.Join(r, "var", "lib", "hmi-kiosk", "config.json"), 0o600)
	os.Chown(filepath.Join(r, "var", "lib", "hmi-kiosk", "config.json"), 1100, 1100)
	if err := os.Link(filepath.Join(r, "var", "lib", "hmi-kiosk", "config.json"), filepath.Join(r, "var", "lib", "hmi-kiosk", "config.hard")); err != nil {
		t.Fatal(err)
	}
	symlink(t, "/etc/hostname", filepath.Join(r, "etc", "hostname-link"))
	symlink(t, "../gross.bin", filepath.Join(r, "usr", "lib", "rel-link"))
	sh(t, "setfattr", "-n", "user.hmi", "-v", "ja", filepath.Join(r, "var", "lib", "hmi-kiosk", "config.json"))
	// Ausgeschlossenes
	writeFile(t, filepath.Join(r, "tmp", "x"), "weg")
	writeFile(t, filepath.Join(r, "var", "tmp", "y"), "weg")
	writeFile(t, filepath.Join(r, "proc", "x"), "weg")
	writeFile(t, filepath.Join(r, "swapfile"), "weg")
	writeFile(t, filepath.Join(r, "var", "swap"), "weg")
	writeFile(t, filepath.Join(r, "var", "cache", "hmi-kiosk-backup", "z"), "weg")
	writeFile(t, filepath.Join(r, "var", "cache", "behalten"), "da")
	writeFile(t, filepath.Join(r, "media", "stick", "q"), "weg")
	writeFile(t, filepath.Join(r, "lost+found", "aus-der-quelle"), "weg")
	h := sha256.Sum256(big)
	sh(t, "sync")
	return h
}

func sumFile(t *testing.T, p string) [32]byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(b)
}

func newLoopEnv(t *testing.T) (*env, loopSource, string) {
	t.Helper()
	if os.Getenv("HMI_LOOP_TEST") != "1" {
		t.Skip("nur im privilegierten Container (test/backup-files/run.sh)")
	}
	linuxOnly(t)
	if os.Geteuid() != 0 {
		t.Skip("braucht root")
	}
	tmp := t.TempDir()
	ls := makeLoopSource(t, tmp)
	e := newEnv(t, "[all]\n")
	e.srv.exec = func(ctx context.Context, name string, args ...string) error {
		_, err := realExecOut(ctx, name, args...)
		return err
	}
	e.srv.ExecOut = realExecOut
	b := &e.srv.Backup
	b.Source = ls.loop
	b.ImageRelease = filepath.Join(tmp, "kein-image-release")
	b.RootDir = ls.root
	b.BootMount = filepath.Join(ls.root, "boot", "firmware")
	b.CacheDir = filepath.Join(tmp, "cache", "hmi-kiosk-backup")
	b.ImageRunDir = filepath.Join(tmp, "run", "hmi-kiosk-image")
	b.MountInfo = "/proc/self/mountinfo"
	mkdirs(t, filepath.Dir(b.CacheDir))
	mkdirs(t, filepath.Dir(b.ImageRunDir))
	return e, ls, tmp
}

func wantNoLeftovers(t *testing.T, e *env) {
	t.Helper()
	b := e.srv.Backup
	if es, _ := os.ReadDir(b.CacheDir); len(es) != 0 {
		t.Errorf("Arbeitsordner nicht leer: %v", es)
	}
	for _, d := range []string{"boot", "root"} {
		if _, err := os.Stat(filepath.Join(b.ImageRunDir, d)); err == nil {
			t.Errorf("Einhängeordner %s übrig", d)
		}
	}
	if mi, _ := os.ReadFile("/proc/self/mountinfo"); strings.Contains(string(mi), b.ImageRunDir) {
		t.Errorf("Mount übrig:\n%s", mi)
	}
	if out := sh(t, "losetup", "-l", "-n", "-O", "NAME,BACK-FILE"); strings.Contains(out, "hmikiosk-") {
		t.Errorf("Loop-Gerät übrig:\n%s", out)
	}
}

func TestFilesLoopImage(t *testing.T) {
	e, ls, tmp := newLoopEnv(t)
	bigSum := ls.fill(t)

	ctx := context.Background()
	plan, err := e.srv.planFiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	run, err := e.srv.backup.begin(ctx, "usb", "Test")
	if err != nil {
		t.Fatal(err)
	}
	rc, size, err := e.srv.openFiles(run.ctx, run, filesTarget{dir: e.srv.Backup.cacheDir(), name: "hmikiosk-test.img"})
	if err != nil {
		t.Fatalf("openFiles: %v", err)
	}
	if size != plan.rawSize {
		t.Errorf("Größe %d, Plan %d", size, plan.rawSize)
	}
	run.setTotal(size)
	// gzip lesbar: komprimieren wie der Strom, entpacken, Prüfsumme mit der Rohdatei vergleichen.
	var gz bytes.Buffer
	if err := e.srv.copyCard(run, rc, &gz); err != nil {
		t.Fatal(err)
	}
	run.end(nil, "")
	rc.Close()
	img := filepath.Join(tmp, "ergebnis.img")
	out, _ := os.Create(img)
	zr, err := gzip.NewReader(&gz)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := io.Copy(out, zr); err != nil || n != size {
		t.Fatalf("entpackt %d von %d: %v", n, size, err)
	}
	out.Close()
	wantNoLeftovers(t, e)

	// Partitionstabelle
	f, _ := os.Open(img)
	head := make([]byte, 512)
	f.ReadAt(head, 0)
	f.Close()
	es, err := parseMBR(head)
	if err != nil {
		t.Fatal(err)
	}
	newID := binary.LittleEndian.Uint32(head[440:])
	if newID == fOldID || newID == 0 {
		t.Errorf("Disk-ID nicht neu: %08x", newID)
	}
	if es[0].Type != 0x0c || es[0].Start != 8192 || es[0].Sectors != 131072 || es[1].Type != 0x83 || es[1].Start%2048 != 0 || es[1].Start != 139264 || !es[2].empty() || !es[3].empty() {
		t.Errorf("Partitionen: %+v", es)
	}
	if o := sh(t, "sfdisk", "-d", img); !strings.Contains(o, "label: dos") || strings.Count(o, "start=") != 2 {
		t.Errorf("sfdisk -d:\n%s", o)
	}

	// Inhalt: Abbild einhängen
	loop := sh(t, "losetup", "-P", "--show", "-f", img)
	defer exec.Command("losetup", "-d", loop).Run()
	waitFor(t, loop+"p1", loop+"p2")
	if o := sh(t, "blkid", "-p", "-o", "export", loop+"p1"); !strings.Contains(o, "TYPE=vfat") || !strings.Contains(strings.ToLower(o), "label=bootfs") || !strings.Contains(o, "UUID=ABCD-1234") {
		t.Errorf("p1: %s", o)
	}
	if o := sh(t, "blkid", "-p", "-o", "export", loop+"p2"); !strings.Contains(o, "TYPE=ext4") || !strings.Contains(o, "LABEL=rootfs") || !strings.Contains(o, "UUID="+fRootUUID) {
		t.Errorf("p2: %s", o)
	}
	bm, rm := filepath.Join(tmp, "ziel-boot"), filepath.Join(tmp, "ziel-root")
	mkdirs(t, bm)
	mkdirs(t, rm)
	sh(t, "mount", "-o", "ro", loop+"p1", bm)
	defer exec.Command("umount", "-l", bm).Run()
	sh(t, "mount", "-o", "ro", loop+"p2", rm)
	defer exec.Command("umount", "-l", rm).Run()

	id := fmt.Sprintf("%08x", newID)
	cmdline, _ := os.ReadFile(filepath.Join(bm, "cmdline.txt"))
	if !strings.Contains(string(cmdline), "root=PARTUUID="+id+"-02 ") || strings.Contains(string(cmdline), "e5c4a53d") || !strings.Contains(string(cmdline), "rootwait") {
		t.Errorf("cmdline.txt: %s", cmdline)
	}
	fstab, _ := os.ReadFile(filepath.Join(rm, "etc", "fstab"))
	for _, want := range []string{"PARTUUID=" + id + "-01  /boot/firmware", "PARTUUID=" + id + "-02  /  ", "UUID=0a0b0c0d-1111-2222-3333-444455556666"} {
		if !strings.Contains(string(fstab), want) {
			t.Errorf("fstab ohne %q:\n%s", want, fstab)
		}
	}
	if strings.Contains(string(fstab), "e5c4a53d") {
		t.Errorf("fstab mit alter ID:\n%s", fstab)
	}
	if _, err := os.Stat(filepath.Join(bm, "config.txt")); err != nil {
		t.Errorf("config.txt: %v", err)
	}
	if got := sumFile(t, filepath.Join(rm, "usr", "lib", "gross.bin")); got != bigSum {
		t.Error("gross.bin weicht ab")
	}
	cj := filepath.Join(rm, "var", "lib", "hmi-kiosk", "config.json")
	st := fileStat(t, cj)
	if st.Uid != 1100 || st.Gid != 1100 || st.Mode&0o7777 != 0o600 || st.Nlink != 2 {
		t.Errorf("config.json: uid %d gid %d mode %o links %d", st.Uid, st.Gid, st.Mode&0o7777, st.Nlink)
	}
	if hs := fileStat(t, filepath.Join(rm, "var", "lib", "hmi-kiosk", "config.hard")); hs.Ino != st.Ino {
		t.Error("fester Verweis nicht erhalten")
	}
	if o, _ := exec.Command("getfattr", "-n", "user.hmi", "--only-values", "--absolute-names", cj).Output(); string(o) != "ja" {
		t.Errorf("xattr: %q", o)
	}
	if l, _ := os.Readlink(filepath.Join(rm, "etc", "hostname-link")); l != "/etc/hostname" {
		t.Errorf("Verweis: %q", l)
	}
	if l, _ := os.Readlink(filepath.Join(rm, "usr", "lib", "rel-link")); l != "../gross.bin" {
		t.Errorf("relativer Verweis: %q", l)
	}
	for _, gone := range []string{"tmp/x", "var/tmp/y", "proc/x", "swapfile", "var/swap", "var/cache/hmi-kiosk-backup", "media/stick", "lost+found/aus-der-quelle"} {
		if _, err := os.Lstat(filepath.Join(rm, gone)); err == nil {
			t.Errorf("%s ist im Abbild, soll fehlen", gone)
		}
	}
	if _, err := os.Lstat(filepath.Join(rm, "var", "cache", "behalten")); err != nil {
		t.Errorf("var/cache/behalten fehlt: %v", err)
	}
	for _, d := range []string{"proc", "sys", "dev", "run", "tmp", "mnt", "media", "var/tmp", "boot/firmware"} {
		ents, err := os.ReadDir(filepath.Join(rm, d))
		if err != nil || len(ents) != 0 {
			t.Errorf("/%s: %v (%d Einträge), soll leer vorhanden sein", d, err, len(ents))
		}
	}
	if m := fileStat(t, filepath.Join(rm, "tmp")); m.Mode&0o7777 != 0o1777 {
		t.Errorf("/tmp: %o", m.Mode&0o7777)
	}
}

func fileStat(t *testing.T, p string) *syscall.Stat_t {
	t.Helper()
	var st syscall.Stat_t
	if err := syscall.Lstat(p, &st); err != nil {
		t.Fatal(err)
	}
	return &st
}

// Ein Abbruch mitten in der Kopie baut alles zurück: Mounts, Loop-Gerät, Arbeitsdatei, Einhängeordner.
func TestFilesLoopCancelCleansUp(t *testing.T) {
	e, ls, _ := newLoopEnv(t)
	ls.fill(t)
	// Genug Daten, damit rsync noch läuft, wenn der Abbruch kommt.
	for i := range 40 {
		writeFile(t, filepath.Join(ls.root, "usr", "share", fmt.Sprintf("d%02d", i)), strings.Repeat("x", 3<<20))
	}
	sh(t, "sync")
	run, err := e.srv.backup.begin(context.Background(), "download", "Browser")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for range 600 {
			if e.srv.backup.status().Phase == PhaseCopy {
				time.Sleep(300 * time.Millisecond)
				e.srv.backup.userCancel()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		e.srv.backup.userCancel()
	}()
	_, _, err = e.srv.openFiles(run.ctx, run, filesTarget{dir: e.srv.Backup.cacheDir(), name: "hmikiosk-abbruch.img"})
	run.end(err, "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err: %v", err)
	}
	wantNoLeftovers(t, e)
}

// Reste eines Absturzes (eingehängtes Abbild, Loop-Gerät, Arbeitsdatei) räumt der Helferstart weg.
func TestFilesLoopStaleCleanupAtStart(t *testing.T) {
	e, _, tmp := newLoopEnv(t)
	b := &e.srv.Backup
	b.UsbMount = filepath.Join(tmp, "usb")
	b.RunDir = filepath.Join(tmp, "runtmp")
	mkdirs(t, b.UsbMount)
	mkdirs(t, b.RunDir)
	mkdirs(t, b.CacheDir)
	os.Chmod(b.CacheDir, 0o700)
	raw := filepath.Join(b.CacheDir, "hmikiosk-absturz.img")
	f, _ := os.Create(raw)
	f.Truncate(32 << 20)
	f.Close()
	sh(t, "mkfs.ext4", "-q", "-F", raw)
	loop := sh(t, "losetup", "-f", "--show", raw)
	t.Cleanup(func() { exec.Command("losetup", "-d", loop).Run() })
	mnt := filepath.Join(b.ImageRunDir, "root")
	mkdirs(t, mnt)
	sh(t, "mount", loop, mnt)
	t.Cleanup(func() { exec.Command("umount", "-l", mnt).Run() })
	writeFile(t, filepath.Join(mnt, "datei"), "x")
	writeFile(t, filepath.Join(b.CacheDir, "fremd.txt"), "bleibt")

	e.srv.CleanupStale(context.Background())

	if _, err := os.Lstat(raw); err == nil {
		t.Error("Arbeitsdatei nicht gelöscht")
	}
	if _, err := os.Lstat(filepath.Join(b.CacheDir, "fremd.txt")); err != nil {
		t.Errorf("fremde Datei gelöscht: %v", err)
	}
	if out := sh(t, "losetup", "-l", "-n", "-O", "NAME,BACK-FILE"); strings.Contains(out, "hmikiosk-absturz") {
		t.Errorf("Loop-Gerät übrig:\n%s", out)
	}
	if mi, _ := os.ReadFile("/proc/self/mountinfo"); strings.Contains(string(mi), b.ImageRunDir) {
		t.Errorf("Mount übrig:\n%s", mi)
	}
	if _, err := os.Stat(mnt); err == nil {
		t.Error("Einhängeordner übrig")
	}
}
