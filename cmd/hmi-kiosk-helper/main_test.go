package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"hmikiosk/internal/helper"
	"hmikiosk/internal/update"
)

var _ helper.UpdateOps = update.Ops{}

func setup(t *testing.T) (root, marker string) {
	t.Helper()
	root = t.TempDir()
	for _, v := range []string{"1.0.0", "1.1.0"} {
		d := filepath.Join(root, "app", v)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(d, "hmi-kiosk"), []byte("x"), 0o755)
		if err := update.Activate(root, v); err != nil {
			t.Fatal(err)
		}
	}
	return root, filepath.Join(t.TempDir(), "healthy")
}

func recorder(calls *[]string) helper.Exec {
	return func(_ context.Context, name string, args ...string) error {
		*calls = append(*calls, strings.Join(append([]string{name}, args...), " "))
		return nil
	}
}

func TestUpdateGuardTimeoutRollsBackAndRestarts(t *testing.T) {
	root, marker := setup(t)
	var calls []string
	if err := runGuard(context.Background(), root, marker, 100*time.Millisecond, recorder(&calls)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, []string{"systemctl reset-failed hmi-kiosk.service", "systemctl restart hmi-kiosk.service"}) {
		t.Fatalf("calls %v", calls)
	}
	cur, _ := os.Readlink(filepath.Join(root, "app", "current"))
	if cur != "1.0.0" {
		t.Fatalf("current=%s", cur)
	}
}

func TestUpdateGuardHealthyKeepsVersion(t *testing.T) {
	root, marker := setup(t)
	os.WriteFile(marker, []byte("1.1.0\n"), 0o644)
	var calls []string
	if err := runGuard(context.Background(), root, marker, time.Second, recorder(&calls)); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 0 {
		t.Fatalf("calls %v", calls)
	}
	if _, err := os.Stat(filepath.Join(root, "app", "pending")); err == nil {
		t.Fatal("pending bleibt")
	}
}

func TestUpdateGuardWithoutPendingExitsClean(t *testing.T) {
	var calls []string
	if err := runGuard(context.Background(), t.TempDir(), "/nicht/da", time.Hour, recorder(&calls)); err != nil || len(calls) != 0 {
		t.Fatalf("%v %v", err, calls)
	}
}

func TestUpdateGuardNoPreviousFails(t *testing.T) {
	root := t.TempDir()
	d := filepath.Join(root, "app", "1.0.0")
	os.MkdirAll(d, 0o755)
	os.WriteFile(filepath.Join(d, "hmi-kiosk"), []byte("x"), 0o755)
	update.Activate(root, "1.0.0")
	var calls []string
	err := runGuard(context.Background(), root, filepath.Join(root, "m"), 50*time.Millisecond, recorder(&calls))
	if err == nil || len(calls) != 0 {
		t.Fatalf("err=%v calls=%v", err, calls)
	}
}
