//go:build windows

package card

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// USB-Tastaturleser unter Windows. Ein Thread mit eigener Nachrichtenschleife trägt zugleich den
// Tastaturhaken (WH_KEYBOARD_LL) und das Raw-Input-Fenster; die Entscheidung („halten und
// wiederholen“, siehe intercept.go) läuft darum ohne Sperren. Die Rückrufe werden je Prozess genau
// einmal angelegt (syscall.NewCallback gibt sie nie wieder frei).

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	hidDLL   = windows.NewLazySystemDLL("hid.dll")

	pRegisterClassExW        = user32.NewProc("RegisterClassExW")
	pCreateWindowExW         = user32.NewProc("CreateWindowExW")
	pDestroyWindow           = user32.NewProc("DestroyWindow")
	pDefWindowProcW          = user32.NewProc("DefWindowProcW")
	pRegisterRawInputDevices = user32.NewProc("RegisterRawInputDevices")
	pGetRawInputData         = user32.NewProc("GetRawInputData")
	pGetRawInputDeviceList   = user32.NewProc("GetRawInputDeviceList")
	pGetRawInputDeviceInfoW  = user32.NewProc("GetRawInputDeviceInfoW")
	pSetWindowsHookExW       = user32.NewProc("SetWindowsHookExW")
	pCallNextHookEx          = user32.NewProc("CallNextHookEx")
	pUnhookWindowsHookEx     = user32.NewProc("UnhookWindowsHookEx")
	pGetMessageW             = user32.NewProc("GetMessageW")
	pTranslateMessage        = user32.NewProc("TranslateMessage")
	pDispatchMessageW        = user32.NewProc("DispatchMessageW")
	pPostThreadMessageW      = user32.NewProc("PostThreadMessageW")
	pSetTimer                = user32.NewProc("SetTimer")
	pKillTimer               = user32.NewProc("KillTimer")
	pSendInput               = user32.NewProc("SendInput")
	pGetCurrentThreadID      = kernel32.NewProc("GetCurrentThreadId")
	pGetModuleHandleW        = kernel32.NewProc("GetModuleHandleW")
	pHidGetProductString     = hidDLL.NewProc("HidD_GetProductString")
)

const (
	wmInput              = 0x00FF
	wmInputDeviceChange  = 0x00FE
	wmTimer              = 0x0113
	wmQuit               = 0x0012
	ridevRemove          = 0x00000001
	ridevInputSink       = 0x00000100
	ridevDevNotify       = 0x00002000
	ridInput             = 0x10000003
	ridiDeviceName       = 0x20000007
	rimTypeKeyboard      = 1
	riKeyBreak           = 1
	riKeyE0              = 2
	whKeyboardLL         = 13
	llkhfExtended        = 0x01
	llkhfInjected        = 0x10
	llkhfLowerILInjected = 0x02
	llkhfUp              = 0x80
	keyeventfExtendedKey = 0x1
	keyeventfKeyUp       = 0x2
	keyeventfScanCode    = 0x8
	inputKeyboard        = 1
	timerID              = 1

	// replayMarker steht in dwExtraInfo jeder Taste, die dieses Programm selbst wiederholt
	// ("PDH1"); der Haken lässt sie durch, und ihr Raw-Ereignis wird ignoriert.
	replayMarker = 0x50444831
)

var hwndMessage = ^uintptr(2) // HWND_MESSAGE (-3)

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   windows.Handle
	Icon       windows.Handle
	Cursor     windows.Handle
	Background windows.Handle
	MenuName   *uint16
	ClassName  *uint16
	IconSm     windows.Handle
}

type rawInputDevice struct {
	UsagePage uint16
	Usage     uint16
	Flags     uint32
	Target    windows.HWND
}

type rawInputDeviceList struct {
	Device uintptr
	Type   uint32
}

type rawInputHeader struct {
	Type   uint32
	Size   uint32
	Device uintptr
	WParam uintptr
}

type rawKeyboard struct {
	MakeCode         uint16
	Flags            uint16
	Reserved         uint16
	VKey             uint16
	Message          uint32
	ExtraInformation uint32
}

type rawInputKbd struct {
	Header rawInputHeader
	Kbd    rawKeyboard
}

type kbdLLHook struct {
	VkCode      uint32
	ScanCode    uint32
	Flags       uint32
	Time        uint32
	DwExtraInfo uintptr
}

