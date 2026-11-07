package helper

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hmikiosk/internal/helperapi"
)

const (
	tP1Start, tP1Size = 2048, 4096
	tP2Start, tP2Size = 6144, 8000
	tP2End            = tP2Start + tP2Size
	tOldP3Start       = tP2End
	tOldP3Size        = 20480
)

// testCard ist die Ersatz-"Karte": MBR mit drei Partitionen, Zufallsbytes in allen.
type testCard struct {
	path string
	raw  []byte
}

func putEntry(sec []byte, n int, typ byte, start, size uint32) {
	e := sec[446+16*n : 446+16*n+16]
	clear(e)
	e[4] = typ
	binary.LittleEndian.PutUint32(e[8:], start)
	binary.LittleEndian.PutUint32(e[12:], size)
}

func makeMBRCard(t *testing.T) testCard {
	t.Helper()
	raw := make([]byte, (tOldP3Start+tOldP3Size)*512)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	// Ein Teil der Rohbytes ist Nullen, wie bei einer echten Karte.
	clear(raw[(tP2Start+4096)*512 : tP2End*512])
	sec := raw[:512]
	putEntry(sec, 0, 0x0c, tP1Start, tP1Size)
	putEntry(sec, 1, 0x83, tP2Start, tP2Size)
	putEntry(sec, 2, 0x83, tOldP3Start, tOldP3Size)
	putEntry(sec, 3, 0, 0, 0)
	sec[510], sec[511] = 0x55, 0xaa
	p := filepath.Join(t.TempDir(), "mmcblk9")
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return testCard{p, raw}
}

// imageMarker legt die Abbild-Kennung (/etc/hmi-kiosk/image-release) in einem Testordner an.
func imageMarker(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "image-release")
	writeFile(t, p, "version=1.0.0\n")
	return p
}

// realTools führt mkfs.ext4 und debugfs wirklich aus, alles andere nur als Aufzeichnung.
func realTools(e *env) Exec {
	return func(ctx context.Context, name string, args ...string) error {
		if err := e.fx.exec(ctx, name, args...); err != nil {
			return err
		}
		if name != "mkfs.ext4" && name != "debugfs" {
			return nil
		}
		p, err := exec.LookPath(name)
		if err != nil {
			return err
		}
		out, err := exec.CommandContext(ctx, p, args...).CombinedOutput()
		if err != nil {
			return &execError{err, string(out)}
		}
		return nil
	}
}

type execError struct {
	err error
	out string
}

func (e *execError) Error() string { return e.err.Error() + ": " + strings.TrimSpace(e.out) }

type compactEnv struct {
	*env
	card testCard
	data string
	run  string
}

func newCompactEnv(t *testing.T) *compactEnv {
	t.Helper()
	linuxOnly(t)
	for _, tool := range []string{"mkfs.ext4", "debugfs"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " fehlt")
		}
	}
	e := newEnv(t, "[all]\n")
	c := &compactEnv{env: e, card: makeMBRCard(t), data: filepath.Join(t.TempDir(), "data"), run: filepath.Join(t.TempDir(), "run")}
	mkdirs(t, c.data)
	mkdirs(t, c.run)
	b := &e.srv.Backup
	b.Source = c.card.path
	b.MountInfo = filepath.Join(t.TempDir(), "mountinfo")
	writeFile(t, b.MountInfo, roMountInfo)
	b.SysClassBlock = filepath.Join(t.TempDir(), "class-block")
	writeFile(t, filepath.Join(b.SysClassBlock, "mmcblk9p1", "dev"), "179:1\n")
	writeFile(t, filepath.Join(b.SysClassBlock, "mmcblk9p2", "dev"), "179:2\n")
	b.DataDir = c.data
	b.RunDir = c.run
	b.ImageRelease = imageMarker(t)
	b.MemInfo = filepath.Join(t.TempDir(), "meminfo")
	writeFile(t, b.MemInfo, "MemTotal: 8000000 kB\nMemAvailable: 6000000 kB\n")
	b.FreeBytes = func(string) (int64, error) { return 4 << 30, nil }
	b.IdleTimeout = 5 * time.Second
	e.srv.exec = realTools(e)
	return c
}

