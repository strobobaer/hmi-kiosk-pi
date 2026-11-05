package helper

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeExec struct {
	mu    sync.Mutex
	calls []string
	fail  map[string]error // Schlüssel: komplette Zeile
}

func (f *fakeExec) exec(_ context.Context, name string, args ...string) error {
	line := strings.Join(append([]string{name}, args...), " ")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, line)
	return f.fail[line]
}

func (f *fakeExec) got() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

type fakeUpd struct {
	activated                []string
	rollbacks                int
	onActivate               func()
	activateErr, rollbackErr error
}

func (u *fakeUpd) Activate(v string) error {
	u.activated = append(u.activated, v)
	if u.onActivate != nil {
		u.onActivate()
	}
	return u.activateErr
}
func (u *fakeUpd) Rollback() error {
	u.rollbacks++
	return u.rollbackErr
}

type env struct {
	srv  *Server
	fx   *fakeExec
	upd  *fakeUpd
	boot string
	dir  string
}

func newEnv(t *testing.T, bootContent string) *env {
	t.Helper()
	dir := t.TempDir()
	e := &env{fx: &fakeExec{fail: map[string]error{}}, upd: &fakeUpd{}, dir: dir, boot: filepath.Join(dir, "config.txt")}
	if err := os.WriteFile(e.boot, []byte(bootContent), 0o644); err != nil {
		t.Fatal(err)
	}
	e.srv = NewServer(filepath.Join(dir, "s.sock"), 1000, e.fx.exec, e.boot, e.upd)
	e.srv.NTPConfPath = filepath.Join(dir, "timesyncd.d", "hmi-kiosk.conf")
	e.srv.NTPStatePath = filepath.Join(dir, "data", "system", "ntp-server")
	e.srv.HealthyMarker = filepath.Join(dir, "healthy")
	return e
}

func (e *env) read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestWifiOffSequence(t *testing.T) {
	e := newEnv(t, "[all]\nx=1\n")
	if err := e.srv.Dispatch(context.Background(), CmdWifiOff, ""); err != nil {
		t.Fatal(err)
	}
	want := []string{"mount -o remount,rw /boot/firmware", "mount -o remount,ro /boot/firmware", "rfkill block wifi"}
	if got := e.fx.got(); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if got := e.read(t, e.boot); got != "[all]\nx=1\ndtoverlay=disable-wifi\n" {
		t.Fatalf("config.txt: %q", got)
	}
	if got := e.read(t, e.boot+".bak"); got != "[all]\nx=1\n" {
		t.Fatalf("bak: %q", got)
	}
	entries, _ := os.ReadDir(e.dir)
	for _, en := range entries {
		if strings.Contains(en.Name(), ".tmp") {
			t.Fatalf("Temp-Datei übrig: %s", en.Name())
		}
	}
}

func TestRadioCommandsMatrix(t *testing.T) {
	cases := []struct {
		cmd, in, rfkill, out string
	}{
		{CmdWifiOn, "[all]\ndtoverlay=disable-wifi\n", "rfkill unblock wifi", "[all]\n"},
		{CmdBtOff, "", "rfkill block bluetooth", "[all]\ndtoverlay=disable-bt\n"},
		{CmdBtOn, "[all]\ndtoverlay=disable-bt\n", "rfkill unblock bluetooth", "[all]\n"},
	}
	for _, c := range cases {
		e := newEnv(t, c.in)
		if err := e.srv.Dispatch(context.Background(), c.cmd, ""); err != nil {
			t.Fatal(c.cmd, err)
		}
		calls := e.fx.got()
		if len(calls) != 3 || calls[2] != c.rfkill {
			t.Fatalf("%s: %v", c.cmd, calls)
		}
		if got := e.read(t, e.boot); got != c.out {
			t.Fatalf("%s: %q", c.cmd, got)
		}
	}
}

func TestUnchangedConfigSkipsRemount(t *testing.T) {
	e := newEnv(t, "[all]\ndtoverlay=disable-wifi\n")
	if err := e.srv.Dispatch(context.Background(), CmdWifiOff, ""); err != nil {
		t.Fatal(err)
	}
	if got := e.fx.got(); !reflect.DeepEqual(got, []string{"mount -o remount,ro /boot/firmware", "rfkill block wifi"}) {
		t.Fatalf("got %v", got)
	}
}

