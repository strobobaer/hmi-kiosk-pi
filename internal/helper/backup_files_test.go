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
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hmikiosk/internal/helperapi"
)

// Ersatzkarte im Raspberry-Pi-Layout: p1 FAT (8 MiB ab 4 MiB), p2 ext4 (ab 12 MiB).
const (
	fP1Start, fP1Size = 8192, 16384
	fP2Start, fP2Size = 24576, 100000
	fOldID            = 0xe5c4a53d
	fBootUUID         = "ABCD-1234"
	fRootUUID         = "6f1d2c1e-8a47-4b8e-9f6a-2d1f0c3b7a55"
)

const piCmdline = "console=serial0,115200 console=tty1 root=PARTUUID=e5c4a53d-02 rootfstype=ext4 fsck.repair=yes rootwait cfg80211.ieee80211_regdom=DE\n"

const piFstab = `proc            /proc           proc    defaults          0       0
PARTUUID=e5c4a53d-01  /boot/firmware  vfat    defaults          0       2
PARTUUID=e5c4a53d-02  /               ext4    defaults,noatime  0       1
UUID=0a0b0c0d-1111-2222-3333-444455556666  /srv  ext4  defaults  0  2
`

func filesCardMBR(mod func(sec []byte)) []byte {
	sec := make([]byte, 512)
	binary.LittleEndian.PutUint32(sec[440:], fOldID)
	putEntry(sec, 0, 0x0c, fP1Start, fP1Size)
	putEntry(sec, 1, 0x83, fP2Start, fP2Size)
	sec[446] = 0x80
	sec[510], sec[511] = 0x55, 0xaa
	if mod != nil {
		mod(sec)
	}
	return sec
}

// filesFake ersetzt alle Programme der dateiweisen Sicherung und zeichnet die Aufrufe auf.
type filesFake struct {
	t     *testing.T
	e     *env
	dev   string
	mu    sync.Mutex
	blkid map[string]string
	// rsync läuft beim Aufruf (nach dem Aufzeichnen); Fehler werden weitergegeben.
	rsync func(ctx context.Context, args []string) error
	// failOn lässt einen Aufruf scheitern.
	failOn func(name string, args []string) error
	// snapshots: Inhalt von Dateien, bevor ein Einhängepunkt "ausgehängt" (geleert) wird.
	snaps map[string]string
	// dirs: Einhängepunkte beim Aushängen (Namen der Einträge).
	dirs   map[string][]string
	mounts map[string]bool
	loopUp bool
	// attachThenFail: losetup hängt die Datei an und meldet trotzdem einen Fehler (z. B. beendet nach dem Anhängen).
	attachThenFail bool
}

func (f *filesFake) call(ctx context.Context, name string, args ...string) (string, error) {
	f.e.fx.mu.Lock()
	f.e.fx.calls = append(f.e.fx.calls, strings.Join(append([]string{name}, args...), " "))
	f.e.fx.mu.Unlock()
	if f.failOn != nil {
		if err := f.failOn(name, args); err != nil {
			return "", err
		}
	}
	switch name {
	case "blkid":
		return f.blkid[args[len(args)-1]], nil
	case "losetup":
		if len(args) > 0 && args[0] == "-P" {
			if f.attachThenFail {
				f.mu.Lock()
				f.loopUp = true
				f.mu.Unlock()
				return "", errors.New("abgebrochen nach dem Anhängen")
			}
			dir := f.e.srv.Backup.devDir()
			for _, n := range []string{"loop7p1", "loop7p2"} {
				writeFile(f.t, filepath.Join(dir, n), "")
			}
			f.mu.Lock()
			f.loopUp = true
			f.mu.Unlock()
			return "/dev/loop7\n", nil
		}
		if len(args) > 0 && args[0] == "-d" {
			f.mu.Lock()
			f.loopUp = false
			f.mu.Unlock()
		}
		if len(args) > 1 && args[0] == "-j" {
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.loopUp {
				return "/dev/loop7: [64768]:12 (" + args[1] + ")\n", nil
			}
		}
	case "mount":
		f.mu.Lock()
		f.mounts[args[len(args)-1]] = true
		f.mu.Unlock()
	case "umount":
		dir := args[len(args)-1]
		f.mu.Lock()
		delete(f.mounts, dir)
		f.mu.Unlock()
		for _, rel := range []string{"cmdline.txt", "etc/fstab"} {
			if b, err := os.ReadFile(filepath.Join(dir, rel)); err == nil {
				f.snaps[filepath.Base(dir)+"/"+rel] = string(b)
			}
		}
		entries, _ := os.ReadDir(dir)
		if !strings.HasPrefix(dir, f.e.srv.Backup.imageRunDir()) {
			entries = nil // nur die Ziele des Abbilds werden geleert
		}
		for _, en := range entries {
			f.dirs[filepath.Base(dir)] = append(f.dirs[filepath.Base(dir)], en.Name())
			if fi, err := os.Lstat(filepath.Join(dir, en.Name())); err == nil && en.Name() == "tmp" {
				f.dirs[filepath.Base(dir)+"/tmp"] = []string{fi.Mode().String()}
			}
			os.RemoveAll(filepath.Join(dir, en.Name()))
		}
	case "rsync":
		if f.rsync != nil {
			return "", f.rsync(ctx, args)
		}
	}
	return "", nil
}

func (f *filesFake) exec(ctx context.Context, name string, args ...string) error {
	_, err := f.call(ctx, name, args...)
	return err
}

func (fe *filesEnv) calls() []string { return fe.fx.got() }

type filesEnv struct {
	*env
	ff       *filesFake
	card     string
	root     string // RootDir (Quelle von /)
	bootSrc  string // BootMount (Quelle von /boot/firmware)
	cache    string
	imgRun   string
	dev      string
	usedRoot int64
	inodes   int64
}