type winMsg struct {
	Hwnd    windows.HWND
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      [2]int32
}

// kbdInput ist INPUT (Typ Tastatur) von SendInput, 40 Byte unter amd64.
type kbdInput struct {
	Type  uint32
	_     uint32
	Vk    uint16
	Scan  uint16
	Flags uint32
	Time  uint32
	_     uint32
	Extra uintptr
	_     [8]byte
}

// Die Rückrufe entstehen einmal je Prozess.
var (
	hookCb    = syscall.NewCallback(hookProc)
	wndProcCb = syscall.NewCallback(wndProc)

	classOnce sync.Once
	classErr  error
	className *uint16
)

var vidPid = regexp.MustCompile(`(?i)VID_([0-9a-f]{4})&PID_([0-9a-f]{4})`)

// rawDeviceName liest den Gerätepfad (\\?\HID#VID_xxxx&PID_xxxx#...) zu einem Raw-Input-Handle.
func rawDeviceName(h uintptr) string {
	var n uint32
	pGetRawInputDeviceInfoW.Call(h, ridiDeviceName, 0, uintptr(unsafe.Pointer(&n)))
	if n == 0 {
		return ""
	}
	buf := make([]uint16, n)
	r, _, _ := pGetRawInputDeviceInfoW.Call(h, ridiDeviceName, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n)))
	if int32(r) <= 0 {
		return ""
	}
	return windows.UTF16ToString(buf)
}

// productString liest den Produktnamen des HID-Geräts; leer, wenn er nicht lesbar ist.
func productString(path string) string {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return ""
	}
	h, err := windows.CreateFile(p, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	var buf [128]uint16
	r, _, _ := pHidGetProductString.Call(uintptr(h), uintptr(unsafe.Pointer(&buf[0])), unsafe.Sizeof(buf))
	if r == 0 {
		return ""
	}
	return strings.TrimSpace(windows.UTF16ToString(buf[:]))
}

// deviceFromPath baut das Gerät zum Pfad: Kennung vvvv:pppp aus VID_xxxx&PID_xxxx (klein), Name aus dem
// Produktstring, sonst aus dem Pfad.
func deviceFromPath(path string) InputDevice {
	d := InputDevice{Path: path}
	if m := vidPid.FindStringSubmatch(path); m != nil {
		d.ID = strings.ToLower(m[1] + ":" + m[2])
	}
	d.Name = productString(path)
	if d.Name == "" {
		d.Name = "Tastatur"
		if d.ID != "" {
			d.Name += " " + d.ID
		}
	}
	return d
}

// ListInputDevices listet die Tastaturen (Raw Input); dir wird ignoriert. Ein Leser mit mehreren
// Tastaturknoten erscheint einmal.
func ListInputDevices(dir string) []InputDevice {
	var n uint32
	if r, _, _ := pGetRawInputDeviceList.Call(0, uintptr(unsafe.Pointer(&n)), unsafe.Sizeof(rawInputDeviceList{})); int32(r) < 0 || n == 0 {
		return nil
	}
	list := make([]rawInputDeviceList, n)
	r, _, _ := pGetRawInputDeviceList.Call(uintptr(unsafe.Pointer(&list[0])), uintptr(unsafe.Pointer(&n)), unsafe.Sizeof(rawInputDeviceList{}))
	if int32(r) <= 0 {
		return nil
	}
	var out []InputDevice
	seen := map[string]bool{}
	for _, e := range list[:r] {
		if e.Type != rimTypeKeyboard {
			continue
		}
		path := rawDeviceName(e.Device)
		if path == "" {
			continue
		}
		d := deviceFromPath(path)
		key := d.ID + "|" + d.Name
		if d.ID != "" && seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, d)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// winSub ist ein Abnehmer der abgefangenen Tasten.
type winSub struct {
	match func(InputDevice) bool // Geräte, deren Kartentasten verschluckt werden
	learn bool                   // alle Tastaturen anlernen: schluckt alle Kartentasten, nur dieser Abnehmer bekommt sie
	fn    func(dev InputDevice, code uint16, at time.Time)
}

// kbdHub besitzt den einen Thread je Prozess; er läuft, solange es Abnehmer gibt.
type kbdHub struct {
	startMu sync.Mutex // Start und Stopp
	mu      sync.Mutex // subs
	subs    []*winSub
	tid     uint32
	done    chan struct{}
}

var hub kbdHub

func (h *kbdHub) snapshot() []*winSub {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]*winSub(nil), h.subs...)
}

