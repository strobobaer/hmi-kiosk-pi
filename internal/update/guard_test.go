package update

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func withFastPoll(t *testing.T) {
	t.Helper()
	old := pollInterval
	pollInterval = 5 * time.Millisecond
	t.Cleanup(func() { pollInterval = old })
}

// pendingRoot legt 1.0.0 (previous) und 1.1.0 (current, pending) an.
func pendingRoot(t *testing.T) (root, marker string) {
	t.Helper()
	root = t.TempDir()
	mkVersion(t, root, "1.0.0")
	mkVersion(t, root, "1.1.0")
	if err := Activate(root, "1.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := Activate(root, "1.1.0"); err != nil {
		t.Fatal(err)
	}
	return root, filepath.Join(t.TempDir(), "healthy")
}

func rollbackFn(root string, n *atomic.Int32) func(context.Context) error {
	return func(context.Context) error {
		n.Add(1)
		return Rollback(root)
	}
}

func TestGuardRollsBackWithoutHealthyMarkerAfter60s(t *testing.T) {
	withFastPoll(t)
	root, marker := pendingRoot(t)
	var n atomic.Int32
	// Die Frist ist ein Parameter (Produktion: 60 s); der Test nimmt 150 ms.
	start := time.Now()
	err := Guard(context.Background(), root, marker, 150*time.Millisecond, rollbackFn(root, &n))
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < 150*time.Millisecond {
		t.Fatal("zu früh zurückgeschaltet")
	}
	if n.Load() != 1 {
		t.Fatalf("Rückfall %d-mal", n.Load())
	}
	if c := link(t, root, "current"); c != "1.0.0" {
		t.Fatalf("current=%s", c)
	}
	if _, err := os.Stat(filepath.Join(appDir(root), "pending")); err == nil {
		t.Fatal("pending bleibt")
	}
}

func TestGuardKeepsVersionWithMarker(t *testing.T) {
	withFastPoll(t)
	root, marker := pendingRoot(t)
	var n atomic.Int32
	go func() {
		time.Sleep(30 * time.Millisecond)
		os.WriteFile(marker, []byte("1.1.0\n"), 0o644)
	}()
	if err := Guard(context.Background(), root, marker, 5*time.Second, rollbackFn(root, &n)); err != nil {
		t.Fatal(err)
	}
	if n.Load() != 0 {
		t.Fatal("Rückfall trotz Marke")
	}
	if c := link(t, root, "current"); c != "1.1.0" {
		t.Fatalf("current=%s", c)
	}
	if _, err := os.Stat(filepath.Join(appDir(root), "pending")); err == nil {
		t.Fatal("pending bleibt")
	}
}

func TestGuardWrongVersionMarkerDoesNotCount(t *testing.T) {
	withFastPoll(t)
	root, marker := pendingRoot(t)
	os.WriteFile(marker, []byte("1.0.0"), 0o644)
	var n atomic.Int32
	if err := Guard(context.Background(), root, marker, 100*time.Millisecond, rollbackFn(root, &n)); err != nil {
		t.Fatal(err)
	}
	if n.Load() != 1 || link(t, root, "current") != "1.0.0" {
		t.Fatalf("kein Rückfall: n=%d", n.Load())
	}
}

func TestGuardMarkerMustBeBoundedRegularFile(t *testing.T) {
	withFastPoll(t)
	root, marker := pendingRoot(t)
	os.WriteFile(marker, []byte("1.1.0"+strings.Repeat(" ", 100)), 0o644)
	var n atomic.Int32
	Guard(context.Background(), root, marker, 80*time.Millisecond, rollbackFn(root, &n))
	if n.Load() != 1 {
		t.Fatal("übergroße Marke zählte")
	}

	root, marker = pendingRoot(t)
	os.MkdirAll(marker, 0o755) // Verzeichnis statt Datei
	n.Store(0)
	Guard(context.Background(), root, marker, 80*time.Millisecond, rollbackFn(root, &n))
	if n.Load() != 1 {
		t.Fatal("Verzeichnis als Marke zählte")
	}

	root, marker = pendingRoot(t)
	target := filepath.Join(t.TempDir(), "t")
	os.WriteFile(target, []byte("1.1.0"), 0o644)
	if err := os.Symlink(target, marker); err != nil {
		t.Skip("keine Symlinks")
	}
	n.Store(0)
	Guard(context.Background(), root, marker, 80*time.Millisecond, rollbackFn(root, &n))
	if n.Load() != 1 {
		t.Fatal("Symlink als Marke zählte")
	}
}

func TestGuardWithoutPendingDoesNothing(t *testing.T) {
	root := t.TempDir()
	var n atomic.Int32
	start := time.Now()
	if err := Guard(context.Background(), root, filepath.Join(root, "m"), time.Hour, rollbackFn(root, &n)); err != nil {
		t.Fatal(err)
	}
	if n.Load() != 0 || time.Since(start) > time.Second {
		t.Fatal("wartete oder schaltete zurück ohne pending")
	}
}

func TestGuardRollbackErrorIsReturned(t *testing.T) {
	withFastPoll(t)
	root := t.TempDir()
	mkVersion(t, root, "1.0.0")
	Activate(root, "1.0.0") // erste Version, kein Vorgänger
	err := Guard(context.Background(), root, filepath.Join(root, "m"), 50*time.Millisecond, func(context.Context) error {
		return Rollback(root)
	})
	if err == nil || !strings.Contains(err.Error(), "Vorgänger") {
		t.Fatalf("err = %v", err)
	}
	if c := link(t, root, "current"); c != "1.0.0" {
		t.Fatalf("current=%s", c)
	}
	err = Guard(context.Background(), root, filepath.Join(root, "m"), 50*time.Millisecond, func(context.Context) error {
		return errors.New("restart kaputt")
	})
	if err == nil {
		t.Fatal("Fehler des Rückfalls verschluckt")
	}
}

func TestGuardCancelDoesNotRollBack(t *testing.T) {
	withFastPoll(t)
	root, marker := pendingRoot(t)
	ctx, cancel := context.WithCancel(context.Background())
	var n atomic.Int32
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	err := Guard(ctx, root, marker, 10*time.Second, rollbackFn(root, &n))
	if err == nil || n.Load() != 0 {
		t.Fatalf("err=%v n=%d", err, n.Load())
	}
	if _, e := os.Stat(filepath.Join(appDir(root), "pending")); e != nil {
		t.Fatal("pending darf bei Abbruch bleiben")
	}
}

// Stromausfall mitten in Activate: pending steht, current zeigt noch auf die alte Version.
func TestGuardResolvesInterruptedActivation(t *testing.T) {
	withFastPoll(t)
	root := t.TempDir()
	mkVersion(t, root, "1.0.0")
	mkVersion(t, root, "1.1.0")
	Activate(root, "1.0.0")
	os.WriteFile(filepath.Join(appDir(root), "pending"), []byte("1.1.0"), 0o644)
	var n atomic.Int32
	if err := Guard(context.Background(), root, filepath.Join(root, "m"), 50*time.Millisecond, rollbackFn(root, &n)); err != nil {
		t.Fatal(err)
	}
	if c := link(t, root, "current"); c != "1.0.0" {
		t.Fatalf("current=%s", c)
	}
	if _, e := os.Stat(filepath.Join(appDir(root), "pending")); e == nil {
		t.Fatal("pending bleibt")
	}
}
