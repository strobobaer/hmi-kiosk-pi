package helper

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"hmikiosk/internal/helperapi"
)

const busyMsg = "Eine SD-Sicherung läuft gerade."

// makeCard schreibt eine Ersatz-"Karte": Zufallsblöcke im Wechsel mit Nullblöcken.
func makeCard(t *testing.T, size int) (string, []byte) {
	t.Helper()
	data := make([]byte, size)
	const blk = 256 << 10
	for off := 0; off < size; off += 2 * blk {
		end := min(off+blk, size)
		if _, err := rand.Read(data[off:end]); err != nil {
			t.Fatal(err)
		}
	}
	p := filepath.Join(t.TempDir(), "card.img")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p, data
}

// rawFileSource liest eine Datei unverändert (statt des kompakten Formats) als Quelle.
func rawFileSource(path string) OpenSource {
	return func(context.Context) (io.ReadCloser, int64, error) {
		f, err := os.Open(path)
		if err != nil {
			return nil, 0, err
		}
		size, err := f.Seek(0, io.SeekEnd)
		if err == nil {
			_, err = f.Seek(0, io.SeekStart)
		}
		if err != nil {
			f.Close()
			return nil, 0, err
		}
		return f, size, nil
	}
}

func gunzipAll(t *testing.T, r io.Reader) []byte {
	t.Helper()
	zr, err := gzip.NewReader(r)
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gzip lesen: %v", err)
	}
	return b
}

func backupEnv(t *testing.T) (*env, []byte) {
	t.Helper()
	e := newEnv(t, "[all]\n")
	card, data := makeCard(t, 20<<20)
	e.srv.Backup.OpenSource = rawFileSource(card)
	e.srv.Backup.IdleTimeout = 2 * time.Second
	return e, data
}

