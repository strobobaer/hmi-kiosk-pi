//go:build windows

package pdhterminal

import (
	"errors"
	"fmt"
	"image/color"
	"log"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Tray-Symbol unter Windows.
//
// Das Symbol lebt auf einem eigenen Thread (LockOSThread) mit Nachrichtenschleife. Es gibt genau eine Fensterklasse
// und eine Fensterprozedur je Prozess (syscall.NewCallback gibt nie frei). Das Fenster ist unsichtbar, aber kein
// Nachrichtenfenster: Nur normale Fenster erhalten „TaskbarCreated“, das der Explorer nach einem Neustart sendet
// (dann kommt das Symbol neu). Die Fensterprozedur blockiert nie, sperrt nichts und schreibt nichts ins Protokoll: Sie
// reiht Ereignisse in einen Kanal ein, den eine eigene Goroutine abarbeitet (Rückfrage, Rückrufe, Protokoll).

const (
	wmCommand  = 0x0111
	wmClose    = 0x0010
	wmTimer    = 0x0113
	wmLButtonU = 0x0202
	wmRButtonU = 0x0205
	wmContext  = 0x007B
	wmNull     = 0x0000
	wmApp      = 0x8000

	wmTrayIcon  = wmApp + 1 // Rückmeldung des Symbols
	wmTrayState = wmApp + 2 // Zustand hat sich geändert

	nimAdd    = 0
	nimModify = 1
	nimDelete = 2

	nifMessage = 0x1
	nifIcon    = 0x2
	nifTip     = 0x4

	mfString  = 0x0
	mfGrayed  = 0x1
	mfSeparat = 0x800

	tpmRightButton = 0x2
	tpmReturnCmd   = 0x100
	tpmBottomAlign = 0x20

	mbYesNo       = 0x4
	mbIconQuest   = 0x20
	mbDefButton2  = 0x100
	mbSetFg       = 0x10000
	mbTopmost     = 0x40000
	idYes         = 6
	smCxSmIcon    = 49
	smCySmIcon    = 50
	timerRetryAdd = 1
	retryAddEvery = 5000 // ms
)

// Befehlsnummern im Menü; die Prüfung kann sie über WM_COMMAND schicken.
const (
	cmdOpen     = 1
	cmdAdmin    = 2
	cmdSelftest = 3
	cmdQuit     = 4
)

const trayClassName = "PDHTerminalTray"

var (
	trUser32   = windows.NewLazySystemDLL("user32.dll")
	trShell32  = windows.NewLazySystemDLL("shell32.dll")
	trGdi32    = windows.NewLazySystemDLL("gdi32.dll")
	trKernel32 = windows.NewLazySystemDLL("kernel32.dll")

	pTrRegisterClassEx   = trUser32.NewProc("RegisterClassExW")
	pTrCreateWindowEx    = trUser32.NewProc("CreateWindowExW")
	pTrDestroyWindow     = trUser32.NewProc("DestroyWindow")
	pTrDefWindowProc     = trUser32.NewProc("DefWindowProcW")
	pTrGetMessage        = trUser32.NewProc("GetMessageW")
	pTrTranslateMessage  = trUser32.NewProc("TranslateMessage")
	pTrDispatchMessage   = trUser32.NewProc("DispatchMessageW")
	pTrPostMessage       = trUser32.NewProc("PostMessageW")
	pTrPostQuit          = trUser32.NewProc("PostQuitMessage")
	pTrRegisterWindowMsg = trUser32.NewProc("RegisterWindowMessageW")
	pTrCreatePopupMenu   = trUser32.NewProc("CreatePopupMenu")
	pTrAppendMenu        = trUser32.NewProc("AppendMenuW")
	pTrTrackPopupMenu    = trUser32.NewProc("TrackPopupMenu")
	pTrDestroyMenu       = trUser32.NewProc("DestroyMenu")
	pTrSetForeground     = trUser32.NewProc("SetForegroundWindow")
	pTrGetCursorPos      = trUser32.NewProc("GetCursorPos")
	pTrCreateIconInd     = trUser32.NewProc("CreateIconIndirect")
	pTrDestroyIcon       = trUser32.NewProc("DestroyIcon")
	pTrGetSystemMetrics  = trUser32.NewProc("GetSystemMetrics")
	pTrSetTimer          = trUser32.NewProc("SetTimer")
	pTrKillTimer         = trUser32.NewProc("KillTimer")
	pTrShellNotifyIcon   = trShell32.NewProc("Shell_NotifyIconW")
	pTrCreateBitmap      = trGdi32.NewProc("CreateBitmap")
	pTrDeleteObject      = trGdi32.NewProc("DeleteObject")
	pTrGetModuleHandle   = trKernel32.NewProc("GetModuleHandleW")
)

type trWndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

type trMsg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      [2]int32
}