func newFilesEnv(t *testing.T) *filesEnv {
	t.Helper()
	linuxOnly(t)
	e := newEnv(t, "[all]\n")
	tmp := t.TempDir()
	fe := &filesEnv{env: e, root: filepath.Join(tmp, "src"), bootSrc: filepath.Join(tmp, "src", "boot", "firmware"),
		cache: filepath.Join(tmp, "cache", "hmi-kiosk-backup"), imgRun: filepath.Join(tmp, "run", "hmi-kiosk-image"),
		dev: filepath.Join(tmp, "dev"), usedRoot: 1 << 30, inodes: 10000}
	mkdirs(t, fe.bootSrc)
	mkdirs(t, filepath.Dir(fe.cache))
	mkdirs(t, filepath.Dir(fe.imgRun))
	mkdirs(t, fe.dev)
	fe.card = filepath.Join(tmp, "mmcblk9")
	fe.writeCard(t, nil)
	fe.ff = &filesFake{t: t, e: e, dev: fe.dev, snaps: map[string]string{}, dirs: map[string][]string{}, mounts: map[string]bool{},
		blkid: map[string]string{
			fe.card + "p1": "DEVNAME=x\nLABEL=bootfs\nUUID=" + fBootUUID + "\nTYPE=vfat\nPARTUUID=e5c4a53d-01\n",
			fe.card + "p2": "DEVNAME=x\nLABEL=rootfs\nUUID=" + fRootUUID + "\nTYPE=ext4\nPARTUUID=e5c4a53d-02\n",
		}}
	b := &e.srv.Backup
	b.Source = fe.card
	b.ImageRelease = filepath.Join(tmp, "kein-image-release")
	b.RootDir, b.BootMount, b.CacheDir, b.ImageRunDir, b.DevDir = fe.root, fe.bootSrc, fe.cache, fe.imgRun, fe.dev
	b.IdleTimeout = 5 * time.Second
	b.Now = func() time.Time { return time.Date(2026, 11, 5, 9, 7, 0, 0, time.Local) }
	b.FsUsage = func(path string) (int64, int64, error) {
		if strings.HasPrefix(path, fe.imgRun) {
			return 0, 0, nil
		}
		return fe.usedRoot, fe.inodes, nil
	}
	b.FreeBytes = func(string) (int64, error) { return 100 << 30, nil }
	b.extraBytes = 8 * mib
	e.srv.exec = fe.ff.exec
	e.srv.ExecOut = fe.ff.call
	fe.ff.rsync = func(_ context.Context, args []string) error {
		dst := args[len(args)-1]
		if strings.HasSuffix(strings.TrimSuffix(dst, "/"), "/boot") {
			writeFile(t, filepath.Join(dst, "cmdline.txt"), piCmdline)
		} else {
			writeFile(t, filepath.Join(dst, "etc", "fstab"), piFstab)
		}
		return nil
	}
	return fe
}

func (fe *filesEnv) writeCard(t *testing.T, mod func(sec []byte)) {
	t.Helper()
	f, err := os.Create(fe.card)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Truncate((fP2Start + fP2Size) * 512); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt(filesCardMBR(mod), 0); err != nil {
		t.Fatal(err)
	}
}

func hasCall(calls []string, prefix string) bool {
	for _, c := range calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func TestFilesSystemSize(t *testing.T) {
	for _, c := range []struct{ used, want int64 }{
		{0, 512 * mib},
		{1 << 30, 1792 * mib},
		{4000 * mib, 5512 * mib},
		{1000*mib + 1, 1763 * mib}, // aufgerundet auf MiB
	} {
		if got := filesSystemSize(c.used, 512*mib); got != c.want {
			t.Errorf("filesSystemSize(%d) = %d MiB, erwartet %d MiB", c.used, got/mib, c.want/mib)
		}
	}
}

func TestFilesPlanSizesAndLabels(t *testing.T) {
	fe := newFilesEnv(t)
	fe.srv.Backup.extraBytes = 0 // Voreinstellung 512 MiB
	p, err := fe.srv.planFiles(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// 4 MiB Vorlauf + Boot (16384 Sektoren = 8 MiB) = Start von p2; System = 1 GiB + 25 % + 512 MiB.
	if p.p2Start != 24576 || p.sysBytes != 1792*mib || p.rawSize != 24576*512+1792*mib {
		t.Errorf("Plan: p2 ab %d, System %d, roh %d", p.p2Start, p.sysBytes, p.rawSize)
	}
	if p.oldID != fOldID || p.boot.uuid != fBootUUID || p.boot.label != "bootfs" || p.root.uuid != fRootUUID || p.root.label != "rootfs" {
		t.Errorf("Plan: %+v", p)
	}
	if p.copyBytes != 1<<30 {
		t.Errorf("Kopiergröße %d", p.copyBytes)
	}
}

func TestFilesPlanRejectsLayouts(t *testing.T) {
	cases := map[string]struct {
		mod   func(sec []byte)
		blkid map[string]string
		want  string
	}{
		"dritte Partition":   {mod: func(s []byte) { putEntry(s, 2, 0x83, 200000, 1000) }, want: "weitere Partitionen"},
		"vierte Partition":   {mod: func(s []byte) { putEntry(s, 3, 0x82, 200000, 1000) }, want: "weitere Partitionen"},
		"GPT-Schutz-MBR":     {mod: func(s []byte) { putEntry(s, 0, 0xee, 1, 99999) }, want: "Layout"},
		"keine Kennung":      {mod: func(s []byte) { s[510] = 0 }, want: "Partitionstabelle"},
		"p2 kein Linux-Typ":  {mod: func(s []byte) { s[446+16+4] = 0x07 }, want: "Partition 2"},
		"p2 vor p1":          {mod: func(s []byte) { putEntry(s, 0, 0x0c, 40000, 1000) }, want: "Layout"},
		"p2 hinter dem Ende": {mod: func(s []byte) { putEntry(s, 1, 0x83, fP2Start, fP2Size*4) }, want: "Layout"},
		"p2 btrfs":           {blkid: map[string]string{"p2": "TYPE=btrfs\nUUID=" + fRootUUID + "\n"}, want: "btrfs"},
		"p2 LVM":             {blkid: map[string]string{"p2": "TYPE=LVM2_member\n"}, want: "LVM2_member"},
		"p1 nicht FAT":       {blkid: map[string]string{"p1": "TYPE=ext4\nUUID=" + fRootUUID + "\n"}, want: "Partition 1"},
		"p2 ohne Kennung":    {blkid: map[string]string{"p2": "TYPE=ext4\n"}, want: "UUID"},
		"Label mit Zeichen":  {blkid: map[string]string{"p2": "TYPE=ext4\nLABEL=root fs\nUUID=" + fRootUUID + "\n"}, want: "Beschriftung"},
		"UUID unlesbar":      {blkid: map[string]string{"p1": "TYPE=vfat\nLABEL=bootfs\nUUID=zzzz\n"}, want: "UUID"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			fe := newFilesEnv(t)
			if c.mod != nil {
				fe.writeCard(t, c.mod)
			}
			for k, v := range c.blkid {
				fe.ff.blkid[fe.card+k] = v
			}
			_, err := fe.srv.planFiles(context.Background())
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err: %v, erwartet %q", err, c.want)
			}
			if info := fe.srv.backupInfo(context.Background()); info.Problem != err.Error() || info.RawBytes != 0 {
				t.Errorf("Info: %+v", info)
			}
		})
	}
}

