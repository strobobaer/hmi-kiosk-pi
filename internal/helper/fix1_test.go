package helper

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"hmikiosk/internal/helperapi"
)

func reapplyWithin(t *testing.T, e *env) error {
	t.Helper()
	ch := make(chan error, 1)
	go func() { ch <- e.srv.ReapplyNTP(context.Background()) }()
	select {
	case err := <-ch:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("ReapplyNTP blockiert")
		return nil
	}
}

func prepState(t *testing.T, e *env) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(e.srv.NTPStatePath), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestReapplyNTPRejectsFIFO(t *testing.T) {
	linuxOnly(t)
	e := newEnv(t, "")
	prepState(t, e)
	if err := syscall.Mkfifo(e.srv.NTPStatePath, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := reapplyWithin(t, e); err == nil {
		t.Fatal("Fehler erwartet")
	}
	if len(e.fx.got()) != 0 {
		t.Fatalf("Aufrufe: %v", e.fx.got())
	}
}

func TestReapplyNTPRejectsSymlink(t *testing.T) {
	linuxOnly(t)
	e := newEnv(t, "")
	prepState(t, e)
	if err := os.Symlink("/dev/zero", e.srv.NTPStatePath); err != nil {
		t.Fatal(err)
	}
	if err := reapplyWithin(t, e); err == nil {
		t.Fatal("Fehler erwartet")
	}
	other := filepath.Join(e.dir, "real")
	os.WriteFile(other, []byte("pool.ntp.org\n"), 0o644)
	os.Remove(e.srv.NTPStatePath)
	os.Symlink(other, e.srv.NTPStatePath)
	if err := reapplyWithin(t, e); err == nil {
		t.Fatal("Symlink auf gültige Datei muss abgelehnt werden")
	}
	if len(e.fx.got()) != 0 {
		t.Fatalf("Aufrufe: %v", e.fx.got())
	}
}

func TestReapplyNTPRejectsOversized(t *testing.T) {
	e := newEnv(t, "")
	prepState(t, e)
	os.WriteFile(e.srv.NTPStatePath, []byte(strings.Repeat("a", 5000)), 0o644)
	if err := reapplyWithin(t, e); err == nil {
		t.Fatal("Fehler erwartet")
	}
	if len(e.fx.got()) != 0 {
		t.Fatalf("Aufrufe: %v", e.fx.got())
	}
}

func TestReapplyNTPSequence(t *testing.T) {
	e := newEnv(t, "")
	prepState(t, e)
	os.WriteFile(e.srv.NTPStatePath, []byte("10.1.2.3\n"), 0o644)
	if err := reapplyWithin(t, e); err != nil {
		t.Fatal(err)
	}
	if got := e.fx.got(); !reflect.DeepEqual(got, ntpSeq) {
		t.Fatalf("got %v", got)
	}
}

func TestServeStartsDespiteBadNTPState(t *testing.T) {
	linuxOnly(t)
	e := newEnv(t, "")
	prepState(t, e)
	if err := syscall.Mkfifo(e.srv.NTPStatePath, 0o644); err != nil {
		t.Fatal(err)
	}
	e.srv.ReapplyNTPOnStart = true
	sock := startServer(t, e)
	if err := helperapi.New(sock).Call(context.Background(), CmdReboot, ""); err != nil {
		t.Fatal(err)
	}
}

func TestSetNTPWritesStateBeforeConf(t *testing.T) {
	e := newEnv(t, "")
	// Elternpfad ist eine Datei: der Zustandswert ist nicht schreibbar.
	blocker := filepath.Join(e.dir, "blocker")
	os.WriteFile(blocker, nil, 0o644)
	e.srv.NTPStatePath = filepath.Join(blocker, "ntp-server")
	if err := e.srv.Dispatch(context.Background(), CmdSetNTP, "pool.ntp.org"); err == nil {
		t.Fatal("Fehler erwartet")
	}
	if _, err := os.Stat(e.srv.NTPConfPath); err == nil {
		t.Fatal("conf trotz fehlgeschlagenem Zustandswert geschrieben")
	}
	if len(e.fx.got()) != 0 {
		t.Fatalf("Aufrufe: %v", e.fx.got())
	}
}

func TestSetTimeStopsWhenNTPOffFails(t *testing.T) {
	e := newEnv(t, "")
	e.fx.fail["timedatectl set-time 2026-11-05 10:00:00"] = errors.New("x")
	e.fx.fail["timedatectl set-ntp false"] = errors.New("aktiv")
	if err := e.srv.Dispatch(context.Background(), CmdSetTime, "2026-11-05T10:00:00Z"); err == nil {
		t.Fatal("Fehler erwartet")
	}
	if got := e.fx.got(); len(got) != 1 {
		t.Fatalf("got %v", got)
	}
}

func TestSetOverlayPreambleCountsAsAll(t *testing.T) {
	in := "dtoverlay=disable-wifi\nfoo=1\n[pi5]\nbar=2\n"
	if got := SetOverlay(in, "disable-wifi", true); got != in {
		t.Fatalf("ergänzt trotz Eintrag: %q", got)
	}
	want := "foo=1\n[pi5]\nbar=2\n"
	if got := SetOverlay(in, "disable-wifi", false); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestUnchangedConfigStillRemountsRo(t *testing.T) {
	e := newEnv(t, "[all]\ndtoverlay=disable-wifi\n")
	e.fx.fail["mount -o remount,ro /boot/firmware"] = errors.New("busy")
	err := e.srv.Dispatch(context.Background(), CmdWifiOff, "")
	if err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("err: %v", err)
	}
	if got := e.fx.got(); got[0] != "mount -o remount,ro /boot/firmware" {
		t.Fatalf("got %v", got)
	}
}

func TestVersionTooLongRejected(t *testing.T) {
	e := newEnv(t, "")
	long := strings.Repeat("1", 40) + ".0.0"
	if err := e.srv.Dispatch(context.Background(), CmdUpdateActivate, long); err == nil {
		t.Fatal("zu lange Version angenommen")
	}
	if len(e.upd.activated) != 0 {
		t.Fatal("Activate aufgerufen")
	}
}

func TestTrailingDataRejected(t *testing.T) {
	linuxOnly(t)
	e := newEnv(t, "")
	sock := startServer(t, e)
	m := rawCall(t, sock, `{"cmd":"reboot"}{"cmd":"reboot"}`+"\n")
	if m["error"] == nil {
		t.Fatalf("resp: %v", m)
	}
	if len(e.fx.got()) != 0 {
		t.Fatalf("Aufrufe: %v", e.fx.got())
	}
	if m := rawCall(t, sock, `{"cmd":"reboot"}  `+"\n"); m["ok"] != true {
		t.Fatalf("Leerraum abgelehnt: %v", m)
	}
}