// fillData legt Dateien, Verweise und Rechte an, die im Abbild erhalten bleiben müssen.
func (c *compactEnv) fillData(t *testing.T) (prog []byte) {
	t.Helper()
	prog = make([]byte, 300<<10)
	if _, err := rand.Read(prog); err != nil {
		t.Fatal(err)
	}
	mkdirs(t, filepath.Join(c.data, "app", "1.2.3"))
	mkdirs(t, filepath.Join(c.data, "system"))
	if err := os.WriteFile(filepath.Join(c.data, "app", "1.2.3", "hmi-kiosk"), prog, 0o755); err != nil {
		t.Fatal(err)
	}
	symlink(t, "1.2.3", filepath.Join(c.data, "app", "current"))
	writeFile(t, filepath.Join(c.data, "config.json"), `{"a":1}`)
	if err := os.Chmod(filepath.Join(c.data, "config.json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(c.data, "config.json"), filepath.Join(c.data, "config.hard")); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		for _, p := range []string{"", "app", "app/1.2.3", "app/1.2.3/hmi-kiosk", "config.json"} {
			if err := os.Chown(filepath.Join(c.data, p), 1100, 1100); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Lchown(filepath.Join(c.data, "app", "current"), 1100, 1100); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(c.data, 0o750); err != nil {
		t.Fatal(err)
	}
	return prog
}

func readAll(t *testing.T, r io.Reader) []byte {
	t.Helper()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func debugfs(t *testing.T, img, cmd string) string {
	t.Helper()
	out, err := exec.Command("debugfs", "-R", cmd, img).Output()
	if err != nil {
		t.Fatalf("debugfs %q: %v", cmd, err)
	}
	return string(out)
}

func dirEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		var names []string
		for _, en := range entries {
			names = append(names, en.Name())
		}
		t.Errorf("Temp-Reste in %s: %v", dir, names)
	}
}

// checkCompact prüft das kompakte Abbild gegen die Ersatzkarte und /data.
func (c *compactEnv) checkCompact(t *testing.T, out []byte, size int64, prog []byte) {
	t.Helper()
	src := c.card.raw
	if int64(len(out)) != size {
		t.Fatalf("Länge %d, gemeldet %d", len(out), size)
	}
	// MBR: Startcode und Disk-ID, Partitionen 1 und 2 wie in der Quelle, Partition 3 klein, Partition 4 leer.
	if !bytes.Equal(out[:446], src[:446]) {
		t.Error("Startcode oder Disk-ID verändert")
	}
	if !bytes.Equal(out[446:446+32], src[446:446+32]) {
		t.Error("Partitionseinträge 1 und 2 verändert")
	}
	if out[510] != 0x55 || out[511] != 0xaa {
		t.Error("MBR-Kennung fehlt")
	}
	e3 := out[446+32 : 446+48]
	p3start := int64(binary.LittleEndian.Uint32(e3[8:]))
	p3size := int64(binary.LittleEndian.Uint32(e3[12:]))
	if e3[4] != 0x83 || e3[0] != 0 {
		t.Errorf("Partition 3: Typ %#x Status %#x", e3[4], e3[0])
	}
	if p3start%2048 != 0 || p3start < tP2End || p3start >= tP2End+alignSectors {
		t.Errorf("Partition 3 beginnt bei %d (Ende p2 %d)", p3start, tP2End)
	}
	if (p3start+p3size)*512 != size || p3size%2048 != 0 || p3size >= tOldP3Size*10 {
		t.Errorf("Partition 3: %d+%d Sektoren, Gesamtgröße %d", p3start, p3size, size)
	}
	if !allZero(out[446+48 : 446+64]) {
		t.Error("Partition 4 nicht leer")
	}
	// Rohbytes bis zum Ende von Partition 2 gleich, danach bis p3 nur Nullen.
	if !bytes.Equal(out[512:tP2End*512], src[512:tP2End*512]) {
		t.Error("Partition 1 oder 2 weicht von der Quelle ab")
	}
	if !allZero(out[tP2End*512 : p3start*512]) {
		t.Error("Lücke vor Partition 3 nicht leer")
	}
	// Partition 3: ext4 HMIDATA mit den Dateien.
	img := filepath.Join(t.TempDir(), "p3.img")
	if err := os.WriteFile(img, out[p3start*512:], 0o600); err != nil {
		t.Fatal(err)
	}
	if o := debugfs(t, img, "stats"); !strings.Contains(o, "Filesystem volume name:   HMIDATA") {
		t.Errorf("Label: %s", firstLines(o, 3))
	}
	if got := debugfs(t, img, "cat /app/1.2.3/hmi-kiosk"); got != string(prog) {
		t.Error("Programmdatei weicht ab")
	}
	if got := debugfs(t, img, "cat /config.json"); got != `{"a":1}` {
		t.Errorf("config.json: %q", got)
	}
	if o := debugfs(t, img, "stat /app/current"); !strings.Contains(o, "Type: symlink") || !strings.Contains(o, `Fast link dest: "1.2.3"`) {
		t.Errorf("Verweis: %s", o)
	}
	if o := debugfs(t, img, "stat /config.hard"); !strings.Contains(o, "Links: 2") {
		t.Errorf("fester Verweis: %s", firstLines(o, 6))
	}
	if o := debugfs(t, img, "stat /config.json"); !strings.Contains(o, "Mode:  0600") {
		t.Errorf("Rechte: %s", firstLines(o, 3))
	}
	if os.Geteuid() == 0 {
		for _, p := range []string{"/", "/app", "/app/1.2.3/hmi-kiosk", "/config.json", "/app/current"} {
			if o := debugfs(t, img, "stat "+p); !strings.Contains(o, "User:  1100   Group:  1100") {
				t.Errorf("Besitzer von %s: %s", p, firstLines(o, 3))
			}
		}
	}
	if o := debugfs(t, img, "stat /"); !strings.Contains(o, "Mode:  0750") || !strings.Contains(o, "Type: directory") {
		t.Errorf("Rechte von /: %s", firstLines(o, 3))
	}
}

func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, " | ")
}

func allZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}

func TestCompactImageContent(t *testing.T) {
	c := newCompactEnv(t)
	prog := c.fillData(t)
	rc, size, err := c.srv.openCompact(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := readAll(t, rc)
	c.checkCompact(t, out, size, prog)
	if err := rc.Close(); err != nil {
		t.Error(err)
	}
	dirEmpty(t, c.run)
	calls := c.fx.got()
	if len(calls) != 2 || !strings.HasPrefix(calls[0], "mkfs.ext4 ") || !strings.Contains(calls[0], "-L HMIDATA") ||
		!strings.Contains(calls[0], "-d "+c.data) || !strings.HasPrefix(calls[1], "debugfs -w") {
		t.Errorf("Aufrufe: %q", calls)
	}
}

func TestCompactImageEmptyDataIs64MiB(t *testing.T) {
	c := newCompactEnv(t)
	rc, size, err := c.srv.openCompact(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	// p3 beginnt bei 14336 (Ende p2 14144 auf 1 MiB aufgerundet).
	if got := size - 14336*512; got != 64<<20 {
		t.Errorf("Datenpartition %d Byte, erwartet 64 MiB", got)
	}
}

func TestCompactImageCloseWithoutReadingRemovesTemp(t *testing.T) {
	c := newCompactEnv(t)
	c.fillData(t)
	rc, _, err := c.srv.openCompact(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(c.run); len(entries) != 1 {
		t.Fatalf("Temp-Ordner fehlt: %v", entries)
	}
	if err := rc.Close(); err != nil {
		t.Error(err)
	}
	rc.Close() // doppeltes Schließen ist harmlos
	dirEmpty(t, c.run)
}

func TestCompactImageMkfsFailureRemovesTemp(t *testing.T) {
	c := newCompactEnv(t)
	real := c.srv.exec
	c.srv.exec = func(ctx context.Context, name string, args ...string) error {
		if name == "mkfs.ext4" {
			return io.ErrUnexpectedEOF
		}
		return real(ctx, name, args...)
	}
	_, _, err := c.srv.openCompact(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Datenpartition") {
		t.Fatalf("err: %v", err)
	}
	dirEmpty(t, c.run)
}

func TestCompactImageCancelDuringMkfsRemovesTemp(t *testing.T) {
	c := newCompactEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	c.srv.exec = func(ctx context.Context, name string, args ...string) error {
		cancel()
		<-ctx.Done()
		return ctx.Err()
	}
	if _, _, err := c.srv.openCompact(ctx); err == nil {
		t.Fatal("kein Fehler")
	}
	dirEmpty(t, c.run)
}

func TestCompactImageRejectsNonImageSystem(t *testing.T) {
	c := newCompactEnv(t)
	c.srv.Backup.ImageRelease = filepath.Join(t.TempDir(), "fehlt")
	_, _, err := c.srv.openCompact(context.Background())
	want := "Die SD-Sicherung gibt es nur für Systeme aus dem SD-Abbild. Für dieses System den Konfigurations-Export nutzen."
	if err == nil || err.Error() != want {
		t.Fatalf("err: %v", err)
	}
	if len(c.fx.got()) != 0 {
		t.Errorf("Aufrufe: %v", c.fx.got())
	}
	dirEmpty(t, c.run)
}

func TestCompactImageRejectsBadPartitionTables(t *testing.T) {
	for name, mod := range map[string]func(b []byte){
		"ohne Kennung":    func(b []byte) { b[510] = 0 },
		"ohne p1":         func(b []byte) { putEntry(b, 0, 0, 0, 0) },
		"ohne p2":         func(b []byte) { putEntry(b, 1, 0, 0, 0) },
		"GPT-Schutz-MBR":  func(b []byte) { putEntry(b, 0, 0xee, 1, 14143); putEntry(b, 1, 0, 0, 0) },
		"p2 vor p1":       func(b []byte) { putEntry(b, 1, 0x83, 1024, 512) },
		"p2 überlappt p1": func(b []byte) { putEntry(b, 1, 0x83, tP1Start+10, 8192) },
		"p2 hinter Karte": func(b []byte) { putEntry(b, 1, 0x83, tP2Start, 1<<20) },
	} {
		t.Run(name, func(t *testing.T) {
			c := newCompactEnv(t)
			mod(c.card.raw[:512])
			if err := os.WriteFile(c.card.path, c.card.raw, 0o600); err != nil {
				t.Fatal(err)
			}
			_, _, err := c.srv.openCompact(context.Background())
			if err == nil || !strings.Contains(err.Error(), "Partitionstabelle") {
				t.Fatalf("err: %v", err)
			}
			dirEmpty(t, c.run)
			if len(c.fx.got()) != 0 {
				t.Errorf("Aufrufe: %v", c.fx.got())
			}
		})
	}
}

func TestCompactImageMissingDevice(t *testing.T) {
	c := newCompactEnv(t)
	c.srv.Backup.Source = filepath.Join(t.TempDir(), "fehlt")
	_, _, err := c.srv.openCompact(context.Background())
	if err == nil || !strings.Contains(err.Error(), "SD-Karte") {
		t.Fatalf("err: %v", err)
	}
}

func TestCompactImageTooMuchData(t *testing.T) {
	c := newCompactEnv(t)
	f, err := os.Create(filepath.Join(c.data, "gross"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(2 << 30); err != nil { // Loch: kostet keinen Platz
		t.Skip("große Datei nicht anlegbar: ", err)
	}
	f.Close()
	_, _, err = c.srv.openCompact(context.Background())
	if err == nil || !strings.Contains(err.Error(), "zu groß") {
		t.Fatalf("err: %v", err)
	}
	if len(c.fx.got()) != 0 {
		t.Errorf("Aufrufe: %v", c.fx.got())
	}
	dirEmpty(t, c.run)
}

func TestCompactImageNeedsRAM(t *testing.T) {
	c := newCompactEnv(t)
	// 64 MiB Abbild + 512 MiB Reserve = 576 MiB = 589824 kB nötig.
	writeFile(t, c.srv.Backup.MemInfo, "MemAvailable: 589823 kB\n")
	_, _, err := c.srv.openCompact(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Arbeitsspeicher") {
		t.Fatalf("zu wenig RAM: %v", err)
	}
	writeFile(t, c.srv.Backup.MemInfo, "MemAvailable: 589824 kB\n")
	rc, _, err := c.srv.openCompact(context.Background())
	if err != nil {
		t.Fatalf("genau genug RAM: %v", err)
	}
	rc.Close()
	// Das tmpfs unter /run muss das Abbild fassen.
	c.srv.Backup.FreeBytes = func(p string) (int64, error) {
		if p != c.run {
			t.Errorf("freier Platz von %q gefragt", p)
		}
		return 63 << 20, nil
	}
	if _, _, err := c.srv.openCompact(context.Background()); err == nil || !strings.Contains(err.Error(), "/run") {
		t.Fatalf("tmpfs zu klein: %v", err)
	}
	dirEmpty(t, c.run)
}

func TestImageSize(t *testing.T) {
	const mib = 1 << 20
	for _, tc := range []struct{ used, want int64 }{
		{0, 64 * mib},
		{100 * mib, 189 * mib},
		{100*mib + 1, 190 * mib}, // 100 MiB + 25 MiB + Rest + 64 MiB, aufgerundet
		{800 * mib, 1064 * mib},
	} {
		if got := imageSize(tc.used); got != tc.want {
			t.Errorf("imageSize(%d) = %d MiB, erwartet %d", tc.used, got/mib, tc.want/mib)
		}
	}
}

func TestDataUsedRoundsToBlocksAndSkipsLinks(t *testing.T) {
	linuxOnly(t)
	dir := t.TempDir()
	mkdirs(t, filepath.Join(dir, "a"))
	writeFile(t, filepath.Join(dir, "a", "klein"), "x")
	writeFile(t, filepath.Join(dir, "a", "fest"), strings.Repeat("y", 5000))
	symlink(t, "/etc", filepath.Join(dir, "verweis"))
	got, _, err := dataUsed(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	// a (1 Block), klein (1), fest (2), verweis (1); die Wurzel zählt nicht, /etc wird nicht betreten.
	if want := int64(5 * 4096); got != want {
		t.Errorf("belegt %d, erwartet %d", got, want)
	}
}

func TestParseMBR(t *testing.T) {
	sec := make([]byte, 512)
	putEntry(sec, 1, 0x83, 100, 200)
	sec[510], sec[511] = 0x55, 0xaa
	es, err := parseMBR(sec)
	if err != nil || es[1].Start != 100 || es[1].Sectors != 200 || es[1].Type != 0x83 || es[0].Sectors != 0 {
		t.Fatalf("%+v %v", es, err)
	}
	if _, err := parseMBR(sec[:100]); err == nil {
		t.Error("kurzer Sektor angenommen")
	}
}

// --- über den Helfer-Strom ---

func TestCompactBackupStreamEndToEnd(t *testing.T) {
	c := newCompactEnv(t)
	prog := c.fillData(t)
	cl := helperapi.New(startServer(t, c.env))
	size, rc, err := cl.BackupStream(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := gunzipAll(t, rc)
	rc.Close()
	c.checkCompact(t, out, size, prog)
	st := waitStopped(t, cl)
	if st.Error != "" || !st.Finished || st.ReadBytes != size || st.TotalBytes != size {
		t.Fatalf("Status: %+v", st)
	}
	dirEmpty(t, c.run)
}

func TestCompactBackupClientAbortRemovesTemp(t *testing.T) {
	c := newCompactEnv(t)
	c.fillData(t)
	cl := helperapi.New(startServer(t, c.env))
	_, rc, err := cl.BackupStream(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(rc, make([]byte, 100)); err != nil {
		t.Fatal(err)
	}
	rc.Close()
	waitStopped(t, cl)
	dirEmpty(t, c.run)
}

func TestPlainInstallTakesTheFilesWay(t *testing.T) {
	c := newCompactEnv(t)
	c.srv.Backup.ImageRelease = filepath.Join(t.TempDir(), "fehlt")
	c.srv.ExecOut = func(context.Context, string, ...string) (string, error) { return "TYPE=ext4\n", nil }
	cl := helperapi.New(startServer(t, c.env))
	// Die Ersatzkarte hat drei Partitionen: das Layout der dateiweisen Sicherung passt nicht.
	_, _, err := cl.BackupStream(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Layout") || !strings.Contains(err.Error(), "weitere Partitionen") {
		t.Fatalf("Download: %v", err)
	}
	err = cl.BackupUSB(context.Background(), "T1")
	if err == nil || !strings.Contains(err.Error(), "weitere Partitionen") {
		t.Fatalf("USB: %v", err)
	}
	if st := waitStopped(t, cl); st.Running || st.Finished {
		t.Fatalf("Status: %+v", st)
	}
}

func TestBackupInfo(t *testing.T) {
	c := newCompactEnv(t)
	c.fillData(t)
	cl := helperapi.New(startServer(t, c.env))
	info, err := cl.BackupInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(c.fx.got()) != 0 {
		t.Errorf("backup-info startete Programme: %q", c.fx.got())
	}
	rc, size, err := c.srv.openCompact(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	rc.Close()
	if info.Problem != "" || info.RawBytes != size || info.RawBytes <= tP2End*512 {
		t.Fatalf("Info %+v, Größe %d", info, size)
	}
	c.srv.Backup.ImageRelease = filepath.Join(t.TempDir(), "fehlt")
	c.srv.ExecOut = func(context.Context, string, ...string) (string, error) { return "TYPE=ext4\n", nil }
	info, err = cl.BackupInfo(context.Background())
	if err != nil || info.RawBytes != 0 || !info.Files || !strings.Contains(info.Problem, "Layout") {
		t.Fatalf("Info ohne Abbild: %+v %v", info, err)
	}
}

// --- Aufräumen beim Start ---

func TestCleanupStaleMountAndTemp(t *testing.T) {
	linuxOnly(t)
	e := newEnv(t, "")
	root := t.TempDir()
	b := &e.srv.Backup
	b.UsbMount = filepath.Join(root, "usb")
	b.RunDir = filepath.Join(root, "run")
	b.MountInfo = filepath.Join(root, "mountinfo")
	mkdirs(t, b.UsbMount)
	mkdirs(t, filepath.Join(b.RunDir, "hmi-kiosk-backup-abc"))
	writeFile(t, filepath.Join(b.RunDir, "hmi-kiosk-backup-abc", "data.img"), "x")
	writeFile(t, filepath.Join(b.RunDir, "anderes"), "bleibt")
	writeFile(t, filepath.Join(b.UsbMount, "hmikiosk-T-2026-11-05-0907.img.gz.part"), "halb")
	writeFile(t, filepath.Join(b.UsbMount, "hmikiosk-T-2026-11-04-0907.img.gz"), "fertig")
	writeFile(t, filepath.Join(b.UsbMount, "fremd.part"), "bleibt")
	victim := filepath.Join(root, "opfer")
	writeFile(t, victim, "bleibt")
	symlink(t, victim, filepath.Join(b.UsbMount, "hmikiosk-link.img.gz.part"))
	writeFile(t, b.MountInfo, "40 22 8:1 / "+b.UsbMount+" rw,nosuid,nodev,noexec - exfat /dev/sda1 rw\n"+bootInfo)

	e.srv.CleanupStale(context.Background())

	if calls := e.fx.got(); len(calls) != 1 || calls[0] != "umount "+b.UsbMount {
		t.Errorf("Aufrufe: %q", calls)
	}
	entries, _ := os.ReadDir(b.UsbMount)
	var names []string
	for _, en := range entries {
		names = append(names, en.Name())
	}
	if len(names) != 2 || names[0] != "fremd.part" || names[1] != "hmikiosk-T-2026-11-04-0907.img.gz" {
		t.Errorf("Stick-Ordner: %v", names)
	}
	if got, _ := os.ReadFile(victim); string(got) != "bleibt" {
		t.Error("Ziel des Verweises berührt")
	}
	entries, _ = os.ReadDir(b.RunDir)
	if len(entries) != 1 || entries[0].Name() != "anderes" {
		t.Errorf("RunDir: %v", entries)
	}
}

func TestCleanupStaleNothingToDo(t *testing.T) {
	linuxOnly(t)
	e := newEnv(t, "")
	root := t.TempDir()
	b := &e.srv.Backup
	b.UsbMount = filepath.Join(root, "fehlt")
	b.RunDir = filepath.Join(root, "auch-fehlt")
	b.MountInfo = filepath.Join(root, "mountinfo")
	writeFile(t, b.MountInfo, bootInfo)
	e.srv.CleanupStale(context.Background())
	if calls := e.fx.got(); len(calls) != 0 {
		t.Errorf("Aufrufe: %q", calls)
	}
}

// Baut sich die Quelle nicht, endet die USB-Sicherung mit Meldung: keine .part-Datei, Stick ausgehängt.
func TestBackupUSBSourceFailureIsReportedAndCleansUp(t *testing.T) {
	linuxOnly(t)
	u := newUSBEnv(t, "exfat")
	u.srv.Backup.OpenSource = func(context.Context) (io.ReadCloser, int64, error) {
		return nil, 0, errors.New("Die Datenpartition für die Sicherung ließ sich nicht erstellen: x")
	}
	cl := helperapi.New(startServer(t, u.env))
	if err := cl.BackupUSB(context.Background(), "T"); err != nil {
		t.Fatal(err)
	}
	st := waitStopped(t, cl)
	if st.Finished || !strings.Contains(st.Error, "nicht erstellen") || st.File != "" {
		t.Fatalf("Status: %+v", st)
	}
	u.wantNoLeftovers(t)
	if calls := u.fx.got(); calls[len(calls)-1] != "umount "+u.mnt {
		t.Fatalf("nicht ausgehängt: %v", calls)
	}
}