func TestFilesDiskNames(t *testing.T) {
	for name, ok := range map[string]bool{"mmcblk0": true, "mmcblk10": true, "nvme0n1": true, "nvme1n2": true, "sda": true, "sdab": true, "mmcblk0p1": false, "nvme0n1p1": false, "nvme0": false, "sda1": false, "sd": false, "loop0": false} {
		err := checkDiskName(name)
		if (err == nil) != ok {
			t.Errorf("%s: %v", name, err)
		}
		if err != nil && !strings.Contains(err.Error(), "Layout") {
			t.Errorf("%s: Meldung ohne Layout: %v", name, err)
		}
	}
}

func TestFilesRootMustBeOnPartition2(t *testing.T) {
	s, sys := detectEnv(t)
	sysTree(t, sys, mmcPath, "mmcblk0", "mmcblk0p1", "mmcblk0p2")
	writeFile(t, filepath.Join(sys, "class", "block", "mmcblk0p1", "dev"), "179:1\n")
	writeFile(t, filepath.Join(sys, "class", "block", "mmcblk0p2", "dev"), "179:2\n")
	for name, c := range map[string]struct {
		info string
		ok   bool
	}{
		"Pi OS":                   {"22 1 179:2 / / rw,relatime - ext4 /dev/root rw\n35 22 179:1 / /boot/firmware rw - vfat /dev/mmcblk0p1 rw\n", true},
		"Wurzel auf NVMe":         {"22 1 259:2 / / rw - ext4 /dev/nvme0n1p2 rw\n35 22 179:1 / /boot/firmware rw - vfat /dev/mmcblk0p1 rw\n", false},
		"Boot auf anderer Platte": {"22 1 179:2 / / rw - ext4 /dev/root rw\n35 22 8:1 / /boot/firmware rw - vfat /dev/sda1 rw\n", false},
		"keine Wurzel":            {"35 22 179:1 / /boot/firmware rw - vfat /dev/mmcblk0p1 rw\n", false},
		"Wurzel auf p1":           {"22 1 179:1 / / rw - ext4 /dev/mmcblk0p1 rw\n35 22 179:1 / /boot/firmware rw - vfat /dev/mmcblk0p1 rw\n", false},
	} {
		t.Run(name, func(t *testing.T) {
			writeFile(t, s.Backup.MountInfo, c.info)
			err := s.checkMounts("/dev/mmcblk0")
			if (err == nil) != c.ok {
				t.Fatalf("err: %v", err)
			}
			if err != nil && !strings.Contains(err.Error(), "Layout") {
				t.Errorf("Meldung ohne Layout: %v", err)
			}
		})
	}
}

func TestFilesExcludes(t *testing.T) {
	want := []string{"/proc", "/sys", "/dev", "/run", "/tmp", "/mnt", "/media", "/lost+found",
		"/var/cache/hmi-kiosk-backup", "/var/swap", "/swapfile", "/var/tmp/*"}
	got := append([]string(nil), filesExcludes...)
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("Ausschlüsse:\n%v\nerwartet:\n%v", got, want)
	}
	for _, e := range filesExcludes {
		if !strings.HasPrefix(e, "/") {
			t.Errorf("%q ist nicht an der Wurzel verankert", e)
		}
	}
}

func TestRewritePartuuid(t *testing.T) {
	out, n, err := rewritePartuuid(piCmdline, fOldID, 0x1234abcd)
	if err != nil || n != 1 || !strings.Contains(out, "root=PARTUUID=1234abcd-02 ") || strings.Contains(out, "e5c4a53d") {
		t.Errorf("cmdline: %q %d %v", out, n, err)
	}
	if !strings.HasPrefix(out, "console=serial0") || !strings.HasSuffix(out, "regdom=DE\n") {
		t.Errorf("Rest verändert: %q", out)
	}
	out, n, err = rewritePartuuid(piFstab, fOldID, 0x1234abcd)
	want := strings.NewReplacer("e5c4a53d", "1234abcd").Replace(piFstab)
	if err != nil || n != 2 || out != want {
		t.Errorf("fstab: n=%d err=%v\n%s", n, err, out)
	}
	// Groß geschrieben im Original, die neue Kennung ist klein geschrieben.
	out, n, _ = rewritePartuuid("root=PARTUUID=E5C4A53D-02", fOldID, 0x0000abcd)
	if n != 1 || out != "root=PARTUUID=0000abcd-02" {
		t.Errorf("groß: %q", out)
	}
	// Andere Formen bleiben unverändert.
	for _, in := range []string{"root=LABEL=rootfs rootwait\n", "root=/dev/mmcblk0p2\n", "UUID=0a0b0c0d-1111-2222-3333-444455556666 / ext4\n", ""} {
		out, n, err := rewritePartuuid(in, fOldID, 5)
		if out != in || n != 0 || err != nil {
			t.Errorf("%q -> %q %d %v", in, out, n, err)
		}
	}
	// Eine fremde Partitions-Kennung lässt sich nicht abbilden.
	if _, _, err := rewritePartuuid("root=PARTUUID=aaaaaaaa-02 rootwait", fOldID, 5); err == nil || !strings.Contains(err.Error(), "aaaaaaaa") || !strings.Contains(err.Error(), "root=PARTUUID=aaaaaaaa-02") {
		t.Errorf("fremde Kennung: %v", err)
	}
}

func TestAdjustReferences(t *testing.T) {
	linuxOnly(t)
	boot, root := filepath.Join(t.TempDir(), "boot"), filepath.Join(t.TempDir(), "root")
	writeFile(t, filepath.Join(boot, "cmdline.txt"), piCmdline)
	writeFile(t, filepath.Join(root, "etc", "fstab"), piFstab)
	if err := adjustReferences(boot, root, fOldID, 0x1234abcd); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(boot, "cmdline.txt")); !strings.Contains(string(b), "root=PARTUUID=1234abcd-02") {
		t.Errorf("cmdline: %s", b)
	}
	b, _ := os.ReadFile(filepath.Join(root, "etc", "fstab"))
	if !strings.Contains(string(b), "PARTUUID=1234abcd-01") || !strings.Contains(string(b), "PARTUUID=1234abcd-02") || !strings.Contains(string(b), "UUID=0a0b0c0d-") {
		t.Errorf("fstab: %s", b)
	}
	if es, _ := filepath.Glob(filepath.Join(root, "etc", ".*")); len(es) != 0 {
		t.Errorf("Temp-Reste: %v", es)
	}
}

