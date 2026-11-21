//go:build windows

package platform

import (
	"context"
	"runtime"
	"testing"
	"time"
	"unsafe"
)

func TestFeedMouseGesture(t *testing.T) {
	g := cornerGestureFor(CornerCfg{ScreenW: 1920, ScreenH: 1080}, 0)
	ev := func(k byte, x, y int32, after time.Duration) mouseEvent {
		return mouseEvent{kind: k, x: x, y: y, at: t0.Add(after)}
	}
	if feedMouse(g, ev('d', 1900, 5, 0)) {
		t.Fatal("Aufsetzen trifft nicht")
	}
	if feedMouse(g, ev('m', 1901, 6, 4900*time.Millisecond)) {
		t.Fatal("4,9 s trifft nicht")
	}
	if !feedMouse(g, ev('m', 1902, 6, 5*time.Second)) {
		t.Fatal("5 s trifft")
	}
	// Antippen: oben abgehoben vor 5 s.
	g = cornerGestureFor(CornerCfg{ScreenW: 1920, ScreenH: 1080}, 0)
	feedMouse(g, ev('d', 1900, 5, 0))
	if feedMouse(g, ev('u', 1900, 5, time.Second)) || g.Tick(t0.Add(10*time.Second)) {
		t.Fatal("Antippen trifft nicht")
	}
}

func TestMouseHookProcQueuesWithoutBlocking(t *testing.T) {
	hs := &hookState{keys: make(chan mouseEvent, 1), wake: make(chan struct{}, 1)}
	hookSink.Store(hs)
	defer hookSink.Store(nil)
	ms := msllHook{X: 1900, Y: 3}
	lp := uintptr(unsafe.Pointer(&ms))
	mouseHookProc(0, wmLButtonDown, lp)
	mouseHookProc(0, wmLButtonUp, lp) // Kanal voll: darf nicht blockieren
	mouseHookProc(0, 0x0204, lp)      // rechte Taste: ignoriert
	if len(hs.keys) != 1 {
		t.Fatalf("Warteschlange: %d", len(hs.keys))
	}
	if e := <-hs.keys; e.kind != 'd' || e.x != 1900 || e.y != 3 {
		t.Fatalf("Ereignis: %+v", e)
	}
	// Bewegungen werden zusammengefasst und stauen nie den Tastenkanal.
	for i := 0; i < 1000; i++ {
		ms.X = int32(i)
		mouseHookProc(0, wmMouseMove, lp)
	}
	if len(hs.keys) != 0 {
		t.Fatalf("Bewegungen im Tastenkanal: %d", len(hs.keys))
	}
	if mv := hs.move.Load(); mv == nil || mv.x != 999 {
		t.Fatalf("letzte Bewegung: %+v", mv)
	}
}

func TestWatchCornerInstallsAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- WatchCorner(ctx, "", CornerCfg{}, func() {}) }()
	deadline := time.Now().Add(5 * time.Second)
	for hookSink.Load() == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if hookSink.Load() == nil {
		t.Fatal("Hook nicht angelegt")
	}
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("Ende: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("WatchCorner endet nicht")
	}
	// Ein zweiter Lauf geht wieder (Callback nur einmal angelegt).
	ctx2, cancel2 := context.WithCancel(context.Background())
	go func() { done <- WatchCorner(ctx2, "", CornerCfg{}, func() {}) }()
	time.Sleep(300 * time.Millisecond)
	cancel2()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("zweiter Lauf endet nicht")
	}
}

// Sofortiges Beenden nach dem Start darf nie hängen (die Nachrichtenschlange des Hook-Threads entsteht früh genug).
func TestWatchCornerImmediateCancelNeverHangs(t *testing.T) {
	for i := 0; i < 200; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- WatchCorner(ctx, "", CornerCfg{}, func() {}) }()
		if i%2 == 0 {
			runtime.Gosched()
		}
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatalf("Durchlauf %d hängt", i)
		}
	}
}