type trIconInfo struct {
	Icon     int32
	XHotspot uint32
	YHotspot uint32
	Mask     uintptr
	Color    uintptr
}

// notifyIconData ist NOTIFYICONDATAW in der Fassung bis Windows Vista.
type notifyIconData struct {
	Size            uint32
	Hwnd            uintptr
	ID              uint32
	Flags           uint32
	CallbackMessage uint32
	Icon            uintptr
	Tip             [128]uint16
	State           uint32
	StateMask       uint32
	Info            [256]uint16
	Timeout         uint32
	InfoTitle       [64]uint16
	InfoFlags       uint32
	GUID            windows.GUID
	BalloonIcon     uintptr
}

// Ereignisse der Fensterprozedur an die Goroutine, die sie abarbeitet.
type trayEvent struct {
	cmd int    // Menübefehl, sonst 0
	log string // Protokollzeile, sonst leer
}

// Zustand je Prozess: Rückruf und Fensterklasse genau einmal.
var (
	trWndProcCb   = syscall.NewCallback(trayWndProc)
	trClassOnce   sync.Once
	trClassErr    error
	trTaskbarMsg  atomic.Uint32 // Nachricht „TaskbarCreated“
	trActive      atomic.Pointer[winTray]
	trClassNamePt = windows.StringToUTF16Ptr(trayClassName)
)

// winTray ist das Symbol. Felder, die nur der Symbolthread anfasst, tragen kein Schloss.
type winTray struct {
	events chan trayEvent
	state  atomic.Pointer[string] // gewünschter Grund; leer grün
	hwnd   atomic.Uintptr
	done   chan struct{} // schließt, wenn der Thread zu Ende ist
	stop   sync.Once

	// nur Symbolthread
	iconGreen, iconRed uintptr
	added              bool
	shownReason        string
}

func newTray() tray { return &winTray{} }

func (t *winTray) Start(a trayActions) error {
	if !trActive.CompareAndSwap(nil, t) {
		return errors.New("Tray-Symbol läuft schon")
	}
	t.events = make(chan trayEvent, 32)
	t.done = make(chan struct{})
	if t.state.Load() == nil {
		empty := ""
		t.state.Store(&empty)
	}
	ready := make(chan error, 1)
	go t.thread(ready)
	select {
	case err := <-ready:
		if err != nil {
			trActive.Store(nil)
			return err
		}
	case <-time.After(5 * time.Second):
		trActive.Store(nil)
		return errors.New("Tray-Thread nicht bereit")
	}
	go t.dispatch(a)
	return nil
}

func (t *winTray) SetState(reason string) {
	t.state.Store(&reason)
	if h := t.hwnd.Load(); h != 0 {
		pTrPostMessage.Call(h, wmTrayState, 0, 0)
	}
}

