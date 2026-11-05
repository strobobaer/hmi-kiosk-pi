package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func sample() Config {
	c := Default()
	c.Terminal.Name = "Halle 1"
	c.HMIs = []HMI{{ID: "h1", Name: "Presse", Host: "10.0.0.5", Port: 5900, Takeover: StyleConfirm}}
	c.Kiosk.PermanentHMI = "h1"
	return c
}

func TestSaveThenLoadRoundTrips(t *testing.T) {
	s := Open(t.TempDir())
	want := sample()
	if err := s.Save(want); err != nil {
		t.Fatal(err)
	}
	got, info, err := s.Load()
	if err != nil || info.UsedFallback {
		t.Fatalf("err=%v info=%+v", err, info)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestSaveLeavesOldFileWhenTargetDirNotWritable(t *testing.T) {
	dir := t.TempDir()
	s := Open(dir)
	old := sample()
	if err := s.Save(old); err != nil {
		t.Fatal(err)
	}

	bad := sample()
	bad.HMIs[0].Port = 0
	if err := s.Save(bad); err == nil {
		t.Fatal("expected error for invalid config")
	}

	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)
	changed := sample()
	changed.Terminal.Name = "Anders"
	err := s.Save(changed)
	if err == nil && os.Geteuid() == 0 {
		// root ignores directory permissions
		os.Chmod(dir, 0o700)
		_ = s.Save(old)
	} else if err == nil {
		t.Fatal("expected error for unwritable dir")
	}

	got, _, lerr := s.Load()
	if lerr != nil {
		t.Fatal(lerr)
	}
	if !reflect.DeepEqual(got, old) {
		t.Fatalf("old state lost: %+v", got)
	}
}

func TestLoadCorruptFallsBackToGood(t *testing.T) {
	dir := t.TempDir()
	s := Open(dir)
	good := sample()
	if err := s.Save(good); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkGood(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{kaputt"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, info, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !info.UsedFallback || info.Reason == "" {
		t.Fatalf("info=%+v", info)
	}
	if !reflect.DeepEqual(got, good) {
		t.Fatalf("got %+v", got)
	}
}

func TestLoadCorruptWithoutGoodReturnsError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{kaputt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Open(dir).Load(); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadMissingReturnsDefault(t *testing.T) {
	got, info, err := Open(t.TempDir()).Load()
	if err != nil || info.UsedFallback {
		t.Fatalf("err=%v info=%+v", err, info)
	}
	if got.Terminal.ID == "" || got.Client.Profile != "remmina" || got.Kiosk.InactivityMinutes != 15 {
		t.Fatalf("not a default: %+v", got)
	}
}

func TestSaveLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	s := Open(dir)
	for i := 0; i < 3; i++ {
		if err := s.Save(sample()); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "config.json" {
		t.Fatalf("entries: %v", entries)
	}
}