func TestAdjustReferencesNeverFollowsLinks(t *testing.T) {
	linuxOnly(t)
	outside := filepath.Join(t.TempDir(), "fremd")
	writeFile(t, outside, "PARTUUID=e5c4a53d-02\n")
	t.Run("fstab ist Verweis", func(t *testing.T) {
		boot, root := t.TempDir(), t.TempDir()
		writeFile(t, filepath.Join(boot, "cmdline.txt"), piCmdline)
		symlink(t, outside, filepath.Join(root, "etc", "fstab"))
		if err := adjustReferences(boot, root, fOldID, 5); err == nil {
			t.Error("kein Fehler")
		}
		if b, _ := os.ReadFile(outside); string(b) != "PARTUUID=e5c4a53d-02\n" {
			t.Errorf("Ziel des Verweises verändert: %s", b)
		}
	})
	t.Run("etc ist Verweis", func(t *testing.T) {
		boot, root := t.TempDir(), t.TempDir()
		writeFile(t, filepath.Join(boot, "cmdline.txt"), piCmdline)
		other := t.TempDir()
		writeFile(t, filepath.Join(other, "fstab"), piFstab)
		symlink(t, other, filepath.Join(root, "etc"))
		if err := adjustReferences(boot, root, fOldID, 5); err == nil {
			t.Error("kein Fehler")
		}
		if b, _ := os.ReadFile(filepath.Join(other, "fstab")); string(b) != piFstab {
			t.Errorf("Ziel des Verweises verändert: %s", b)
		}
	})
	t.Run("cmdline ist Verweis", func(t *testing.T) {
		boot, root := t.TempDir(), t.TempDir()
		symlink(t, outside, filepath.Join(boot, "cmdline.txt"))
		writeFile(t, filepath.Join(root, "etc", "fstab"), piFstab)
		if err := adjustReferences(boot, root, fOldID, 5); err == nil {
			t.Error("kein Fehler")
		}
	})
	t.Run("cmdline fehlt", func(t *testing.T) {
		if err := adjustReferences(t.TempDir(), t.TempDir(), fOldID, 5); err == nil {
			t.Error("kein Fehler")
		}
	})
}

func TestFilesMBR(t *testing.T) {
	src := filesCardMBR(nil)
	copy(src[:440], bytes.Repeat([]byte{0xab}, 440))
	out := filesMBR(src, 0x1234abcd, 8192, 16384, 24576, 3670016)
	if len(out) != 512 || out[510] != 0x55 || out[511] != 0xaa {
		t.Fatal("kein MBR")
	}
	if !bytes.Equal(out[:440], src[:440]) {
		t.Error("Startcode verändert")
	}
	if binary.LittleEndian.Uint32(out[440:]) != 0x1234abcd || out[444] != 0 || out[445] != 0 {
		t.Errorf("Disk-ID: % x", out[440:446])
	}
	es, err := parseMBR(out)
	if err != nil {
		t.Fatal(err)
	}
	if es[0].Type != 0x0c || es[0].Start != 8192 || es[0].Sectors != 16384 || es[1].Type != 0x83 || es[1].Start != 24576 || es[1].Sectors != 3670016 {
		t.Errorf("Einträge: %+v", es)
	}
	if !es[2].empty() || !es[3].empty() {
		t.Errorf("Rest nicht leer: %+v", es)
	}
	if out[446] != 0x80 || out[446+16] != 0 {
		t.Errorf("Startkennzeichen: %#x %#x", out[446], out[446+16])
	}
	if es[1].Start%alignSectors != 0 {
		t.Error("nicht ausgerichtet")
	}
}

func openAndRead(t *testing.T, fe *filesEnv) (*backupRun, []byte, int64, io.ReadCloser) {
	t.Helper()
	run, err := fe.srv.backup.begin(context.Background(), "download", "Browser")
	if err != nil {
		t.Fatal(err)
	}
	rc, size, err := fe.srv.openFiles(run.ctx, run, filesTarget{dir: fe.cache, name: "hmikiosk-test.img"})
	if err != nil {
		t.Fatalf("openFiles: %v", err)
	}
	return run, nil, size, rc
}