func waitStopped(t *testing.T, cl *helperapi.Client) helperapi.BackupStatus {
	t.Helper()
	for i := 0; i < 500; i++ {
		st, err := cl.BackupStatus(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !st.Running {
			return st
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Sicherung endet nicht")
	return helperapi.BackupStatus{}
}

func TestBackupStreamRoundTrip(t *testing.T) {
	linuxOnly(t)
	e, data := backupEnv(t)
	cl := helperapi.New(startServer(t, e))
	size, rc, err := cl.BackupStream(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if size != int64(len(data)) {
		t.Fatalf("Größe %d, erwartet %d", size, len(data))
	}
	got := gunzipAll(t, rc)
	rc.Close()
	if !bytes.Equal(got, data) {
		t.Fatal("entpackte Daten weichen ab")
	}
	st := waitStopped(t, cl)
	if st.Error != "" || !st.Finished || st.ReadBytes != size || st.TotalBytes != size || st.Mode != "download" || st.Phase != "Fertig" {
		t.Fatalf("Status: %+v", st)
	}
	if calls := e.fx.got(); len(calls) != 1 || calls[0] != "sync" {
		t.Fatalf("Aufrufe: %v", calls)
	}
}

func TestBackupStreamSyncFailureIsReported(t *testing.T) {
	linuxOnly(t)
	e, _ := backupEnv(t)
	e.fx.fail["sync"] = io.ErrUnexpectedEOF
	cl := helperapi.New(startServer(t, e))
	_, _, err := cl.BackupStream(context.Background())
	if err == nil || !strings.Contains(err.Error(), "sync fehlgeschlagen") {
		t.Fatalf("err: %v", err)
	}
	if st := waitStopped(t, cl); st.Running || st.Finished {
		t.Fatalf("Status: %+v", st)
	}
	// Die Sperre ist wieder frei.
	e.fx.mu.Lock()
	e.fx.fail["sync"] = nil
	e.fx.mu.Unlock()
	_, rc, err := cl.BackupStream(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	rc.Close()
}

func TestBackupClientAbortReleasesLock(t *testing.T) {
	linuxOnly(t)
	e, _ := backupEnv(t)
	cl := helperapi.New(startServer(t, e))
	_, rc, err := cl.BackupStream(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(rc, make([]byte, 1000)); err != nil {
		t.Fatal(err)
	}
	rc.Close()
	st := waitStopped(t, cl)
	if st.Finished || st.Error == "" {
		t.Fatalf("Status: %+v", st)
	}
	_, rc, err = cl.BackupStream(context.Background())
	if err != nil {
		t.Fatalf("zweiter Versuch: %v", err)
	}
	rc.Close()
}

func TestBackupRejectsSecondAndBlocksCommands(t *testing.T) {
	linuxOnly(t)
	e, _ := backupEnv(t)
	cl := helperapi.New(startServer(t, e))
	_, rc, err := cl.BackupStream(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()

	if _, _, err := cl.BackupStream(context.Background()); err == nil || err.Error() != busyMsg {
		t.Fatalf("zweiter Strom: %v", err)
	}
	if err := cl.BackupUSB(context.Background(), "Presse"); err == nil || err.Error() != busyMsg {
		t.Fatalf("USB: %v", err)
	}
	before := len(e.fx.got())
	for _, c := range []struct{ cmd, arg string }{
		{CmdReboot, ""}, {CmdRestartService, ""}, {CmdUpdateActivate, "1.2.3"}, {CmdUpdateRollback, ""},
		{CmdWifiOff, ""}, {CmdWifiOn, ""}, {CmdBtOff, ""}, {CmdBtOn, ""},
	} {
		if err := cl.Call(context.Background(), c.cmd, c.arg); err == nil || err.Error() != busyMsg {
			t.Errorf("%s: %v", c.cmd, err)
		}
	}
	if got := len(e.fx.got()); got != before {
		t.Errorf("Befehle trotz Sicherung ausgeführt: %v", e.fx.got()[before:])
	}
	if len(e.upd.activated) != 0 || e.upd.rollbacks != 0 {
		t.Error("Update trotz Sicherung")
	}
	if st, err := cl.BackupStatus(context.Background()); err != nil || !st.Running {
		t.Fatalf("Status: %+v %v", st, err)
	}
}

func TestBackupCommandsAllowedAfterBackupEnds(t *testing.T) {
	linuxOnly(t)
	e, _ := backupEnv(t)
	cl := helperapi.New(startServer(t, e))
	_, rc, err := cl.BackupStream(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	rc.Close()
	waitStopped(t, cl)
	if err := cl.Call(context.Background(), CmdReboot, ""); err != nil {
		t.Fatalf("reboot nach der Sicherung: %v", err)
	}
}

func TestBackupCancelStopsStream(t *testing.T) {
	linuxOnly(t)
	e, data := backupEnv(t)
	cl := helperapi.New(startServer(t, e))
	size, rc, err := cl.BackupStream(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	if err := cl.BackupCancel(context.Background()); err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(rc)
	if err == nil {
		_, err = io.ReadAll(zr)
	}
	if err == nil {
		t.Fatal("abgebrochener Strom endete sauber")
	}
	st := waitStopped(t, cl)
	if st.Finished || st.Error != "Die Sicherung wurde abgebrochen." || st.ReadBytes >= size || size != int64(len(data)) {
		t.Fatalf("Status: %+v", st)
	}
}

func TestBackupCancelWithoutBackup(t *testing.T) {
	linuxOnly(t)
	e, _ := backupEnv(t)
	cl := helperapi.New(startServer(t, e))
	if err := cl.BackupCancel(context.Background()); err == nil || err.Error() != "Es läuft keine SD-Sicherung." {
		t.Fatalf("err: %v", err)
	}
}

func TestBackupStreamIdleTimeoutFreesLock(t *testing.T) {
	linuxOnly(t)
	e, _ := backupEnv(t)
	e.srv.Backup.IdleTimeout = 150 * time.Millisecond
	cl := helperapi.New(startServer(t, e))
	_, rc, err := cl.BackupStream(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close() // wird nicht gelesen
	st := waitStopped(t, cl)
	if st.Finished || !strings.Contains(st.Error, "nicht abgeholt") {
		t.Fatalf("Status: %+v", st)
	}
}

func TestBackupStatusInitiallyIdle(t *testing.T) {
	linuxOnly(t)
	e, _ := backupEnv(t)
	cl := helperapi.New(startServer(t, e))
	st, err := cl.BackupStatus(context.Background())
	if err != nil || st.Running || st.Finished || st.Mode != "" {
		t.Fatalf("Status: %+v %v", st, err)
	}
}

func TestBackupMissingSourceIsReported(t *testing.T) {
	linuxOnly(t)
	e, _ := backupEnv(t)
	e.srv.Backup.OpenSource = nil
	e.srv.Backup.Source = filepath.Join(t.TempDir(), "fehlt")
	e.srv.Backup.ImageRelease = imageMarker(t)
	cl := helperapi.New(startServer(t, e))
	_, _, err := cl.BackupStream(context.Background())
	if err == nil || !strings.Contains(err.Error(), "SD-Karte") {
		t.Fatalf("err: %v", err)
	}
}

// --- Geräteerkennung ---

func mkdirs(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, p, content string) {
	t.Helper()
	mkdirs(t, filepath.Dir(p))
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func symlink(t *testing.T, target, link string) {
	t.Helper()
	mkdirs(t, filepath.Dir(link))
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// sysTree legt einen Ausschnitt von /sys an: Gerät dev unter devPath, mit Partitionen parts.
func sysTree(t *testing.T, root, devPath, dev string, parts ...string) {
	t.Helper()
	real := filepath.Join(root, "devices", devPath, "block", dev)
	mkdirs(t, real)
	symlink(t, "../devices/"+devPath+"/block/"+dev, filepath.Join(root, "block", dev))
	symlink(t, "../../devices/"+devPath+"/block/"+dev, filepath.Join(root, "class", "block", dev))
	for i, p := range parts {
		writeFile(t, filepath.Join(real, p, "partition"), string(rune('1'+i))+"\n")
		symlink(t, "../../devices/"+devPath+"/block/"+dev+"/"+p, filepath.Join(root, "class", "block", p))
	}
}

func detectEnv(t *testing.T) (*Server, string) {
	t.Helper()
	e := newEnv(t, "")
	root := t.TempDir()
	b := &e.srv.Backup
	b.MountInfo = filepath.Join(root, "mountinfo")
	b.SysClassBlock = filepath.Join(root, "sys", "class", "block")
	b.SysBlock = filepath.Join(root, "sys", "block")
	b.DevDir = "/dev"
	return e.srv, filepath.Join(root, "sys")
}

const bootInfo = `22 1 179:2 / / ro,relatime - ext4 /dev/mmcblk0p2 ro
35 22 179:1 / /boot/firmware rw,relatime shared:9 - vfat /dev/mmcblk0p1 rw,fmask=0022
36 22 179:3 / /data rw,relatime - ext4 /dev/mmcblk0p3 rw
`

const (
	mmcPath = "platform/emmc2/mmc_host/mmc0/mmc0:0001"
	usbPath = "platform/xhci/usb1/1-1/1-1:1.0/host0/target0:0:0/0:0:0:0"
)

func TestBootDiskFromMountinfo(t *testing.T) {
	linuxOnly(t)
	s, sys := detectEnv(t)
	sysTree(t, sys, mmcPath, "mmcblk0", "mmcblk0p1", "mmcblk0p2", "mmcblk0p3")
	writeFile(t, s.Backup.MountInfo, bootInfo)
	name, err := s.bootDisk()
	if err != nil || name != "mmcblk0" {
		t.Fatalf("bootDisk: %q %v", name, err)
	}
}

func TestBootDiskWithoutPartitionTable(t *testing.T) {
	linuxOnly(t)
	s, sys := detectEnv(t)
	sysTree(t, sys, "platform/x/block", "loop7")
	writeFile(t, s.Backup.MountInfo, "35 22 7:7 / /boot/firmware rw - vfat /dev/loop7 rw\n")
	if name, err := s.bootDisk(); err != nil || name != "loop7" {
		t.Fatalf("bootDisk: %q %v", name, err)
	}
}

func TestBootDiskErrors(t *testing.T) {
	linuxOnly(t)
	s, _ := detectEnv(t)
	writeFile(t, s.Backup.MountInfo, "22 1 179:2 / / ro - ext4 /dev/mmcblk0p2 ro\n")
	if _, err := s.bootDisk(); err == nil {
		t.Fatal("ohne /boot/firmware kein Fehler")
	}
	writeFile(t, s.Backup.MountInfo, "35 22 179:1 / /boot/firmware rw - vfat ../../etc/passwd rw\n")
	if _, err := s.bootDisk(); err == nil {
		t.Fatal("unsicherer Gerätename akzeptiert")
	}
	writeFile(t, s.Backup.MountInfo, bootInfo)
	if _, err := s.bootDisk(); err == nil {
		t.Fatal("ohne /sys kein Fehler")
	}
}

func TestFindUSB(t *testing.T) {
	linuxOnly(t)
	s, sys := detectEnv(t)
	sysTree(t, sys, mmcPath, "mmcblk0", "mmcblk0p1", "mmcblk0p2")

	if _, err := s.findUSB("mmcblk0"); err == nil || err.Error() != "Kein USB-Stick gefunden. Bitte einen Stick einstecken." {
		t.Fatalf("ohne Stick: %v", err)
	}

	sysTree(t, sys, usbPath, "sda", "sda1", "sda2")
	if dev, err := s.findUSB("mmcblk0"); err != nil || dev != "/dev/sda1" {
		t.Fatalf("mit Stick: %q %v", dev, err)
	}
	// Bootet das Gerät selbst vom Stick, ist er kein Sicherungsziel.
	if _, err := s.findUSB("sda"); err == nil {
		t.Fatal("Systemgerät als Ziel angeboten")
	}

	sysTree(t, sys, "platform/xhci/usb1/1-2/1-2:1.0/host1/target1:0:0/1:0:0:0", "sdb")
	if _, err := s.findUSB("mmcblk0"); err == nil || !strings.Contains(err.Error(), "Mehrere") {
		t.Fatalf("zwei Sticks: %v", err)
	}
}

func TestFindUSBWholeDeviceWithoutPartitions(t *testing.T) {
	linuxOnly(t)
	s, sys := detectEnv(t)
	sysTree(t, sys, usbPath, "sdb")
	if dev, err := s.findUSB("mmcblk0"); err != nil || dev != "/dev/sdb" {
		t.Fatalf("%q %v", dev, err)
	}
}

// --- USB ---

type usbEnv struct {
	*env
	mnt  string
	card []byte
}

func newUSBEnv(t *testing.T, fstype string) *usbEnv {
	t.Helper()
	e, data := backupEnv(t)
	s := e.srv
	root := t.TempDir()
	s.Backup.MountInfo = filepath.Join(root, "mountinfo")
	s.Backup.SysClassBlock = filepath.Join(root, "sys", "class", "block")
	s.Backup.SysBlock = filepath.Join(root, "sys", "block")
	s.Backup.DevDir = "/dev"
	s.Backup.UsbMount = filepath.Join(root, "mnt")
	sysTree(t, filepath.Join(root, "sys"), mmcPath, "mmcblk0", "mmcblk0p1", "mmcblk0p2")
	sysTree(t, filepath.Join(root, "sys"), usbPath, "sda", "sda1")
	writeFile(t, s.Backup.MountInfo, bootInfo)
	s.Backup.FreeBytes = func(string) (int64, error) { return 100 << 30, nil }
	s.Backup.Now = func() time.Time { return time.Date(2026, 11, 5, 9, 7, 0, 0, time.Local) }
	s.ExecOut = func(ctx context.Context, name string, args ...string) (string, error) {
		if err := e.fx.exec(ctx, name, args...); err != nil {
			return "", err
		}
		if name == "blkid" {
			return fstype + "\n", nil
		}
		return "", nil
	}
	// Der Ersatz-Mount legt nichts an; UsbMount ist ein normaler Ordner.
	return &usbEnv{env: e, mnt: s.Backup.UsbMount, card: data}
}

func (u *usbEnv) wantNoLeftovers(t *testing.T) {
	t.Helper()
	entries, _ := os.ReadDir(u.mnt)
	for _, en := range entries {
		if strings.HasSuffix(en.Name(), ".part") {
			t.Errorf(".part übrig: %s", en.Name())
		}
	}
}

func TestBackupUSBSuccess(t *testing.T) {
	linuxOnly(t)
	u := newUSBEnv(t, "exfat")
	cl := helperapi.New(startServer(t, u.env))
	if err := cl.BackupUSB(context.Background(), "Presse-1"); err != nil {
		t.Fatal(err)
	}
	st := waitStopped(t, cl)
	const name = "hmikiosk-Presse-1-2026-11-05-0907.img.gz"
	if st.Error != "" || !st.Finished || st.File != name || st.Mode != "usb" || st.ReadBytes != int64(len(u.card)) {
		t.Fatalf("Status: %+v", st)
	}
	f, err := os.Open(filepath.Join(u.mnt, name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if !bytes.Equal(gunzipAll(t, f), u.card) {
		t.Fatal("Datei weicht ab")
	}
	u.wantNoLeftovers(t)
	want := []string{
		"blkid -o value -s TYPE /dev/sda1",
		"mount -t exfat -o nosuid,nodev,noexec,uid=0,gid=0,umask=0077 /dev/sda1 " + u.mnt,
		"sync",
		"sync",
		"umount " + u.mnt,
	}
	if got := u.fx.got(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("Aufrufe:\n%v\nerwartet:\n%v", got, want)
	}
}

func TestBackupUSBFilesystemMapping(t *testing.T) {
	linuxOnly(t)
	for fs, kernel := range map[string]string{"ext4": "ext4", "ntfs": "ntfs3"} {
		opts := "nosuid,nodev,noexec"
		if kernel == "ntfs3" {
			opts += ",uid=0,gid=0,umask=0077" // ohne Rechte im Dateisystem: nur root darf lesen
		}
		u := newUSBEnv(t, fs)
		cl := helperapi.New(startServer(t, u.env))
		if err := cl.BackupUSB(context.Background(), "T"); err != nil {
			t.Fatalf("%s: %v", fs, err)
		}
		waitStopped(t, cl)
		want := "mount -t " + kernel + " -o " + opts + " /dev/sda1 " + u.mnt
		found := false
		for _, c := range u.fx.got() {
			found = found || c == want
		}
		if !found {
			t.Errorf("%s: %q fehlt in %v", fs, want, u.fx.got())
		}
	}
}

func TestBackupUSBRejectsFilesystems(t *testing.T) {
	linuxOnly(t)
	cases := map[string]string{
		"vfat": "FAT32 kann keine Dateien über 4 GB speichern. Bitte den Stick mit exFAT formatieren.",
		"":     "Das Dateisystem des USB-Sticks ist nicht erkennbar.",
		"xfs":  "Das Dateisystem xfs wird nicht unterstützt (erlaubt: exFAT, ext4, NTFS).",
	}
	for fs, msg := range cases {
		u := newUSBEnv(t, fs)
		cl := helperapi.New(startServer(t, u.env))
		err := cl.BackupUSB(context.Background(), "T")
		if err == nil || err.Error() != msg {
			t.Fatalf("%q: %v", fs, err)
		}
		for _, c := range u.fx.got() {
			if strings.HasPrefix(c, "mount") {
				t.Errorf("%q: Stick eingehängt", fs)
			}
		}
		// Die Sperre ist frei: der zweite Versuch meldet denselben Fehler, nicht "läuft gerade".
		if err := cl.BackupUSB(context.Background(), "T"); err == nil || err.Error() != msg {
			t.Errorf("%q zweiter Versuch: %v", fs, err)
		}
	}
}

func TestBackupUSBNoStick(t *testing.T) {
	linuxOnly(t)
	u := newUSBEnv(t, "exfat")
	os.Remove(filepath.Join(u.srv.Backup.SysBlock, "sda"))
	cl := helperapi.New(startServer(t, u.env))
	err := cl.BackupUSB(context.Background(), "T")
	if err == nil || !strings.HasPrefix(err.Error(), "Kein USB-Stick gefunden") {
		t.Fatalf("err: %v", err)
	}
}

func TestBackupUSBTooLittleSpace(t *testing.T) {
	linuxOnly(t)
	u := newUSBEnv(t, "exfat")
	u.srv.Backup.FreeBytes = func(string) (int64, error) { return 3 << 30, nil }
	cl := helperapi.New(startServer(t, u.env))
	err := cl.BackupUSB(context.Background(), "T")
	if err == nil || !strings.Contains(err.Error(), "Zu wenig freier Platz") {
		t.Fatalf("err: %v", err)
	}
	calls := u.fx.got()
	if calls[len(calls)-1] != "umount "+u.mnt {
		t.Fatalf("nicht ausgehängt: %v", calls)
	}
	if entries, _ := os.ReadDir(u.mnt); len(entries) != 0 {
		t.Fatalf("Dateien angelegt: %v", entries)
	}
}

func TestBackupUSBMountFailure(t *testing.T) {
	linuxOnly(t)
	u := newUSBEnv(t, "ntfs")
	u.fx.fail["mount -t ntfs3 -o nosuid,nodev,noexec,uid=0,gid=0,umask=0077 /dev/sda1 "+u.mnt] = io.ErrUnexpectedEOF
	cl := helperapi.New(startServer(t, u.env))
	err := cl.BackupUSB(context.Background(), "T")
	if err == nil || !strings.Contains(err.Error(), "mount fehlgeschlagen") || !strings.Contains(err.Error(), "NTFS") {
		t.Fatalf("err: %v", err)
	}
	for _, c := range u.fx.got() {
		if strings.HasPrefix(c, "umount") {
			t.Errorf("ausgehängt, obwohl nie eingehängt: %v", u.fx.got())
		}
	}
}

func TestBackupUSBCancelDeletesPart(t *testing.T) {
	linuxOnly(t)
	u := newUSBEnv(t, "exfat")
	hit := make(chan struct{})
	release := make(chan struct{})
	u.srv.Backup.onChunk = func(total int64) {
		if total > 1<<20 {
			select {
			case <-hit:
			default:
				close(hit)
			}
			<-release
		}
	}
	cl := helperapi.New(startServer(t, u.env))
	if err := cl.BackupUSB(context.Background(), "T"); err != nil {
		t.Fatal(err)
	}
	<-hit
	st, _ := cl.BackupStatus(context.Background())
	if !st.Running || st.Mode != "usb" || st.TotalBytes != int64(len(u.card)) {
		t.Fatalf("Status: %+v", st)
	}
	if entries, _ := os.ReadDir(u.mnt); len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), ".part") {
		t.Fatalf("Zieldateien: %v", entries)
	}
	if err := cl.BackupCancel(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(release)
	st = waitStopped(t, cl)
	if st.Finished || st.Error != "Die Sicherung wurde abgebrochen." || st.File != "" {
		t.Fatalf("Status: %+v", st)
	}
	if entries, _ := os.ReadDir(u.mnt); len(entries) != 0 {
		t.Fatalf("Dateien übrig: %v", entries)
	}
	calls := u.fx.got()
	if calls[len(calls)-1] != "umount "+u.mnt {
		t.Fatalf("nicht ausgehängt: %v", calls)
	}
}

func TestBackupUSBRefusesExistingFile(t *testing.T) {
	linuxOnly(t)
	u := newUSBEnv(t, "exfat")
	final := filepath.Join(u.mnt, "hmikiosk-T-2026-11-05-0907.img.gz")
	writeFile(t, final, "alt")
	cl := helperapi.New(startServer(t, u.env))
	err := cl.BackupUSB(context.Background(), "T")
	if err == nil || !strings.Contains(err.Error(), "existiert bereits") {
		t.Fatalf("err: %v", err)
	}
	if b, _ := os.ReadFile(final); string(b) != "alt" {
		t.Fatal("vorhandene Datei überschrieben")
	}
}

func TestBackupUSBRejectsBadName(t *testing.T) {
	linuxOnly(t)
	u := newUSBEnv(t, "exfat")
	cl := helperapi.New(startServer(t, u.env))
	for _, n := range []string{"../x", "a/b", "ä", "a b", strings.Repeat("a", 41), "a.b"} {
		if err := cl.Call(context.Background(), CmdBackupUSB, n); err == nil || err.Error() != "Terminalname ungültig" {
			t.Errorf("%q: %v", n, err)
		}
	}
	if err := cl.Call(context.Background(), CmdBackupUSB, ""); err == nil || err.Error() != "Der Wert fehlt" {
		t.Errorf("leer: %v", err)
	}
	if len(u.fx.got()) != 0 {
		t.Errorf("Aufrufe: %v", u.fx.got())
	}
}

func TestUSBWriteErrorMessages(t *testing.T) {
	if got := usbWriteErr(syscall.ENOSPC); got.Error() != "Der USB-Stick ist voll." {
		t.Fatalf("ENOSPC: %v", got)
	}
	if got := usbWriteErr(&os.PathError{Op: "write", Path: "x", Err: syscall.ENOSPC}); got.Error() != "Der USB-Stick ist voll." {
		t.Fatalf("PathError: %v", got)
	}
	if got := usbWriteErr(io.ErrClosedPipe); !strings.Contains(got.Error(), "USB-Stick") {
		t.Fatalf("sonst: %v", got)
	}
}

// Der Einhängeort ist nur für root zugänglich, auch ein vorhandener zu offener Ordner wird eingeschränkt.
func TestBackupUSBMountpointIsPrivate(t *testing.T) {
	linuxOnly(t)
	u := newUSBEnv(t, "exfat")
	mkdirs(t, u.mnt)
	if err := os.Chmod(u.mnt, 0o755); err != nil {
		t.Fatal(err)
	}
	cl := helperapi.New(startServer(t, u.env))
	if err := cl.BackupUSB(context.Background(), "T"); err != nil {
		t.Fatal(err)
	}
	waitStopped(t, cl)
	if fi, err := os.Lstat(u.mnt); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("Einhängeort: %v %v", fi, err)
	}
}

func TestBackupUSBMountpointLinkIsRefused(t *testing.T) {
	linuxOnly(t)
	u := newUSBEnv(t, "exfat")
	symlink(t, t.TempDir(), u.mnt)
	cl := helperapi.New(startServer(t, u.env))
	err := cl.BackupUSB(context.Background(), "T")
	if err == nil || !strings.Contains(err.Error(), "Einhängeort") {
		t.Fatalf("err: %v", err)
	}
}

func TestUSBMountOptions(t *testing.T) {
	for kernel, want := range map[string]string{
		"exfat": "nosuid,nodev,noexec,uid=0,gid=0,umask=0077",
		"ntfs3": "nosuid,nodev,noexec,uid=0,gid=0,umask=0077",
		"ext4":  "nosuid,nodev,noexec",
	} {
		if got := usbMountOptions(kernel); got != want {
			t.Errorf("%s: %q, erwartet %q", kernel, got, want)
		}
	}
}
