//go:build windows

package platform

import (
	"context"
	"errors"
	"runtime"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	whMouseLL     = 14
	wmMouseMove   = 0x0200
	wmLButtonDown = 0x0201
	wmLButtonUp   = 0x0202
	wmQuit        = 0x0012

	smCXScreen = 0
	smCYScreen = 1

	hookQueue  = 256 // Ereignisse, die zwischen Hook und Auswertung warten dürfen
	cornerTick = 250 * time.Millisecond
)

var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	procSetWindowsHookExW   = user32.NewProc("SetWindowsHookExW")
	procUnhookWindowsHookEx = user32.NewProc("UnhookWindowsHookEx")
	procCallNextHookEx      = user32.NewProc("CallNextHookEx")
	procGetMessageW         = user32.NewProc("GetMessageW")
	procPostThreadMessageW  = user32.NewProc("PostThreadMessageW")
	procGetSystemMetrics    = user32.NewProc("GetSystemMetrics")
)

// msllHook ist MSLLHOOKSTRUCT.
type msllHook struct {
	X, Y        int32
	MouseData   uint32
	Flags       uint32
	Time        uint32
	DwExtraInfo uintptr
}

// mouseEvent ist ein verkürztes Mausereignis aus dem Hook.
type mouseEvent struct {
	kind byte // 'd' Taste unten, 'u' Taste oben, 'm' Bewegung
	x, y int32
	at   time.Time
}

// hookSink nimmt die Ereignisse des Hooks an. Die Hook-Prozedur schreibt nur nicht blockierend hinein.
var hookSink atomic.Pointer[chan mouseEvent]

// mouseHookCB wird genau einmal je Prozess angelegt (syscall.NewCallback ist nicht freigebbar und auf
// 2000 Aufrufe begrenzt); WatchCorner darf also beliebig oft laufen.
var mouseHookCB = syscall.NewCallback(mouseHookProc)

// mouseHookProc ist die WH_MOUSE_LL-Prozedur. Sie blockiert nie, sperrt nie und protokolliert nie: Windows
// entfernt einen zu langsamen Hook, und die ganze Maus hinge daran. Ereignisse, für die kein Platz ist, fallen weg.
func mouseHookProc(code int32, wparam, lparam uintptr) uintptr {
	if code == 0 && lparam != 0 { // HC_ACTION
		var kind byte
		switch wparam {
		case wmLButtonDown:
			kind = 'd'
		case wmLButtonUp:
			kind = 'u'
		case wmMouseMove:
			kind = 'm'
		}
		if kind != 0 {
			if ch := hookSink.Load(); ch != nil {
				ms := (*msllHook)(*(*unsafe.Pointer)(unsafe.Pointer(&lparam))) // Zeiger des Systems, gültig während des Aufrufs
				select {
				case *ch <- mouseEvent{kind: kind, x: ms.X, y: ms.Y, at: time.Now()}:
				default:
				}
			}
		}
	}
	r, _, _ := procCallNextHookEx.Call(0, uintptr(uint32(code)), wparam, lparam)
	return r
}

// feedMouse setzt ein Mausereignis auf die Geste um.
func feedMouse(g *cornerGesture, ev mouseEvent) bool {
	switch ev.kind {
	case 'd':
		return g.Down(int64(ev.x), int64(ev.y), ev.at)
	case 'u':
		return g.Up(ev.at)
	case 'm':
		return g.Move(int64(ev.x), int64(ev.y), ev.at)
	}
	return false
}

// screenSize ist die Größe des Hauptbildschirms in den Einheiten, in denen der Hook die Zeigerlage meldet.
func screenSize() (w, h int) {
	cx, _, _ := procGetSystemMetrics.Call(smCXScreen)
	cy, _, _ := procGetSystemMetrics.Call(smCYScreen)
	return int(cx), int(cy)
}

// cornerGestureFor legt das Feld oben rechts in Bildschirmpixeln fest; cfg.ScreenW, falls gesetzt, geht vor.
func cornerGestureFor(cfg CornerCfg, w int) *cornerGesture {
	px := cfg.RegionPx
	if px <= 0 {
		px = defaultCornerPx
	}
	if cfg.ScreenW > 0 {
		w = cfg.ScreenW
	}
	if w <= 0 {
		w = 1920
	}
	return newCornerGesture(cfg.Hold, int64(w-px), int64(px))
}

// WatchCorner überwacht die Maus (und damit jede Berührung, Windows macht daraus Mausereignisse) über einen
// WH_MOUSE_LL-Hook und ruft hit, wenn die linke Taste cfg.Hold lang im Feld oben rechts gehalten wird. devDir
// ist unter Windows ohne Bedeutung. Der Hook läuft auf einem eigenen, festen Thread mit Nachrichtenschleife; die
// Auswertung geschieht in dieser Goroutine. Mit ctx endet alles.
func WatchCorner(ctx context.Context, devDir string, cfg CornerCfg, hit func()) error {
	w, _ := screenSize()
	g := cornerGestureFor(cfg, w)
	events := make(chan mouseEvent, hookQueue)
	if !hookSink.CompareAndSwap(nil, &events) {
		return errors.New("Maus-Hook läuft schon")
	}
	defer hookSink.Store(nil)

	type started struct {
		tid uint32
		err error
	}
	ready := make(chan started, 1)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		hook, _, err := procSetWindowsHookExW.Call(whMouseLL, mouseHookCB, 0, 0)
		if hook == 0 {
			ready <- started{err: err}
			return
		}
		ready <- started{tid: windows.GetCurrentThreadId()}
		var msg [48]byte // MSG; GetMessage liefert 0 bei WM_QUIT
		for {
			r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg[0])), 0, 0, 0)
			if int32(r) <= 0 {
				break
			}
		}
		procUnhookWindowsHookEx.Call(hook)
	}()
	st := <-ready
	if st.err != nil {
		<-stopped
		return st.err
	}

	t := time.NewTicker(cornerTick)
	defer t.Stop()
	defer func() {
		procPostThreadMessageW.Call(uintptr(st.tid), wmQuit, 0, 0)
		<-stopped
	}()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev := <-events:
			if feedMouse(g, ev) {
				hit()
			}
		case now := <-t.C:
			if g.Tick(now) {
				hit()
			}
		}
	}
}