func TestFilesBuildSteps(t *testing.T) {
	fe := newFilesEnv(t)
	run, _, size, rc := openAndRead(t, fe)
	defer run.end(nil, "")
	p, _ := fe.srv.planFiles(context.Background())
	if size != p.rawSize {
		t.Fatalf("Größe %d, Plan %d", size, p.rawSize)
	}
	raw := filepath.Join(fe.cache, "hmikiosk-test.img")
	fi, err := os.Lstat(raw)
	if err != nil || fi.Size() != size || fi.Mode().Perm() != 0o600 {
		t.Fatalf("Arbeitsdatei: %v %v", fi, err)
	}
	if fi, _ := os.Lstat(fe.cache); fi.Mode().Perm() != 0o700 {
		t.Errorf("Arbeitsordner: %v", fi.Mode())
	}
	// Partitionstabelle: neue Disk-ID, p1 wie die Quelle, p2 füllt den Rest.
	head := make([]byte, 512)
	f, _ := os.Open(raw)
	f.ReadAt(head, 0)
	f.Close()
	es, err := parseMBR(head)
	if err != nil {
		t.Fatal(err)
	}
	id := binary.LittleEndian.Uint32(head[440:])
	if id == fOldID || id == 0 {
		t.Errorf("Disk-ID: %08x", id)
	}
	if es[0].Type != 0x0c || es[0].Start != fP1Start || es[0].Sectors != fP1Size || es[1].Type != 0x83 || es[1].Start != 24576 || int64(es[1].Sectors)*512 != p.sysBytes {
		t.Errorf("Einträge: %+v", es)
	}
	if !es[2].empty() || !es[3].empty() {
		t.Error("p3/p4 belegt")
	}
	loopP1, loopP2 := filepath.Join(fe.dev, "loop7p1"), filepath.Join(fe.dev, "loop7p2")
	bootMnt, rootMnt := filepath.Join(fe.imgRun, "boot"), filepath.Join(fe.imgRun, "root")
	excl := ""
	for _, e := range filesExcludes {
		excl += " --exclude=" + e
	}
	want := []string{
		"blkid -p -o export " + fe.card + "p1",
		"blkid -p -o export " + fe.card + "p2",
		"losetup -P --show -f " + raw,
		"mkfs.vfat -F 32 -n bootfs -i ABCD1234 " + loopP1,
		"mkfs.ext4 -q -F -L rootfs -U " + fRootUUID + " -i 16384 -E lazy_itable_init=1,lazy_journal_init=1 " + loopP2,
		"mount -t vfat -o nodev,nosuid,noexec " + loopP1 + " " + bootMnt,
		"mount -t ext4 -o nodev,nosuid,noexec " + loopP2 + " " + rootMnt,
		"rsync -rtx --no-owner --no-group --no-perms --omit-dir-times -q " + fe.bootSrc + "/ " + bootMnt + "/",
		"rsync -aHAXx -S --numeric-ids -q" + excl + " " + fe.root + "/ " + rootMnt + "/",
		"sync",
		"umount " + rootMnt,
		"umount " + bootMnt,
		"losetup -d /dev/loop7",
	}
	got := fe.calls()
	// blkid darf auch beim Planen laufen; geprüft wird die Reihenfolge der übrigen Aufrufe.
	var seq []string
	for _, c := range got {
		if strings.HasPrefix(c, "blkid") && len(seq) > 0 && seq[len(seq)-1] == c {
			continue
		}
		seq = append(seq, c)
	}
	if !subsequence(seq, want) {
		t.Fatalf("Aufrufe:\n%s\nerwartet (in dieser Reihenfolge):\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// Verweise im Ziel: neue Disk-ID in cmdline und fstab.
	newID := fmt.Sprintf("%08x", id)
	if s := fe.ff.snaps["boot/cmdline.txt"]; !strings.Contains(s, "root=PARTUUID="+newID+"-02") || strings.Contains(s, "e5c4a53d") {
		t.Errorf("cmdline: %q", s)
	}
	if s := fe.ff.snaps["root/etc/fstab"]; !strings.Contains(s, "PARTUUID="+newID+"-01") || !strings.Contains(s, "PARTUUID="+newID+"-02") || strings.Contains(s, "e5c4a53d") {
		t.Errorf("fstab: %q", s)
	}
	// Leere Einhängepunkte im Ziel, /tmp mit Sticky-Bit.
	names := strings.Join(fe.ff.dirs["root"], " ")
	for _, d := range []string{"proc", "sys", "dev", "run", "tmp", "mnt", "media"} {
		if !strings.Contains(" "+names+" ", " "+d+" ") {
			t.Errorf("Einhängepunkt /%s fehlt (%s)", d, names)
		}
	}
	if m := fe.ff.dirs["root/tmp"]; len(m) != 1 || !strings.Contains(m[0], "t") || !strings.Contains(m[0], "rwxrwxrwx") {
		t.Errorf("/tmp: %v", m)
	}
	// Nach dem Bau sind Einhängepunkte und Loop-Gerät weg, nur die Arbeitsdatei bleibt.
	if _, err := os.Stat(fe.imgRun + "/boot"); err == nil {
		t.Error("Einhängepunkt boot übrig")
	}
	if _, err := os.Stat(fe.imgRun + "/root"); err == nil {
		t.Error("Einhängepunkt root übrig")
	}
	if st := fe.srv.backup.status(); st.Phase != "Kopieren" && st.Phase != "Vorbereiten" {
		t.Errorf("Phase: %q", st.Phase)
	}
	if r := run.b.read.Load(); r != 0 {
		t.Errorf("Zähler nicht zurückgesetzt: %d", r)
	}
	// Das Schließen löscht die Arbeitsdatei.
	rc.Close()
	rc.Close()
	if _, err := os.Lstat(raw); err == nil {
		t.Error("Arbeitsdatei nach Close übrig")
	}
}

func subsequence(got, want []string) bool {
	i := 0
	for _, g := range got {
		if i < len(want) && g == want[i] {
			i++
		}
	}
	return i == len(want)
}

func (fe *filesEnv) wantClean(t *testing.T) {
	t.Helper()
	fe.ff.mu.Lock()
	defer fe.ff.mu.Unlock()
	if fe.ff.loopUp {
		t.Errorf("Loop-Gerät nicht gelöst: %v", fe.calls())
	}
	if len(fe.ff.mounts) != 0 {
		t.Errorf("Einhängepunkte übrig: %v", fe.ff.mounts)
	}
	if es, _ := os.ReadDir(fe.cache); len(es) != 0 {
		var n []string
		for _, e := range es {
			n = append(n, e.Name())
		}
		t.Errorf("Arbeitsordner nicht leer: %v", n)
	}
	for _, d := range []string{"boot", "root"} {
		if _, err := os.Stat(filepath.Join(fe.imgRun, d)); err == nil {
			t.Errorf("Ordner %s übrig", d)
		}
	}
}

func TestFilesCancelDuringCopyCleansUp(t *testing.T) {
	fe := newFilesEnv(t)
	started := make(chan struct{})
	fe.ff.rsync = func(ctx context.Context, args []string) error {
		if strings.HasSuffix(args[len(args)-1], "/root/") {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	}
	run, err := fe.srv.backup.begin(context.Background(), "download", "Browser")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := fe.srv.openFiles(run.ctx, run, filesTarget{dir: fe.cache, name: "hmikiosk-test.img"})
		done <- err
	}()
	<-started
	if st := fe.srv.backup.status(); st.Phase != "Kopieren" {
		t.Errorf("Phase: %q", st.Phase)
	}
	if err := fe.srv.backup.userCancel(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("err: %v", err)
	}
	fe.wantClean(t)
	run.end(context.Canceled, "")
}

func TestFilesBuildFailuresCleanUp(t *testing.T) {
	for name, fail := range map[string]string{
		"mkfs.vfat": "mkfs.vfat", "mkfs.ext4": "mkfs.ext4", "mount": "mount", "rsync": "rsync", "losetup": "losetup", "sync": "sync",
	} {
		t.Run(name, func(t *testing.T) {
			fe := newFilesEnv(t)
			fe.ff.failOn = func(n string, args []string) error {
				if n == fail && !(n == "losetup" && (args[0] == "-d" || args[0] == "-j")) {
					return errors.New("kaputt")
				}
				return nil
			}
			run, _ := fe.srv.backup.begin(context.Background(), "download", "Browser")
			_, _, err := fe.srv.openFiles(run.ctx, run, filesTarget{dir: fe.cache, name: "hmikiosk-test.img"})
			if err == nil || !strings.Contains(err.Error(), "kaputt") {
				t.Fatalf("err: %v", err)
			}
			fe.wantClean(t)
			run.end(err, "")
		})
	}
}

func TestFilesRsyncVanishedFilesAreFine(t *testing.T) {
	fe := newFilesEnv(t)
	n := 0
	fe.ff.rsync = func(_ context.Context, args []string) error {
		n++
		if n == 2 {
			return exitErr(24)
		}
		if n == 1 {
			writeFile(t, filepath.Join(args[len(args)-1], "cmdline.txt"), piCmdline)
		} else {
			writeFile(t, filepath.Join(args[len(args)-1], "etc", "fstab"), piFstab)
		}
		return nil
	}
	run, _ := fe.srv.backup.begin(context.Background(), "download", "Browser")
	rc, _, err := fe.srv.openFiles(run.ctx, run, filesTarget{dir: fe.cache, name: "hmikiosk-test.img"})
	if err != nil {
		t.Fatalf("Exit 24 ist kein Fehler: %v", err)
	}
	rc.Close()
	run.end(nil, "")
	fe.ff.rsync = func(context.Context, []string) error { return exitErr(23) }
	run, _ = fe.srv.backup.begin(context.Background(), "download", "Browser")
	if _, _, err := fe.srv.openFiles(run.ctx, run, filesTarget{dir: fe.cache, name: "hmikiosk-test.img"}); err == nil {
		t.Fatal("Exit 23 muss scheitern")
	}
	run.end(nil, "")
	fe.wantClean(t)
}

type exitErr int

func (e exitErr) Error() string { return fmt.Sprintf("exit status %d", int(e)) }
func (e exitErr) ExitCode() int { return int(e) }

func TestFilesRejectsAdjustFailureAndCleansUp(t *testing.T) {
	fe := newFilesEnv(t)
	fe.ff.rsync = func(_ context.Context, args []string) error {
		dst := args[len(args)-1]
		if strings.HasSuffix(dst, "/boot/") {
			writeFile(t, filepath.Join(dst, "cmdline.txt"), "root=PARTUUID=aaaaaaaa-02\n")
		}
		return nil
	}
	run, _ := fe.srv.backup.begin(context.Background(), "download", "Browser")
	_, _, err := fe.srv.openFiles(run.ctx, run, filesTarget{dir: fe.cache, name: "hmikiosk-test.img"})
	if err == nil || !strings.Contains(err.Error(), "aaaaaaaa") || !strings.Contains(err.Error(), "root=PARTUUID=aaaaaaaa-02") || !strings.Contains(err.Error(), "cmdline.txt") {
		t.Fatalf("err: %v", err)
	}
	fe.wantClean(t)
	run.end(err, "")
}

func TestFilesDownloadSpaceCheck(t *testing.T) {
	fe := newFilesEnv(t)
	p, _ := fe.srv.planFiles(context.Background())
	fe.srv.Backup.FreeBytes = func(string) (int64, error) { return p.rawSize + 512*mib - 1, nil }
	run, _ := fe.srv.backup.begin(context.Background(), "download", "Browser")
	_, _, err := fe.srv.openFiles(run.ctx, run, filesTarget{dir: fe.cache, name: "x.img"})
	run.end(err, "")
	if err == nil || !strings.Contains(err.Error(), "nicht genug Platz") || !strings.Contains(err.Error(), "GB") {
		t.Fatalf("err: %v", err)
	}
	if hasCall(fe.calls(), "losetup") {
		t.Error("Bau trotz fehlendem Platz gestartet")
	}
	info := fe.srv.backupInfo(context.Background())
	if info.DownloadProblem == "" || info.Problem != "" || info.RawBytes != p.rawSize || !info.Files {
		t.Errorf("Info: %+v", info)
	}
	fe.srv.Backup.FreeBytes = func(string) (int64, error) { return p.rawSize + 512*mib, nil }
	info = fe.srv.backupInfo(context.Background())
	if info.DownloadProblem != "" || info.CardFree != p.rawSize+512*mib || info.NeedCard != p.rawSize+512*mib || info.NeedUSB != p.usbNeed() {
		t.Errorf("Info: %+v", info)
	}
}

func TestFilesInfoWithoutToolsAsksForInstall(t *testing.T) {
	fe := newFilesEnv(t)
	fe.srv.Backup.HasTool = func(n string) bool { return n != "rsync" && n != "mkfs.vfat" }
	info := fe.srv.backupInfo(context.Background())
	if !strings.Contains(info.Problem, "rsync") || !strings.Contains(info.Problem, "mkfs.vfat") || !strings.Contains(info.Problem, "apt") {
		t.Errorf("Info: %+v", info)
	}
}

func TestFilesWorkDirRefusesLinksAndForeignOwner(t *testing.T) {
	fe := newFilesEnv(t)
	target := t.TempDir()
	symlink(t, target, fe.cache)
	run, _ := fe.srv.backup.begin(context.Background(), "download", "Browser")
	_, _, err := fe.srv.openFiles(run.ctx, run, filesTarget{dir: fe.cache, name: "x.img"})
	run.end(err, "")
	if err == nil {
		t.Fatal("Verweis als Arbeitsordner angenommen")
	}
	if es, _ := os.ReadDir(target); len(es) != 0 {
		t.Errorf("Ziel des Verweises beschrieben: %v", es)
	}
	if hasCall(fe.calls(), "losetup") {
		t.Error("Bau trotz Verweis")
	}
}

func TestFilesWorkDirIsCreatedRootOnly(t *testing.T) {
	fe := newFilesEnv(t)
	dir, err := fe.srv.ensureWorkDir(fe.cache)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(dir)
	if err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 {
		t.Fatalf("Arbeitsordner: %v %v", fi, err)
	}
	// Ein vorhandener, zu offener Ordner wird auf 0700 gesetzt.
	os.Chmod(dir, 0o755)
	if _, err := fe.srv.ensureWorkDir(fe.cache); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Lstat(dir); fi.Mode().Perm() != 0o700 {
		t.Errorf("Rechte: %v", fi.Mode())
	}
}

// --- gesamter Weg über den Helfer ---

func hashOf(r io.Reader) ([32]byte, int64) {
	h := sha256.New()
	n, _ := io.Copy(h, r)
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum, n
}

func TestFilesDownloadEndToEnd(t *testing.T) {
	fe := newFilesEnv(t)
	fe.usedRoot = 0
	var sizeSeen int64
	fe.srv.Backup.onChunk = func(int64) {}
	cl := helperapi.New(startServer(t, fe.env))
	size, rc, err := cl.BackupStream(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	zr := gunzipReader(t, rc)
	sum, n := hashOf(zr)
	rc.Close()
	sizeSeen = size
	if n != size {
		t.Fatalf("entpackt %d, Kopf %d", n, size)
	}
	st := waitStopped(t, cl)
	if st.Error != "" || !st.Finished || st.Phase != "Fertig" || st.ReadBytes != sizeSeen || st.TotalBytes != sizeSeen || st.Mode != "download" {
		t.Fatalf("Status: %+v", st)
	}
	// Erster Sektor: eine gültige Tabelle mit neuer Disk-ID (Rest ist Sparse-Null).
	var zero [32]byte
	if sum == zero {
		t.Error("kein Hash")
	}
	fe.wantClean(t)
}

func gunzipReader(t *testing.T, r io.Reader) io.Reader {
	t.Helper()
	zr, err := gzip.NewReader(r)
	if err != nil {
		t.Fatal(err)
	}
	return zr
}

func TestFilesUSBEndToEnd(t *testing.T) {
	fe := newFilesEnv(t)
	fe.usedRoot = 0
	root := t.TempDir()
	mnt := filepath.Join(root, "usb")
	b := &fe.srv.Backup
	b.MountInfo = filepath.Join(root, "mountinfo")
	b.SysClassBlock = filepath.Join(root, "sys", "class", "block")
	b.SysBlock = filepath.Join(root, "sys", "block")
	b.UsbMount = mnt
	writeFile(t, b.MountInfo, "22 1 179:2 / / ro - ext4 /dev/mmcblk9p2 ro\n35 22 179:1 / /boot/firmware ro - vfat /dev/mmcblk9p1 ro\n")
	sysTree(t, filepath.Join(root, "sys"), usbPath, "sda", "sda1")
	sysTree(t, filepath.Join(root, "sys"), mmcPath, "mmcblk9", "mmcblk9p1", "mmcblk9p2")
	fe.ff.blkid["/dev/sda1"] = "exfat\n"
	var stickFreeAtMount int64
	b.FreeBytes = func(p string) (int64, error) {
		if p == mnt {
			return stickFreeAtMount, nil
		}
		return 100 << 30, nil
	}
	p, _ := fe.srv.planFiles(context.Background())
	// Die ExecOut-Ersatzfunktion beantwortet blkid -o value ... für den Stick.
	orig := fe.srv.ExecOut
	fe.srv.ExecOut = func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "blkid" && args[0] == "-o" {
			fe.fx.mu.Lock()
			fe.fx.calls = append(fe.fx.calls, "blkid "+strings.Join(args, " "))
			fe.fx.mu.Unlock()
			return "exfat\n", nil
		}
		return orig(ctx, name, args...)
	}
	cl := helperapi.New(startServer(t, fe.env))

	// Zu wenig Platz: Ablehnung vorab mit der nötigen Größe, nichts gebaut.
	stickFreeAtMount = p.usbNeed() - 1
	err := cl.BackupUSB(context.Background(), "Presse-1")
	if err == nil || !strings.Contains(err.Error(), "nicht genug Platz") || !strings.Contains(err.Error(), "nötig") {
		t.Fatalf("zu wenig Platz: %v", err)
	}
	if hasCall(fe.calls(), "losetup") || waitStopped(t, cl).Finished {
		t.Fatalf("trotz zu wenig Platz gebaut: %v", fe.calls())
	}
	stickFreeAtMount = p.usbNeed()
	if err := cl.BackupUSB(context.Background(), "Presse-1"); err != nil {
		t.Fatal(err)
	}
	st := waitStopped(t, cl)
	const name = "hmikiosk-Presse-1-2026-11-05-0907.img.gz"
	if st.Error != "" || !st.Finished || st.File != name || st.Phase != "Fertig" || st.ReadBytes != p.rawSize {
		t.Fatalf("Status: %+v", st)
	}
	f, err := os.Open(filepath.Join(mnt, name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, n := hashOf(gunzipReader(t, f)); n != p.rawSize {
		t.Fatalf("entpackt %d, erwartet %d", n, p.rawSize)
	}
	es, _ := os.ReadDir(mnt)
	if len(es) != 1 {
		t.Errorf("Stick enthält mehr als die Sicherung: %v", es)
	}
	// Die Arbeitsdatei lag auf dem Stick, nicht auf der Karte.
	if !hasCall(fe.calls(), "losetup -P --show -f "+mnt+"/hmikiosk-Presse-1-2026-11-05-0907.img.part") {
		t.Errorf("Arbeitsdatei: %v", fe.calls())
	}
	if es, _ := os.ReadDir(fe.cache); len(es) != 0 {
		t.Errorf("Karte beschrieben: %v", es)
	}
}

func TestFilesPhasesAreReported(t *testing.T) {
	fe := newFilesEnv(t)
	copyPollEvery = 5 * time.Millisecond
	t.Cleanup(func() { copyPollEvery = time.Second })
	var grow atomic.Int64
	fe.srv.Backup.FsUsage = func(path string) (int64, int64, error) {
		if strings.HasPrefix(path, fe.imgRun) {
			return grow.Add(100 << 20), 10, nil // Kopierfortschritt
		}
		return fe.usedRoot, fe.inodes, nil
	}
	hold := make(chan struct{})
	reached := make(chan struct{})
	fe.ff.rsync = func(ctx context.Context, args []string) error {
		if strings.HasSuffix(args[len(args)-1], "/root/") {
			close(reached)
			select {
			case <-hold:
			case <-ctx.Done():
			}
		}
		return nil
	}
	run, _ := fe.srv.backup.begin(context.Background(), "download", "Browser")
	if st := fe.srv.backup.status(); st.Phase != "Vorbereiten" {
		t.Errorf("Anfang: %q", st.Phase)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		rc, _, err := fe.srv.openFiles(run.ctx, run, filesTarget{dir: fe.cache, name: "hmikiosk-test.img"})
		if err == nil {
			rc.Close()
		}
	}()
	<-reached
	deadline := time.Now().Add(5 * time.Second)
	for {
		st := fe.srv.backup.status()
		if st.Phase == "Kopieren" && st.TotalBytes == fe.usedRoot && st.ReadBytes > 0 && st.ReadBytes <= st.TotalBytes {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Status: %+v", st)
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(hold)
	<-done
	run.end(nil, "")
}

// --- Aufräumen beim Start ---

func TestCleanupStaleFilesLeftovers(t *testing.T) {
	fe := newFilesEnv(t)
	root := t.TempDir()
	mnt := filepath.Join(root, "usb")
	b := &fe.srv.Backup
	b.UsbMount = mnt
	b.MountInfo = filepath.Join(root, "mountinfo")
	writeFile(t, b.MountInfo, "50 22 7:1 / "+fe.imgRun+"/root rw - ext4 /dev/loop7p2 rw\n51 22 7:2 / "+fe.imgRun+"/boot rw - vfat /dev/loop7p1 rw\n"+
		"52 22 8:1 / "+mnt+" rw - exfat /dev/sda1 rw\n")
	mkdirs(t, fe.imgRun+"/root")
	mkdirs(t, fe.imgRun+"/boot")
	mkdirs(t, fe.cache)
	writeFile(t, filepath.Join(fe.cache, "hmikiosk-download-x.img"), "x")
	writeFile(t, filepath.Join(fe.cache, "fremd.txt"), "bleibt")
	writeFile(t, filepath.Join(mnt, "hmikiosk-A-2026.img.part"), "x")
	writeFile(t, filepath.Join(mnt, "hmikiosk-A-2026.img.gz.part"), "x")
	writeFile(t, filepath.Join(mnt, "fremd.img.part"), "bleibt")
	writeFile(t, filepath.Join(mnt, "hmikiosk-A-2026.img.gz"), "bleibt")
	// Eine fremde Loop-Datei darf nicht gelöst werden.
	fe.srv.ExecOut = func(ctx context.Context, name string, args ...string) (string, error) {
		fe.ff.call(ctx, name, args...)
		if name == "losetup" && args[0] == "-l" {
			return "/dev/loop7 " + fe.cache + "/hmikiosk-download-x.img\n/dev/loop8 " + mnt + "/hmikiosk-A-2026.img.part (deleted)\n" +
				"/dev/loop9 /srv/fremd.img\n/dev/loop10 " + mnt + "/andere/hmikiosk-B.img\n", nil
		}
		return "", nil
	}
	fe.srv.CleanupStale(context.Background())
	calls := fe.calls()
	order := []string{"umount " + fe.imgRun + "/root", "umount " + fe.imgRun + "/boot", "losetup -d /dev/loop7", "losetup -d /dev/loop8", "umount " + mnt}
	if !subsequence(calls, order) {
		t.Fatalf("Aufrufe:\n%s\nerwartet:\n%s", strings.Join(calls, "\n"), strings.Join(order, "\n"))
	}
	for _, c := range calls {
		if strings.Contains(c, "loop9") || strings.Contains(c, "loop10") {
			t.Errorf("fremdes Loop-Gerät angefasst: %s", c)
		}
	}
	for _, p := range []string{filepath.Join(fe.cache, "hmikiosk-download-x.img"), filepath.Join(mnt, "hmikiosk-A-2026.img.part"), filepath.Join(mnt, "hmikiosk-A-2026.img.gz.part")} {
		if _, err := os.Lstat(p); err == nil {
			t.Errorf("%s nicht gelöscht", p)
		}
	}
	for _, p := range []string{filepath.Join(fe.cache, "fremd.txt"), filepath.Join(mnt, "fremd.img.part"), filepath.Join(mnt, "hmikiosk-A-2026.img.gz")} {
		if _, err := os.Lstat(p); err != nil {
			t.Errorf("%s gelöscht: %v", p, err)
		}
	}
	for _, d := range []string{"root", "boot"} {
		if _, err := os.Stat(filepath.Join(fe.imgRun, d)); err == nil {
			t.Errorf("Ordner %s übrig", d)
		}
	}
}

// Ließ sich ein Ziel nicht aushängen, darf nichts darunter gelöscht werden.
func TestCleanupStaleKeepsMountedImageContent(t *testing.T) {
	fe := newFilesEnv(t)
	b := &fe.srv.Backup
	b.UsbMount = filepath.Join(t.TempDir(), "usb")
	b.MountInfo = filepath.Join(t.TempDir(), "mountinfo")
	writeFile(t, b.MountInfo, "50 22 7:1 / "+fe.imgRun+"/root rw - ext4 /dev/loop7p2 rw\n")
	writeFile(t, filepath.Join(fe.imgRun, "root", "wichtig"), "x")
	fe.ff.failOn = func(n string, _ []string) error {
		if n == "umount" {
			return errors.New("busy")
		}
		return nil
	}
	fe.srv.CleanupStale(context.Background())
	if _, err := os.Stat(filepath.Join(fe.imgRun, "root", "wichtig")); err != nil {
		t.Errorf("Inhalt gelöscht: %v", err)
	}
}

func TestFilesManyInodesGetExplicitCount(t *testing.T) {
	fe := newFilesEnv(t)
	fe.inodes = 500000 // mehr als 1288 MiB / 16 KiB
	p, err := fe.srv.planFiles(context.Background())
	if err != nil || p.inodes != 625000+1024 {
		t.Fatalf("Inodes: %+v %v", p, err)
	}
	fe.inodes = 10000
	if p, _ = fe.srv.planFiles(context.Background()); p.inodes != 0 {
		t.Errorf("-i genügt, -N nicht nötig: %d", p.inodes)
	}
}

// Scheitert losetup, nachdem es die Datei angehängt hat, wird das Loop-Gerät über die Datei gefunden und
// gelöst, bevor sie gelöscht wird.
func TestFilesLosetupErrorDetachesLoopOfWorkFile(t *testing.T) {
	fe := newFilesEnv(t)
	fe.ff.attachThenFail = true
	run, _ := fe.srv.backup.begin(context.Background(), "download", "Browser")
	_, _, err := fe.srv.openFiles(run.ctx, run, filesTarget{dir: fe.cache, name: "hmikiosk-test.img"})
	run.end(err, "")
	if err == nil {
		t.Fatal("kein Fehler")
	}
	raw := filepath.Join(fe.cache, "hmikiosk-test.img")
	calls := fe.calls()
	if !subsequence(calls, []string{"losetup -P --show -f " + raw, "losetup -j " + raw, "losetup -d /dev/loop7"}) {
		t.Fatalf("Aufrufe: %v", calls)
	}
	fe.wantClean(t)
}

// Lässt sich das Loop-Gerät nicht lösen, bleibt die Arbeitsdatei bestehen (sie wird nicht unter dem Gerät gelöscht).
func TestFilesFileIsKeptWhenDetachFails(t *testing.T) {
	fe := newFilesEnv(t)
	fe.ff.failOn = func(n string, args []string) error {
		if n == "rsync" {
			return errors.New("kaputt")
		}
		if n == "losetup" && args[0] == "-d" {
			return errors.New("busy")
		}
		return nil
	}
	run, _ := fe.srv.backup.begin(context.Background(), "download", "Browser")
	_, _, err := fe.srv.openFiles(run.ctx, run, filesTarget{dir: fe.cache, name: "hmikiosk-test.img"})
	run.end(err, "")
	if err == nil {
		t.Fatal("kein Fehler")
	}
	if _, serr := os.Lstat(filepath.Join(fe.cache, "hmikiosk-test.img")); serr != nil {
		t.Errorf("Arbeitsdatei trotz angehängtem Loop-Gerät gelöscht: %v", serr)
	}
}

func TestFilesUSBNeedCoversRawAndCompressed(t *testing.T) {
	p := &filesPlan{rawSize: 10 << 30}
	// Rohdatei plus komprimierte Datei (bis 70 % der Rohgröße bei vollem System) plus Reserve.
	if want := int64(10<<30) + 7<<30 + 256*mib; p.usbNeed() != want {
		t.Errorf("usbNeed %d, erwartet %d", p.usbNeed(), want)
	}
}

func TestFilesRsyncUsesSparse(t *testing.T) {
	fe := newFilesEnv(t)
	var args []string
	fe.ff.rsync = func(_ context.Context, a []string) error {
		if strings.HasSuffix(a[len(a)-1], "/root/") {
			args = a
		}
		dst := a[len(a)-1]
		if strings.HasSuffix(dst, "/boot/") {
			writeFile(t, filepath.Join(dst, "cmdline.txt"), piCmdline)
		} else {
			writeFile(t, filepath.Join(dst, "etc", "fstab"), piFstab)
		}
		return nil
	}
	run, _ := fe.srv.backup.begin(context.Background(), "download", "Browser")
	rc, _, err := fe.srv.openFiles(run.ctx, run, filesTarget{dir: fe.cache, name: "hmikiosk-test.img"})
	if err != nil {
		t.Fatal(err)
	}
	rc.Close()
	run.end(nil, "")
	found := false
	for _, a := range args {
		found = found || a == "-S"
	}
	if !found {
		t.Errorf("rsync ohne -S: %v", args)
	}
}