// subscribe meldet einen Abnehmer an und startet bei Bedarf den Thread; das Ergebnis meldet ihn ab
// und stoppt den Thread, wenn es der letzte war (Haken und Raw-Input werden freigegeben).
func (h *kbdHub) subscribe(s *winSub) (func(), error) {
	h.startMu.Lock()
	defer h.startMu.Unlock()
	h.mu.Lock()
	first := len(h.subs) == 0
	h.subs = append(h.subs, s)
	h.mu.Unlock()
	if first {
		ready := make(chan error, 1)
		done := make(chan struct{})
		go runLoop(ready, done, h)
		if err := <-ready; err != nil {
			h.mu.Lock()
			h.subs = nil
			h.mu.Unlock()
			return nil, err
		}
		h.done = done
	}
	return func() {
		h.startMu.Lock()
		defer h.startMu.Unlock()
		h.mu.Lock()
		for i, x := range h.subs {
			if x == s {
				h.subs = append(h.subs[:i:i], h.subs[i+1:]...)
				break
			}
		}
		last := len(h.subs) == 0
		h.mu.Unlock()
		if last && h.done != nil {
			pPostThreadMessageW.Call(uintptr(h.tid), wmQuit, 0, 0)
			<-h.done
			h.done = nil
		}
	}, nil
}

// dispatch reicht eine verschluckte Taste an die Abnehmer: beim Anlernen nur an diesen.
func (h *kbdHub) dispatch(dev InputDevice, code uint16, at time.Time) {
	subs := h.snapshot()
	for _, s := range subs {
		if s.learn {
			s.fn(dev, code, at)
			return
		}
	}
	for _, s := range subs {
		if s.match != nil && s.match(dev) {
			s.fn(dev, code, at)
		}
	}
}

// classify: Gehört das Raw-Input-Gerät zu einem zu fangenden Leser? Eingespielte Tasten haben kein
// Gerät (0) und gehören nie dazu.
func (h *kbdHub) classify(info InputDevice, dev uintptr) bool {
	if dev == 0 {
		return false
	}
	for _, s := range h.snapshot() {
		if s.learn || (s.match != nil && s.match(info)) {
			return true
		}
	}
	return false
}

// loopState gehört dem Hub-Thread; nur dieser Thread liest und schreibt ihn (Haken und
// Fensterprozedur laufen in dessen Nachrichtenschleife).
type loopState struct {
	hub   *kbdHub
	g     gate
	hwnd  uintptr
	cache map[uintptr]InputDevice
}

var cur *loopState

// injectedAsHardware lässt die Prüfung eingespielte Tasten wie Tasten einer Tastatur behandeln, damit
// sich der Weg „halten und wiederholen“ ohne zweite Tastatur ausführen lässt. Nur Tests setzen es.
var injectedAsHardware bool

func (l *loopState) device(h uintptr) InputDevice {
	if d, ok := l.cache[h]; ok {
		return d
	}
	d := deviceFromPath(rawDeviceName(h))
	l.cache[h] = d
	return d
}

// apply führt die Folgen einer Entscheidung aus.
func (l *loopState) apply(acts []action) {
	for _, a := range acts {
		switch a.Kind {
		case actDrop:
			if code, ok := keyCode(a.Key); ok && !a.Key.Up {
				l.hub.dispatch(l.device(a.Dev), code, a.At)
			}
		case actReplay:
			replay(a.Key)
		}
	}
	l.rearm()
}

// rearm stellt den Zeitgeber auf die nächste Frist.
func (l *loopState) rearm() {
	dl, ok := l.g.Deadline()
	if !ok {
		pKillTimer.Call(l.hwnd, timerID)
		return
	}
	ms := time.Until(dl).Milliseconds()
	if ms < 1 {
		ms = 1
	}
	pSetTimer.Call(l.hwnd, timerID, uintptr(ms), 0)
}