func TestRemountRoAlsoAfterWriteFailure(t *testing.T) {
	e := newEnv(t, "[all]\n")
	// Ein Verzeichnis an der Stelle der Sicherungsdatei lässt das Schreiben scheitern (root im Container).
	if err := os.MkdirAll(filepath.Join(e.boot+".bak", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := e.srv.Dispatch(context.Background(), CmdWifiOff, "")
	if err == nil || !strings.Contains(err.Error(), "config.txt konnte nicht geschrieben werden") {
		t.Fatalf("err: %v", err)
	}
	want := []string{"mount -o remount,rw /boot/firmware", "mount -o remount,ro /boot/firmware"}
	if got := e.fx.got(); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if got := e.read(t, e.boot); got != "[all]\n" {
		t.Fatalf("config.txt verändert: %q", got)
	}
}

func TestRemountRoFailureReportedAlongsideWrite(t *testing.T) {
	e := newEnv(t, "[all]\n")
	e.fx.fail["mount -o remount,ro /boot/firmware"] = errors.New("busy")
	err := e.srv.Dispatch(context.Background(), CmdWifiOff, "")
	if err == nil || !strings.Contains(err.Error(), "geschrieben") || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("err: %v", err)
	}
	if got := e.read(t, e.boot); !strings.Contains(got, "dtoverlay=disable-wifi") {
		t.Fatalf("Schreiben fehlt: %q", got)
	}
	if got := e.fx.got(); got[len(got)-1] != "rfkill block wifi" {
		t.Fatalf("rfkill fehlt: %v", got)
	}
}

func TestRemountRwFailureWritesNothing(t *testing.T) {
	e := newEnv(t, "[all]\n")
	e.fx.fail["mount -o remount,rw /boot/firmware"] = errors.New("nein")
	if err := e.srv.Dispatch(context.Background(), CmdWifiOff, ""); err == nil {
		t.Fatal("Fehler erwartet")
	}
	if got := e.fx.got(); len(got) != 1 {
		t.Fatalf("got %v", got)
	}
}

func TestUnknownCommandRejected(t *testing.T) {
	e := newEnv(t, "")
	for _, c := range []string{"", "format-disk", "WIFI-OFF", "reboot "} {
		if err := e.srv.Dispatch(context.Background(), c, ""); err == nil {
			t.Errorf("%q angenommen", c)
		}
	}
	if len(e.fx.got()) != 0 {
		t.Fatalf("Aufrufe: %v", e.fx.got())
	}
}

func TestNoArgCommandsRejectArgument(t *testing.T) {
	e := newEnv(t, "")
	for _, c := range []string{CmdWifiOff, CmdWifiOn, CmdBtOff, CmdBtOn, CmdReboot, CmdRestartService, CmdUpdateRollback} {
		if err := e.srv.Dispatch(context.Background(), c, "x"); err == nil {
			t.Errorf("%s nahm Wert an", c)
		}
	}
	if len(e.fx.got()) != 0 || e.upd.rollbacks != 0 {
		t.Fatal("Befehl trotz Wert ausgeführt")
	}
}

func TestSimpleExecCommands(t *testing.T) {
	e := newEnv(t, "")
	for _, c := range []string{CmdReboot, CmdRestartService} {
		if err := e.srv.Dispatch(context.Background(), c, ""); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"systemctl reboot", "systemctl restart hmi-kiosk.service"}
	if got := e.fx.got(); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestSetTimeRejectsNonRFC3339(t *testing.T) {
	e := newEnv(t, "")
	for _, a := range []string{"2026; rm -rf /", "2026-11-05 10:00:00", "", "2026-11-05T10:00:00", "1999-01-01T00:00:00Z"} {
		if err := e.srv.Dispatch(context.Background(), CmdSetTime, a); err == nil {
			t.Errorf("%q angenommen", a)
		}
	}
	if len(e.fx.got()) != 0 {
		t.Fatalf("Aufrufe: %v", e.fx.got())
	}
}

func TestSetTimeConvertsToLocal(t *testing.T) {
	e := newEnv(t, "")
	in := "2026-11-05T10:20:30Z"
	if err := e.srv.Dispatch(context.Background(), CmdSetTime, in); err != nil {
		t.Fatal(err)
	}
	ts, _ := time.Parse(time.RFC3339, in)
	want := []string{"timedatectl set-ntp false", "timedatectl set-time " + ts.Local().Format("2006-01-02 15:04:05")}
	if got := e.fx.got(); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestSetNTPRejectsShellChars(t *testing.T) {
	e := newEnv(t, "")
	for _, a := range []string{"a;b", "$(x)", "-x", "a b", "a\nNTP=evil", "fe80::1%eth0", "`x`"} {
		if err := e.srv.Dispatch(context.Background(), CmdSetNTP, a); err == nil {
			t.Errorf("%q angenommen", a)
		}
	}
	if len(e.fx.got()) != 0 {
		t.Fatalf("Aufrufe: %v", e.fx.got())
	}
	if _, err := os.Stat(e.srv.NTPConfPath); err == nil {
		t.Fatal("Datei trotz Ablehnung geschrieben")
	}
}

func TestSetNTPWritesFilesAndEnables(t *testing.T) {
	e := newEnv(t, "")
	if err := e.srv.Dispatch(context.Background(), CmdSetNTP, "pool.ntp.org"); err != nil {
		t.Fatal(err)
	}
	if got := e.read(t, e.srv.NTPConfPath); got != "[Time]\nNTP=pool.ntp.org\n" {
		t.Fatalf("conf: %q", got)
	}
	if got := e.read(t, e.srv.NTPStatePath); got != "pool.ntp.org\n" {
		t.Fatalf("state: %q", got)
	}
	if got := e.fx.got(); !reflect.DeepEqual(got, ntpSeq) {
		t.Fatalf("got %v", got)
	}
}

func TestReapplyNTP(t *testing.T) {
	e := newEnv(t, "")
	if err := e.srv.ReapplyNTP(context.Background()); err != nil || len(e.fx.got()) != 0 {
		t.Fatalf("ohne Wert: %v %v", err, e.fx.got())
	}
	os.MkdirAll(filepath.Dir(e.srv.NTPStatePath), 0o755)
	os.WriteFile(e.srv.NTPStatePath, []byte("10.1.2.3\n"), 0o644)
	if err := e.srv.ReapplyNTP(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := e.read(t, e.srv.NTPConfPath); got != "[Time]\nNTP=10.1.2.3\n" {
		t.Fatalf("conf: %q", got)
	}
	os.WriteFile(e.srv.NTPStatePath, []byte("a;b\n"), 0o644)
	if err := e.srv.ReapplyNTP(context.Background()); err == nil {
		t.Fatal("ungültiger Wert angewendet")
	}
}

func TestUpdateActivateRejectsPathTraversal(t *testing.T) {
	e := newEnv(t, "")
	for _, a := range []string{"../x", "1.2", "1.2.3.4", "1.2.3/..", "v1.2.3", "1.2.3\n", "../../1.2.3", " 1.2.3", "a.b.c"} {
		if err := e.srv.Dispatch(context.Background(), CmdUpdateActivate, a); err == nil {
			t.Errorf("%q angenommen", a)
		}
	}
	if len(e.upd.activated) != 0 {
		t.Fatal("Activate trotz Ablehnung")
	}
	if err := e.srv.Dispatch(context.Background(), CmdUpdateActivate, "1.12.3"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(e.upd.activated, []string{"1.12.3"}) {
		t.Fatalf("got %v", e.upd.activated)
	}
	if err := e.srv.Dispatch(context.Background(), CmdUpdateRollback, ""); err != nil || e.upd.rollbacks != 1 {
		t.Fatal("Rollback")
	}
}

var ntpSeq = []string{"timedatectl set-ntp true", "systemctl restart systemd-timesyncd"}