func (t *winTray) Stop() {
	t.stop.Do(func() {
		if h := t.hwnd.Load(); h != 0 {
			pTrPostMessage.Call(h, wmClose, 0, 0)
			select {
			case <-t.done:
			case <-time.After(3 * time.Second):
				log.Print("Tray-Symbol: Thread endet nicht")
			}
		}
		trActive.CompareAndSwap(t, nil)
	})
}

// dispatch arbeitet die Ereignisse der Fensterprozedur ab: Protokoll, Rückfrage, Rückrufe.
func (t *winTray) dispatch(a trayActions) {
	quitAsking := false
	for {
		select {
		case <-t.done:
			return
		case ev := <-t.events:
			switch {
			case ev.log != "":
				log.Print(ev.log)
			case ev.cmd == cmdOpen:
				go a.Open()
			case ev.cmd == cmdAdmin:
				go a.Admin()
			case ev.cmd == cmdSelftest:
				go a.Selftest()
			case ev.cmd == cmdQuit:
				if quitAsking {
					continue
				}
				quitAsking = true
				go func() {
					ok := confirmQuit()
					select {
					case t.events <- trayEvent{cmd: -1}: // gibt die Rückfrage wieder frei
					case <-t.done:
					}
					if ok {
						a.Quit()
					}
				}()
			case ev.cmd == -1:
				quitAsking = false
			}
		}
	}
}

// confirmQuit fragt nach; Nein ist die Vorgabe. Das Fenster liegt zuoberst, auch wenn sonst ein anderes Programm vorn ist.
func confirmQuit() bool {
	r, err := windows.MessageBox(0,
		windows.StringToUTF16Ptr("Soll das PDH-Terminal beendet werden?\n\nOhne laufendes Terminal werden Karten nicht angemeldet."),
		windows.StringToUTF16Ptr("PDH-Terminal beenden"),
		mbYesNo|mbIconQuest|mbDefButton2|mbSetFg|mbTopmost)
	return err == nil && r == idYes
}

func (t *winTray) post(ev trayEvent) {
	select {
	case t.events <- ev:
	default: // voll: das Ereignis fällt weg, die Fensterprozedur wartet nie
	}
}