// replay spielt die Taste mit Markierung neu ein (Scancode, so wie sie von der Tastatur kam).
func replay(k keyEvent) {
	fl := uint32(keyeventfScanCode)
	if k.Up {
		fl |= keyeventfKeyUp
	}
	if k.Ext {
		fl |= keyeventfExtendedKey
	}
	in := kbdInput{Type: inputKeyboard, Scan: k.Scan, Flags: fl, Extra: replayMarker}
	pSendInput.Call(1, uintptr(unsafe.Pointer(&in)), unsafe.Sizeof(in))
}

// hookProc ist der Tastaturhaken. Er rührt nur Kartentasten an (siehe gate.Hook).
func hookProc(code, wp, lp uintptr) uintptr {
	if int32(code) >= 0 && cur != nil {
		k := (*kbdLLHook)(*(*unsafe.Pointer)(unsafe.Pointer(&lp)))
		h := hookEvent{
			keyEvent: keyEvent{Scan: uint16(k.ScanCode), Ext: k.Flags&llkhfExtended != 0, Up: k.Flags&llkhfUp != 0},
			Marked:   k.DwExtraInfo == replayMarker,
			Injected: !injectedAsHardware && k.Flags&(llkhfInjected|llkhfLowerILInjected) != 0,
		}
		block, acts := cur.g.Hook(h, time.Now())
		cur.apply(acts)
		if block {
			return 1
		}
	}
	r, _, _ := pCallNextHookEx.Call(0, code, wp, lp)
	return r
}

// wndProc nimmt WM_INPUT (Gerät der Taste), den Zeitgeber und Gerätewechsel entgegen.
func wndProc(hwnd, msg, wp, lp uintptr) uintptr {
	l := cur
	if l != nil {
		switch msg {
		case wmInput:
			l.onRawInput(lp)
		case wmTimer:
			if wp == timerID {
				l.apply(l.g.Tick(time.Now()))
			}
		case wmInputDeviceChange:
			l.cache = map[uintptr]InputDevice{}
		}
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, msg, wp, lp)
	return r
}

func (l *loopState) onRawInput(lp uintptr) {
	var buf [64]byte
	size := uint32(len(buf))
	r, _, _ := pGetRawInputData.Call(lp, ridInput, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), unsafe.Sizeof(rawInputHeader{}))
	if int32(r) <= 0 {
		return
	}
	ri := (*rawInputKbd)(unsafe.Pointer(&buf[0]))
	if ri.Header.Type != rimTypeKeyboard || ri.Kbd.ExtraInformation == replayMarker {
		return
	}
	if ri.Kbd.MakeCode == 0 || ri.Kbd.MakeCode == 0xFF { // Überlauf oder Scancode-lose Ereignisse
		return
	}
	k := keyEvent{Scan: ri.Kbd.MakeCode, Ext: ri.Kbd.Flags&riKeyE0 != 0, Up: ri.Kbd.Flags&riKeyBreak != 0}
	re := rawEvent{keyEvent: k, Dev: ri.Header.Device, At: time.Now()}
	if isCandidate(k) {
		re.Reader = l.hub.classify(l.device(re.Dev), re.Dev)
	}
	l.apply(l.g.Raw(re, re.At))
}

func registerClass() error {
	classOnce.Do(func() {
		hInst, _, _ := pGetModuleHandleW.Call(0)
		className, classErr = windows.UTF16PtrFromString("PDHTerminalKbdIntercept")
		if classErr != nil {
			return
		}
		wc := wndClassEx{Instance: windows.Handle(hInst), ClassName: className, WndProc: wndProcCb}
		wc.Size = uint32(unsafe.Sizeof(wc))
		if a, _, e := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); a == 0 {
			classErr = fmt.Errorf("Fensterklasse: %w", e)
		}
	})
	return classErr
}

