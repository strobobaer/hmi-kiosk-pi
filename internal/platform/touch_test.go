//go:build linux

package platform

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func inputEvent(typ, code uint16, val int32) []byte {
	b := make([]byte, inputEventSize)
	binary.NativeEndian.PutUint16(b[inputEventSize-8:], typ)
	binary.NativeEndian.PutUint16(b[inputEventSize-6:], code)
	binary.NativeEndian.PutUint32(b[inputEventSize-4:], uint32(val))
	return b
}

func TestWatchTouchCallsActivityOnEvent(t *testing.T) {
	dir := t.TempDir()
	var data []byte
	data = append(data, inputEvent(0, 0, 0)...)     // EV_SYN: ignored
	data = append(data, inputEvent(3, 0x35, 5)...)  // EV_ABS
	data = append(data, inputEvent(1, 0x14a, 1)...) // EV_KEY
	data = append(data, inputEvent(2, 0, 1)...)     // EV_REL: ignored
	if err := os.WriteFile(filepath.Join(dir, "event0"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	var n atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- WatchTouch(ctx, dir, func() { n.Add(1) }) }()
	deadline := time.Now().Add(3 * time.Second)
	for n.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n.Load() != 2 {
		t.Fatalf("activity calls = %d, want 2", n.Load())
	}
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("WatchTouch did not return after cancel")
	}
}

func TestWatchTouchNoDeviceRetriesUntilCancel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	err := WatchTouch(ctx, t.TempDir(), func() { t.Error("unexpected activity") })
	if err != context.DeadlineExceeded {
		t.Fatalf("err = %v", err)
	}
}

func TestWatchTouchPicksUpLateDevice(t *testing.T) {
	old := touchRescanInterval
	touchRescanInterval = 20 * time.Millisecond
	dir := t.TempDir()
	var n atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { WatchTouch(ctx, dir, func() { n.Add(1) }); close(done) }()
	defer func() { cancel(); <-done; touchRescanInterval = old }()
	time.Sleep(60 * time.Millisecond)
	os.WriteFile(filepath.Join(dir, "event1"), inputEvent(1, 0x14a, 1), 0o600)
	deadline := time.Now().Add(3 * time.Second)
	for n.Load() < 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n.Load() < 1 {
		t.Fatal("late device not picked up")
	}
}
