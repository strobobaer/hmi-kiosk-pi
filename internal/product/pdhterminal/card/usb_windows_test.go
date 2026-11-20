//go:build windows

package card

import (
	"context"
	"runtime"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// Haken und Raw Input lassen sich mehrfach hintereinander starten und stoppen (Rückrufe entstehen
// nur einmal, Thread und Haken werden freigegeben).
func TestHubStartStopRepeatedly(t *testing.T) {
	for i := 0; i < 5; i++ {
		unsub, err := hub.subscribe(&winSub{match: func(InputDevice) bool { return false }, fn: func(InputDevice, uint16, time.Time) {}})
		if err != nil {
			t.Fatalf("Start %d: %v", i, err)
		}
		unsub()
	}
	if hub.done != nil || len(hub.snapshot()) != 0 {
		t.Fatal("Hub nach dem Stopp nicht leer")
	}
}

func TestReadKeysStopsWithContext(t *testing.T) {
	u := &usbReader{sel: "ffff:ffff"}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- u.readKeys(ctx, func(uint16, time.Time) {}) }()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("readKeys endet nicht")
	}
	if hub.done != nil {
		t.Fatal("Hub läuft nach dem Ende weiter")
	}
}

func TestListInputDevicesWindows(t *testing.T) {
	for _, d := range ListInputDevices("ignoriert") {
		if d.Path == "" || d.Name == "" {
			t.Errorf("unvollständig: %+v", d)
		}
		if d.ID != "" && len(d.ID) != 9 {
			t.Errorf("Kennung %q nicht vvvv:pppp", d.ID)
		}
		t.Logf("%s | %s | %s", d.ID, d.Name, d.Path)
	}
}

func TestLearnTimesOut(t *testing.T) {
	_, _, err := learnPlatform(context.Background(), 150*time.Millisecond)
	if err == nil {
		t.Fatal("ohne Eingabe muss Learn scheitern")
	}
}

// observer ist ein zweiter Tastaturhaken, der VOR dem des Terminals angelegt wird und damit erst
// sieht, was dessen Haken durchlässt (spätere Haken kommen zuerst dran). Er schluckt die
// eingespielten Prüftasten 2, 3 und 4, damit sie nirgends eintippen.
type obsEvent struct {
	scan       uint32
	up, marked bool
	at         time.Time
}

var (
	obsCh = make(chan obsEvent, 256)
	obsCb = syscall.NewCallback(func(code, wp, lp uintptr) uintptr {
		if int32(code) >= 0 {
			k := (*kbdLLHook)(*(*unsafe.Pointer)(unsafe.Pointer(&lp)))
			if k.Flags&llkhfInjected != 0 && (k.ScanCode == 2 || k.ScanCode == 3 || k.ScanCode == 4 || k.ScanCode == 0x64) {
				select {
				case obsCh <- obsEvent{scan: k.ScanCode, up: k.Flags&llkhfUp != 0, marked: k.DwExtraInfo == replayMarker, at: time.Now()}:
				default:
				}
				if k.ScanCode != 0x64 {
					return 1
				}
			}
		}
		r, _, _ := pCallNextHookEx.Call(0, code, wp, lp)
		return r
	})
)

func startObserver(t *testing.T) (stop func()) {
	t.Helper()
	ready := make(chan uint32, 1)
	done := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(done)
		hInst, _, _ := pGetModuleHandleW.Call(0)
		hk, _, _ := pSetWindowsHookExW.Call(whKeyboardLL, obsCb, hInst, 0)
		if hk == 0 {
			ready <- 0
			return
		}
		defer pUnhookWindowsHookEx.Call(hk)
		tid, _, _ := pGetCurrentThreadID.Call()
		ready <- uint32(tid)
		var m winMsg
		for {
			r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
			if int32(r) <= 0 {
				return
			}
		}
	}()
	tid := <-ready
	if tid == 0 {
		t.Fatal("Beobachter-Haken nicht möglich")
	}
	return func() {
		pPostThreadMessageW.Call(uintptr(tid), wmQuit, 0, 0)
		<-done
	}
}

func sendScan(scan uint16, up bool) {
	fl := uint32(keyeventfScanCode)
	if up {
		fl |= keyeventfKeyUp
	}
	in := kbdInput{Type: inputKeyboard, Scan: scan, Flags: fl}
	pSendInput.Call(1, uintptr(unsafe.Pointer(&in)), unsafe.Sizeof(in))
}

func drainObs() {
	for {
		select {
		case <-obsCh:
		default:
			return
		}
	}
}

// Fremde (eingespielte) Tasten gehen nie verloren: Normalerweise laufen sie unberührt durch; behandelt
// das Terminal sie wie Tasten einer Tastatur, werden sie gehalten und mit Markierung in derselben
// Reihenfolge wiederholt, Nicht-Kartentasten laufen sofort durch.
func TestReplayPathKeepsOrderAndMarks(t *testing.T) {
	stopObs := startObserver(t)
	defer stopObs()
	injectedAsHardware = true
	defer func() { injectedAsHardware = false }()
	unsub, err := hub.subscribe(&winSub{match: func(InputDevice) bool { return false }, fn: func(InputDevice, uint16, time.Time) {}})
	if err != nil {
		t.Fatal(err)
	}
	defer unsub()
	drainObs()

	start := time.Now()
	for _, s := range []uint16{2, 3, 4} {
		sendScan(s, false)
		time.Sleep(3 * time.Millisecond)
		sendScan(s, true)
		time.Sleep(3 * time.Millisecond)
	}
	sendScan(0x64, false) // F13: keine Kartentaste, läuft sofort unberührt durch
	sendScan(0x64, true)

	var got []obsEvent
	deadline := time.After(2 * time.Second)
	for len(got) < 8 {
		select {
		case e := <-obsCh:
			got = append(got, e)
		case <-deadline:
			t.Fatalf("nur %d von 8 Ereignissen angekommen: %+v", len(got), got)
		}
	}
	// F13 läuft sofort durch, also vor den gehaltenen Kartentasten.
	if got[0].scan != 0x64 || got[0].marked {
		t.Errorf("F13 muss zuerst, unmarkiert ankommen: %+v", got[0])
	}
	want := []struct {
		scan uint32
		up   bool
	}{{2, false}, {2, true}, {3, false}, {3, true}, {4, false}, {4, true}}
	for i, w := range want {
		g := got[i+2]
		if g.scan != w.scan || g.up != w.up || !g.marked {
			t.Errorf("Ereignis %d: %+v, erwartet Scancode %d up=%v markiert", i, g, w.scan, w.up)
		}
	}
	t.Logf("alle sechs Kartentasten nach %v in Reihenfolge wiederholt", got[7].at.Sub(start))
}
