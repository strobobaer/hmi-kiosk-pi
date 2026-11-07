package helper

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hmikiosk/internal/helperapi"
)

const roMountInfo = `22 1 179:2 / /media/root-ro ro,relatime - ext4 /dev/mmcblk9p2 ro
23 22 179:1 / /boot/firmware ro,relatime - vfat /dev/mmcblk9p1 ro,fmask=0022
24 1 0:40 / / rw,relatime - overlay overlayroot rw,lowerdir=/media/root-ro
25 24 179:3 / /data rw,relatime - ext4 /dev/mmcblk9p3 rw
`

// Ist Boot oder System irgendwo schreibbar eingehängt, wäre die Rohkopie nicht stimmig.
func TestCompactImageRefusesWritableBootOrRoot(t *testing.T) {
	const want = "Der Schreibschutz ist ausgeschaltet. Eine SD-Sicherung ist nur mit eingeschaltetem Schreibschutz möglich, sonst wäre die Kopie unbrauchbar."
	for name, info := range map[string]string{
		"System rw":                           "22 1 179:2 / / rw,relatime - ext4 /dev/mmcblk9p2 rw\n23 22 179:1 / /boot/firmware ro,relatime - vfat /dev/mmcblk9p1 ro\n",
		"Boot rw":                             "22 1 179:2 / /media/root-ro ro,relatime - ext4 /dev/mmcblk9p2 ro\n23 22 179:1 / /boot/firmware rw,relatime - vfat /dev/mmcblk9p1 rw\n",
		"zweiter rw":                          roMountInfo + "30 24 179:2 / /mnt/x rw,relatime - ext4 /dev/mmcblk9p2 rw\n",
		"System ohne":                         "",
		"System als /dev/root rw":             "22 1 179:2 / / rw,relatime - ext4 /dev/root rw\n23 22 179:1 / /boot/firmware ro - vfat /dev/mmcblk9p1 ro\n",
		"Overlay, Boot rw über anderen Namen": roMountInfo + "31 24 179:1 / /mnt/boot rw - vfat /dev/disk/by-uuid/ABCD rw\n",
	} {
		t.Run(name, func(t *testing.T) {
			c := newCompactEnv(t)
			writeFile(t, c.srv.Backup.MountInfo, info)
			_, _, err := c.srv.openCompact(context.Background())
			if name == "System ohne" { // nichts eingehängt: nichts schreibbar
				if err != nil {
					t.Fatalf("err: %v", err)
				}
				return
			}
			if err == nil || err.Error() != want {
				t.Fatalf("err: %v", err)
			}
			if len(c.fx.got()) != 0 {
				t.Errorf("Aufrufe: %v", c.fx.got())
			}
			dirEmpty(t, c.run)
			if bi := c.srv.backupInfo(context.Background()); bi.Problem != want || bi.RawBytes != 0 {
				t.Errorf("Info: %+v", bi)
			}
		})
	}
}

func TestCompactImageUnreadableMountinfoRefuses(t *testing.T) {
	c := newCompactEnv(t)
	c.srv.Backup.MountInfo = filepath.Join(t.TempDir(), "fehlt")
	if _, _, err := c.srv.openCompact(context.Background()); err == nil {
		t.Fatal("kein Fehler")
	}
}