// runLoop ist der Hub-Thread: Fenster, Raw Input und Haken anlegen, Nachrichten pumpen, am Ende alles
// wieder freigeben. ready meldet den Start (oder seinen Fehler), done das Ende.
func runLoop(ready chan<- error, done chan<- struct{}, h *kbdHub) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(done)

	fail := func(err error) { ready <- err }
	if err := registerClass(); err != nil {
		fail(err)
		return
	}
	hInst, _, _ := pGetModuleHandleW.Call(0)
	hwnd, _, e := pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(className)), 0, 0, 0, 0, 0, 0, hwndMessage, 0, hInst, 0)
	if hwnd == 0 {
		fail(fmt.Errorf("Nachrichtenfenster: %w", e))
		return
	}
	defer pDestroyWindow.Call(hwnd)
	rid := rawInputDevice{UsagePage: 1, Usage: 6, Flags: ridevInputSink | ridevDevNotify, Target: windows.HWND(hwnd)}
	if r, _, e := pRegisterRawInputDevices.Call(uintptr(unsafe.Pointer(&rid)), 1, unsafe.Sizeof(rid)); r == 0 {
		fail(fmt.Errorf("Raw Input: %w", e))
		return
	}
	defer func() {
		off := rawInputDevice{UsagePage: 1, Usage: 6, Flags: ridevRemove}
		pRegisterRawInputDevices.Call(uintptr(unsafe.Pointer(&off)), 1, unsafe.Sizeof(off))
	}()
	cur = &loopState{hub: h, hwnd: hwnd, cache: map[uintptr]InputDevice{}}
	defer func() { cur = nil }()
	hk, _, e := pSetWindowsHookExW.Call(whKeyboardLL, hookCb, hInst, 0)
	if hk == 0 {
		fail(fmt.Errorf("Tastaturhaken: %w", e))
		return
	}
	defer pUnhookWindowsHookEx.Call(hk)
	defer pKillTimer.Call(hwnd, timerID)

	tid, _, _ := pGetCurrentThreadID.Call()
	h.tid = uint32(tid)
	ready <- nil

	var m winMsg
	for {
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
	// Noch gehaltene Tasten gehen nicht verloren: Sie werden wiederholt.
	if cur != nil {
		for _, a := range cur.g.Tick(time.Now().Add(holdMax)) {
			if a.Kind == actReplay {
				replay(a.Key)
			}
		}
	}
}

type keyIn struct {
	code uint16
	at   time.Time
}

// readKeys liefert die Kartentasten des Lesers. Die Auswahl wird bei jeder Taste neu gegen das Gerät
// geprüft; ein neu angesteckter Leser (neues Handle, gleiche Kennung) wird so ohne Neustart gefunden.
func (u *usbReader) readKeys(ctx context.Context, fn func(code uint16, at time.Time)) error {
	ch := make(chan keyIn, 256)
	unsub, err := hub.subscribe(&winSub{
		match: func(d InputDevice) bool { return deviceMatches(u.sel, d) },
		fn: func(_ InputDevice, code uint16, at time.Time) {
			select {
			case ch <- keyIn{code, at}: // der Hub-Thread darf nie blockieren
			default:
			}
		},
	})
	if err != nil {
		return fmt.Errorf("Leser kann nicht übernommen werden: %w", err)
	}
	defer unsub()
	for {
		select {
		case <-ctx.Done():
			return nil
		case k := <-ch:
			fn(k.code, k.at)
		}
	}
}

// usbMayBeAbsent: Unter Windows startet der Leser auch ohne angestecktes Gerät und findet es später
// von selbst (Auswahl per Kennung oder Name, nicht per Handle); unter Linux scheitert das Öffnen.
const usbMayBeAbsent = true

// learnPlatform: Alle Kartentasten werden bis zum ersten Enter von allen Tastaturen verschluckt und
// dem Anlernen zugeführt; das Gerät der Eingabe ist das gesuchte.
func learnPlatform(ctx context.Context, d time.Duration) (InputDevice, string, error) {
	type learnKey struct {
		dev  InputDevice
		code uint16
		at   time.Time
	}
	ch := make(chan learnKey, 256)
	unsub, err := hub.subscribe(&winSub{learn: true, fn: func(dev InputDevice, code uint16, at time.Time) {
		select {
		case ch <- learnKey{dev, code, at}:
		default:
		}
	}})
	if err != nil {
		return InputDevice{}, "", fmt.Errorf("Anlernen nicht möglich: %w", err)
	}
	defer unsub()
	timer := time.NewTimer(d)
	defer timer.Stop()
	var buf keyBuffer
	for {
		select {
		case <-ctx.Done():
			return InputDevice{}, "", ctx.Err()
		case <-timer.C:
			return InputDevice{}, "", errors.New("Keine Karte gelesen")
		case k := <-ch:
			if uid, done := buf.Key(k.code, k.at); done {
				return k.dev, strings.ToUpper(strings.TrimSpace(uid)), nil
			}
		}
	}
}
