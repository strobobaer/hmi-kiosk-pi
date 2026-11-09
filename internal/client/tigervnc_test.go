package client

import (
	"crypto/des"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestTigerVNCPasswordTruncatesTo8AndUsesFixedKey(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "s")
	s := testSpec()
	s.Password = "abcdefghij"
	if _, err := (tigervnc{}).Prepare(dir, s); err != nil {
		t.Fatal(err)
	}
	pf := filepath.Join(dir, "passwd")
	ct, err := os.ReadFile(pf)
	if err != nil {
		t.Fatal(err)
	}
	if len(ct) != 8 {
		t.Fatalf("len %d", len(ct))
	}
	key, _ := hex.DecodeString("e84ad660c4721ae0")
	blk, _ := des.NewCipher(key)
	pt := make([]byte, 8)
	blk.Decrypt(pt, ct)
	if string(pt) != "abcdefgh" {
		t.Fatalf("decrypted %q", pt)
	}
	fi, _ := os.Stat(pf)
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode().Perm())
	}

	s.Password = "ab"
	if _, err := (tigervnc{}).Prepare(dir, s); err != nil {
		t.Fatal(err)
	}
	ct, _ = os.ReadFile(pf)
	blk.Decrypt(pt, ct)
	if string(pt) != "ab\x00\x00\x00\x00\x00\x00" {
		t.Fatalf("short password decrypted %q", pt)
	}
}

func TestTigerVNCArgsPlaceWindowBelowBar(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "s")
	s := testSpec()
	s.X, s.Y, s.W, s.H = 0, 48, 1920, 1032
	cmd, err := (tigervnc{}).Prepare(dir, s)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-geometry=1920x1032+0+48", "-PasswordFile=" + filepath.Join(dir, "passwd"), "-ViewOnly=1", "-Shared=1", "10.0.0.5::5900"}
	if cmd.Path != "vncviewer" || strings.Join(cmd.Args, "|") != strings.Join(want, "|") {
		t.Fatalf("cmd = %+v", cmd)
	}
	if slices.Contains(cmd.Args, "-FullScreen") {
		t.Fatal("-FullScreen verdeckt die Leiste")
	}
	s.ViewOnly = false
	s.Y, s.H = 0, 1080
	cmd, _ = (tigervnc{}).Prepare(dir, s)
	if cmd.Args[0] != "-geometry=1920x1080+0+0" || cmd.Args[2] != "-ViewOnly=0" {
		t.Fatalf("args %v", cmd.Args)
	}
}

func TestTigerVNCClassMatchesWindowRules(t *testing.T) {
	if TigerVNCClass != "Vncviewer" {
		t.Fatalf("Klasse %q", TigerVNCClass)
	}
}