// Schließt der Dienst, während das Abbild gebaut wird, endet der Bau und die Sperre wird frei.
func TestBackupStreamClientCloseDuringBuildCancelsAndFreesLock(t *testing.T) {
	linuxOnly(t)
	e, _ := backupEnv(t)
	started := make(chan struct{})
	seen := make(chan error, 1)
	e.srv.Backup.OpenSource = func(ctx context.Context) (io.ReadCloser, int64, error) {
		close(started)
		select {
		case <-ctx.Done():
			seen <- ctx.Err()
			return nil, 0, ctx.Err()
		case <-time.After(20 * time.Second):
			seen <- nil
			return nil, 0, io.EOF
		}
	}
	cl := helperapi.New(startServer(t, e))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { cl.BackupStream(ctx); close(done) }()
	<-started
	cancel()
	<-done
	select {
	case err := <-seen:
		if err == nil {
			t.Fatal("Bau wurde nicht abgebrochen")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Bau läuft weiter")
	}
	if st := waitStopped(t, cl); st.Running || st.Finished {
		t.Fatalf("Status: %+v", st)
	}
}

func TestBackupTimeoutsAlign(t *testing.T) {
	worst := openTimeout + commandTimeout // Bau plus sync
	if helperapi.BackupHeaderTimeout < worst+30*time.Second || helperapi.BackupHeaderTimeout > worst+time.Minute {
		t.Fatalf("Kopfzeile %v, Bau %v", helperapi.BackupHeaderTimeout, openTimeout)
	}
}

// Liefert die Quelle weniger Bytes als angekündigt, gilt die Sicherung nie als fertig.
func TestBackupShortSourceFails(t *testing.T) {
	linuxOnly(t)
	e, data := backupEnv(t)
	e.srv.Backup.OpenSource = func(context.Context) (io.ReadCloser, int64, error) {
		return io.NopCloser(bytes.NewReader(data[:len(data)-5000])), int64(len(data)), nil
	}
	cl := helperapi.New(startServer(t, e))
	_, rc, err := cl.BackupStream(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, rc)
	rc.Close()
	st := waitStopped(t, cl)
	if st.Finished || st.Error == "" {
		t.Fatalf("Status: %+v", st)
	}
}

// Viele kleine Dateien: genug Inodes, feste Dichte 16384 Byte je Inode.
func TestCompactImageManySmallFiles(t *testing.T) {
	c := newCompactEnv(t)
	const n = 20000
	for i := 0; i < n; i++ {
		d := filepath.Join(c.data, fmt.Sprintf("d%d", i%50))
		if i < 50 {
			mkdirs(t, d)
		}
		if err := os.WriteFile(filepath.Join(d, fmt.Sprintf("f%d", i)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rc, size, err := c.srv.openCompact(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := readAll(t, rc)
	rc.Close()
	calls := c.fx.got()
	if len(calls) < 1 || !strings.Contains(calls[0], " -i 16384 ") || !strings.Contains(calls[0], " -N 40") {
		t.Errorf("mkfs-Aufruf: %q", calls)
	}
	p3 := int64(out[446+32+8]) | int64(out[446+32+9])<<8 | int64(out[446+32+10])<<16
	img := filepath.Join(t.TempDir(), "p3.img")
	if err := os.WriteFile(img, out[p3*512:size], 0o600); err != nil {
		t.Fatal(err)
	}
	st := debugfs(t, img, "stats")
	var inodes, free int
	for _, l := range strings.Split(st, "\n") {
		fmt.Sscanf(l, "Inode count: %d", &inodes)
		fmt.Sscanf(l, "Free inodes: %d", &free)
	}
	if inodes < 2*n || free < n {
		t.Errorf("Inodes %d, frei %d", inodes, free)
	}
	if o := debugfs(t, img, "stat /d7/f57"); !strings.Contains(o, "Size: 1") {
		t.Errorf("Datei fehlt: %s", firstLines(o, 3))
	}
}

func TestDataUsedCountsEntries(t *testing.T) {
	linuxOnly(t)
	dir := t.TempDir()
	mkdirs(t, filepath.Join(dir, "a"))
	writeFile(t, filepath.Join(dir, "a", "x"), "x")
	symlink(t, "x", filepath.Join(dir, "l"))
	_, n, err := dataUsed(context.Background(), dir)
	if err != nil || n != 3 {
		t.Fatalf("Einträge %d %v", n, err)
	}
}

// Mit Overlay erscheint die untere Ebene unter /dev/root nur lesbar: erlaubt.
func TestCompactImageOverlayWithDevRootReadOnlyIsFine(t *testing.T) {
	c := newCompactEnv(t)
	writeFile(t, c.srv.Backup.MountInfo, "22 1 179:2 / /media/root-ro ro,relatime - ext4 /dev/root ro\n23 22 179:1 / /boot/firmware ro - vfat /dev/mmcblk9p1 ro\n24 1 0:40 / / rw - overlay overlayroot rw\n")
	rc, _, err := c.srv.openCompact(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	rc.Close()
}

func TestCompactImageMissingSysfsRefuses(t *testing.T) {
	c := newCompactEnv(t)
	c.srv.Backup.SysClassBlock = filepath.Join(t.TempDir(), "fehlt")
	if _, _, err := c.srv.openCompact(context.Background()); err == nil {
		t.Fatal("kein Fehler")
	}
}

func TestBackupClientAbortGivesGermanStatus(t *testing.T) {
	linuxOnly(t)
	e, _ := backupEnv(t)
	cl := helperapi.New(startServer(t, e))
	_, rc, err := cl.BackupStream(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	io.ReadFull(rc, make([]byte, 100))
	rc.Close()
	st := waitStopped(t, cl)
	if st.Error != "Die Verbindung zum Browser wurde getrennt." {
		t.Fatalf("Status: %+v", st)
	}
}

func TestBackupClientCloseDuringBuildGivesGermanStatus(t *testing.T) {
	c := newCompactEnv(t)
	real := c.srv.exec
	started := make(chan struct{})
	c.srv.exec = func(ctx context.Context, name string, args ...string) error {
		if name == "mkfs.ext4" {
			close(started)
			<-ctx.Done()
			return fmt.Errorf("signal: killed: %v", ctx.Err())
		}
		return real(ctx, name, args...)
	}
	cl := helperapi.New(startServer(t, c.env))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { cl.BackupStream(ctx); close(done) }()
	<-started
	cancel()
	<-done
	st := waitStopped(t, cl)
	if st.Error != "Die Verbindung zum Browser wurde getrennt." || strings.Contains(st.Error, "context") {
		t.Fatalf("Status: %+v", st)
	}
	dirEmpty(t, c.run)
}

func TestImageBudgetCountsInodeTables(t *testing.T) {
	c := newCompactEnv(t)
	for i := 0; i < 2000; i++ {
		writeFile(t, filepath.Join(c.data, "d", fmt.Sprintf("f%d", i)), "x")
	}
	used, n, err := dataUsed(context.Background(), c.data)
	if err != nil {
		t.Fatal(err)
	}
	p, dev, err := c.srv.planCompact(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	dev.Close()
	if want := imageSize(used + n*512); p.dataSize != want {
		t.Fatalf("Datenpartition %d, erwartet %d", p.dataSize, want)
	}
}
