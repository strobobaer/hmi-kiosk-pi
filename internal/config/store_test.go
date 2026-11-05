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
	if err := s.MarkGood(); err != nil {
		t.Fatal(err)
	}

	bad := sample()
	bad.HMIs[0].Port = 0
	if err := s.Save(bad); err == nil {
		t.Fatal("expected error for invalid config")
	}

	// A non-empty directory in place of config.json makes the final Rename
	// fail even for root, exercising the write error and cleanup path.
	target := filepath.Join(dir, "config.json")
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(target, "blocker"), 0o700); err != nil {
		t.Fatal(err)
	}
	changed := sample()
	changed.Terminal.Name = "Anders"
	if err := s.Save(changed); err == nil {
		t.Fatal("expected error when rename target is blocked")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "config.json" && e.Name() != "config.json.good" {
			t.Errorf("leftover file %q", e.Name())
		}
	}

	got, info, lerr := s.Load()
	if lerr != nil || !info.UsedFallback {
		t.Fatalf("err=%v info=%+v", lerr, info)
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

func TestLoadMissingFallsBackToGood(t *testing.T) {
	dir := t.TempDir()
	s := Open(dir)
	good := sample()
	if err := s.Save(good); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkGood(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "config.json")); err != nil {
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

func TestLoadMissingWithCorruptGoodReturnsError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json.good"), []byte("{kaputt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Open(dir).Load(); err == nil {
		t.Fatal("expected error, not a default config")
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
