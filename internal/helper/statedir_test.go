package helper

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// /data gehört dem Dienstbenutzer; er könnte /data/system gegen einen Verweis oder
// einen eigenen Ordner tauschen. Der Helfer darf dann weder schreiben noch lesen.

func swapStateDirForSymlink(t *testing.T, e *env) string {
	t.Helper()
	target := filepath.Join(e.dir, "fremd")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(filepath.Dir(e.srv.NTPStatePath)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Dir(e.srv.NTPStatePath)); err != nil {
		t.Fatal(err)
	}
	return target
}

func TestSetNTPRejectsSymlinkedStateDir(t *testing.T) {
	linuxOnly(t)
	e := newEnv(t, "")
	target := swapStateDirForSymlink(t, e)
	if err := e.srv.Dispatch(context.Background(), CmdSetNTP, "pool.ntp.org"); err == nil {
		t.Fatal("Fehler erwartet")
	}
	if ents, _ := os.ReadDir(target); len(ents) != 0 {
		t.Fatalf("im Ziel des Verweises geschrieben: %v", ents)
	}
	if len(e.fx.got()) != 0 {
		t.Fatalf("Aufrufe: %v", e.fx.got())
	}
}

func TestSetNTPRejectsForeignOwnedStateDir(t *testing.T) {
	linuxOnly(t)
	if os.Geteuid() != 0 {
		t.Skip("braucht root für chown")
	}
	e := newEnv(t, "")
	prepState(t, e)
	if err := os.Chown(filepath.Dir(e.srv.NTPStatePath), 1100, 1100); err != nil {
		t.Fatal(err)
	}
	if err := e.srv.Dispatch(context.Background(), CmdSetNTP, "pool.ntp.org"); err == nil {
		t.Fatal("Fehler erwartet")
	}
	if _, err := os.Lstat(e.srv.NTPStatePath); err == nil {
		t.Fatal("Datei in fremdem Ordner geschrieben")
	}
}

func TestSetNTPRejectsWritableStateDir(t *testing.T) {
	linuxOnly(t)
	e := newEnv(t, "")
	prepState(t, e)
	if err := os.Chmod(filepath.Dir(e.srv.NTPStatePath), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := e.srv.Dispatch(context.Background(), CmdSetNTP, "pool.ntp.org"); err == nil {
		t.Fatal("Fehler erwartet")
	}
}

func TestSetNTPCreatesStateDirOwnedByHelper(t *testing.T) {
	linuxOnly(t)
	e := newEnv(t, "")
	if err := e.srv.Dispatch(context.Background(), CmdSetNTP, "pool.ntp.org"); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(filepath.Dir(e.srv.NTPStatePath))
	if err != nil {
		t.Fatal(err)
	}
	if !fi.IsDir() || fi.Mode().Perm()&0o022 != 0 {
		t.Fatalf("Ordner: %v", fi.Mode())
	}
	if got := e.read(t, e.srv.NTPStatePath); got != "pool.ntp.org\n" {
		t.Fatalf("got %q", got)
	}
	ents, _ := os.ReadDir(filepath.Dir(e.srv.NTPStatePath))
	if len(ents) != 1 {
		t.Fatalf("Reste im Ordner: %v", ents)
	}
}

func TestReapplyNTPRejectsSymlinkedStateDir(t *testing.T) {
	linuxOnly(t)
	e := newEnv(t, "")
	target := swapStateDirForSymlink(t, e)
	if err := os.WriteFile(filepath.Join(target, filepath.Base(e.srv.NTPStatePath)), []byte("10.1.2.3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := reapplyWithin(t, e); err == nil {
		t.Fatal("Fehler erwartet")
	}
	if len(e.fx.got()) != 0 {
		t.Fatalf("Aufrufe: %v", e.fx.got())
	}
}