// thread ist der Symbolthread mit Nachrichtenschleife.
func (t *winTray) thread(ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(t.done)

	hInst, _, _ := pTrGetModuleHandle.Call(0)
	trClassOnce.Do(func() {
		msg, _, _ := pTrRegisterWindowMsg.Call(uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("TaskbarCreated"))))
		trTaskbarMsg.Store(uint32(msg))
		wc := trWndClassEx{WndProc: trWndProcCb, Instance: hInst, ClassName: trClassNamePt}
		wc.Size = uint32(unsafe.Sizeof(wc))
		if a, _, e := pTrRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc))); a == 0 {
			trClassErr = e
		}
	})
	if trClassErr != nil {
		ready <- fmt.Errorf("Fensterklasse: %w", trClassErr)
		return
	}
	size := 16
	if w, _, _ := pTrGetSystemMetrics.Call(smCxSmIcon); w > 0 {
		size = int(w)
	}
	t.iconGreen, t.iconRed = makeIcon(size, trayGreen), makeIcon(size, trayRed)
	defer func() {
		for _, i := range []uintptr{t.iconGreen, t.iconRed} {
			if i != 0 {
				pTrDestroyIcon.Call(i)
			}
		}
	}()
	if t.iconGreen == 0 || t.iconRed == 0 {
		ready <- errors.New("Symbol nicht erzeugt")
		return
	}
	hwnd, _, e := pTrCreateWindowEx.Call(0, uintptr(unsafe.Pointer(trClassNamePt)),
		uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("PDH-Terminal"))), 0, 0, 0, 0, 0, 0, 0, hInst, 0)
	if hwnd == 0 {
		ready <- fmt.Errorf("Fenster: %w", e)
		return
	}
	t.hwnd.Store(hwnd)
	t.addIcon(hwnd, true)
	ready <- nil

	var m trMsg
	for {
		r, _, _ := pTrGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		pTrTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pTrDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// makeIcon macht aus dem gezeichneten Bild ein Windows-Symbol (32 Bit mit Alphakanal).
func makeIcon(size int, col color.NRGBA) uintptr {
	img := renderTrayIcon(size, col)
	bgra := make([]byte, size*size*4)
	for i := 0; i < size*size; i++ {
		p := img.Pix[i*4 : i*4+4]
		a := uint32(p[3])
		bgra[i*4], bgra[i*4+1], bgra[i*4+2], bgra[i*4+3] = byte(uint32(p[2])*a/255), byte(uint32(p[1])*a/255), byte(uint32(p[0])*a/255), p[3]
	}
	mask := make([]byte, ((size+15)/16*2)*size) // 1 Bit je Punkt, Zeilen auf 16 Bit; alles 0: Farbe zählt
	hc, _, _ := pTrCreateBitmap.Call(uintptr(size), uintptr(size), 1, 32, uintptr(unsafe.Pointer(&bgra[0])))
	mk, _, _ := pTrCreateBitmap.Call(uintptr(size), uintptr(size), 1, 1, uintptr(unsafe.Pointer(&mask[0])))
	defer func() {
		if hc != 0 {
			pTrDeleteObject.Call(hc)
		}
		if mk != 0 {
			pTrDeleteObject.Call(mk)
		}
	}()
	if hc == 0 || mk == 0 {
		return 0
	}
	ii := trIconInfo{Icon: 1, Mask: mk, Color: hc}
	h, _, _ := pTrCreateIconInd.Call(uintptr(unsafe.Pointer(&ii)))
	return h
}

// notifyData füllt die Daten für das aktuelle Symbol und den Tooltip.
func (t *winTray) notifyData(hwnd uintptr) notifyIconData {
	reason := ""
	if p := t.state.Load(); p != nil {
		reason = *p
	}
	d := notifyIconData{Hwnd: hwnd, ID: 1, Flags: nifMessage | nifIcon | nifTip, CallbackMessage: wmTrayIcon}
	d.Size = uint32(unsafe.Sizeof(d))
	d.Icon = t.iconGreen
	if reason != "" {
		d.Icon = t.iconRed
	}
	tip := windows.StringToUTF16(trayTip(reason))
	if len(tip) > len(d.Tip) {
		tip = append(tip[:len(d.Tip)-1], 0)
	}
	copy(d.Tip[:], tip)
	t.shownReason = reason
	return d
}

// addIcon legt das Symbol an (oder neu an, nach einem Neustart des Explorers). Schlägt es fehl, kommt ein Zeitgeber
// nach fünf Sekunden wieder (die Taskleiste ist beim Anmelden manchmal noch nicht da).
func (t *winTray) addIcon(hwnd uintptr, first bool) {
	if !first {
		t.deleteIcon(hwnd) // ein Rest (Explorer lebt noch) ließe das Anlegen scheitern; fehlt nichts, schadet es nicht
	}
	d := t.notifyData(hwnd)
	ok, _, e := pTrShellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&d)))
	if ok != 0 {
		pTrKillTimer.Call(hwnd, timerRetryAdd)
		t.added = true
		if first {
			t.post(trayEvent{log: "Tray-Symbol angelegt (Shell_NotifyIcon erfolgreich)"})
		} else {
			t.post(trayEvent{log: "Tray-Symbol nach Neustart der Taskleiste neu angelegt"})
		}
		return
	}
	t.added = false
	pTrSetTimer.Call(hwnd, timerRetryAdd, retryAddEvery, 0)
	t.post(trayEvent{log: fmt.Sprintf("Tray-Symbol nicht angelegt, neuer Versuch in 5 s: %v", e)})
}

func (t *winTray) modifyIcon(hwnd uintptr) {
	if !t.added {
		t.addIcon(hwnd, false)
		return
	}
	d := t.notifyData(hwnd)
	if ok, _, _ := pTrShellNotifyIcon.Call(nimModify, uintptr(unsafe.Pointer(&d))); ok == 0 {
		t.addIcon(hwnd, false) // das Symbol ist verschwunden
	}
}

func (t *winTray) deleteIcon(hwnd uintptr) {
	d := notifyIconData{Hwnd: hwnd, ID: 1}
	d.Size = uint32(unsafe.Sizeof(d))
	pTrShellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&d)))
	t.added = false
}

// showMenu zeigt das Menü an der Mausposition; TPM_RETURNCMD liefert die Wahl, ohne dass das Fenster sie zugestellt bekommt.
func (t *winTray) showMenu(hwnd uintptr) {
	menu, _, _ := pTrCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer pTrDestroyMenu.Call(menu)
	add := func(flags, id uintptr, text string) {
		var p *uint16
		if text != "" {
			p = windows.StringToUTF16Ptr(text)
		}
		pTrAppendMenu.Call(menu, flags, id, uintptr(unsafe.Pointer(p)))
	}
	add(mfString, cmdOpen, "Terminal öffnen")
	add(mfString, cmdAdmin, "Admin-Seite")
	add(mfString, cmdSelftest, "Selbstprüfung")
	add(mfSeparat, 0, "")
	add(mfString, cmdQuit, "Beenden")
	var pt [2]int32
	pTrGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	pTrSetForeground.Call(hwnd) // sonst schließt das Menü nicht, wenn man daneben klickt
	cmd, _, _ := pTrTrackPopupMenu.Call(menu, tpmRightButton|tpmReturnCmd|tpmBottomAlign, uintptr(pt[0]), uintptr(pt[1]), 0, hwnd, 0)
	pTrPostMessage.Call(hwnd, wmNull, 0, 0)
	if cmd != 0 {
		t.post(trayEvent{cmd: int(cmd)})
	}
}

// trayWndProc läuft im Symbolthread und blockiert nie (außer dem Menü, das seine eigene Schleife hat);
// es schreibt nichts ins Protokoll und sperrt nichts.
func trayWndProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	t := trActive.Load()
	if t == nil || hwnd != t.hwnd.Load() {
		r, _, _ := pTrDefWindowProc.Call(hwnd, msg, wparam, lparam)
		return r
	}
	switch {
	case msg == wmTrayIcon:
		switch uint32(lparam) {
		case wmLButtonU:
			t.post(trayEvent{cmd: cmdOpen})
		case wmRButtonU, wmContext:
			t.showMenu(hwnd)
		}
		return 0
	case msg == wmCommand:
		if id := int(wparam & 0xffff); id >= cmdOpen && id <= cmdQuit { // von Menü oder Prüfung
			t.post(trayEvent{cmd: id})
		}
		return 0
	case msg == wmTrayState:
		t.modifyIcon(hwnd)
		return 0
	case msg == wmTimer && wparam == timerRetryAdd:
		t.addIcon(hwnd, true)
		return 0
	case msg == wmClose:
		pTrKillTimer.Call(hwnd, timerRetryAdd)
		t.deleteIcon(hwnd)
		pTrDestroyWindow.Call(hwnd)
		return 0
	case msg == 0x0002: // WM_DESTROY
		pTrPostQuit.Call(0)
		return 0
	case msg == uintptr(trTaskbarMsg.Load()) && msg != 0:
		t.addIcon(hwnd, false) // Der Explorer wurde neu gestartet: das Symbol ist weg.
		return 0
	}
	r, _, _ := pTrDefWindowProc.Call(hwnd, msg, wparam, lparam)
	return r
}

// openURL öffnet die Adresse im Standardbrowser.
func openURL(url string) error {
	return windows.ShellExecute(0, windows.StringToUTF16Ptr("open"), windows.StringToUTF16Ptr(url), nil, nil, windows.SW_SHOWNORMAL)
}
