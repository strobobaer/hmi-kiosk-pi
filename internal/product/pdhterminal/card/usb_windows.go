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
	"sync/atomic"
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

	pRegisterClassExW         = user32.NewProc("RegisterClassExW")
	pCreateWindowExW          = user32.NewProc("CreateWindowExW")
	pDestroyWindow            = user32.NewProc("DestroyWindow")
	pDefWindowProcW           = user32.NewProc("DefWindowProcW")
	pRegisterRawInputDevices  = user32.NewProc("RegisterRawInputDevices")
	pGetRawInputData          = user32.NewProc("GetRawInputData")
	pGetRawInputDeviceList    = user32.NewProc("GetRawInputDeviceList")
	pGetRawInputDeviceInfoW   = user32.NewProc("GetRawInputDeviceInfoW")
	pSetWindowsHookExW        = user32.NewProc("SetWindowsHookExW")
	pCallNextHookEx           = user32.NewProc("CallNextHookEx")
	pUnhookWindowsHookEx      = user32.NewProc("UnhookWindowsHookEx")
	pGetMessageW              = user32.NewProc("GetMessageW")
	pTranslateMessage         = user32.NewProc("TranslateMessage")
	pDispatchMessageW         = user32.NewProc("DispatchMessageW")
	pPostThreadMessageW       = user32.NewProc("PostThreadMessageW")
	pPostMessageW             = user32.NewProc("PostMessageW")
	pGetTickCount64           = kernel32.NewProc("GetTickCount64")
	pGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
	pGetWindowThreadProcessID = user32.NewProc("GetWindowThreadProcessId")
	pSetTimer                 = user32.NewProc("SetTimer")
	pKillTimer                = user32.NewProc("KillTimer")
	pSendInput                = user32.NewProc("SendInput")
	pGetCurrentThreadID       = kernel32.NewProc("GetCurrentThreadId")
	pGetModuleHandleW         = kernel32.NewProc("GetModuleHandleW")
	pHidGetProductString      = hidDLL.NewProc("HidD_GetProductString")
)

const (
	wmInput              = 0x00FF
	wmInputDeviceChange  = 0x00FE
	wmTimer              = 0x0113
	wmRefresh            = 0x0401 // WM_USER+1: Abnehmer oder Geraete haben sich geaendert
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

// deviceID ist die Kennung vvvv:pppp aus VID_xxxx&PID_xxxx im Pfad (klein), sonst leer.
func deviceID(path string) string {
	if m := vidPid.FindStringSubmatch(path); m != nil {
		return strings.ToLower(m[1] + ":" + m[2])
	}
	return ""
}

// deviceFromPath baut das Gerät zum Pfad mit Name aus dem Produktstring (CreateFile und HidD_*: darf nur
// außerhalb von Haken und Fensterprozedur laufen). named sagt, ob der Name wirklich gelesen wurde; sonst
// steht ein Ersatzname da, der nicht zwischengespeichert werden darf.
func deviceFromPath(path string) (d InputDevice, named bool) {
	d = InputDevice{Path: path, ID: deviceID(path)}
	d.Name = productString(path)
	if d.Name != "" {
		return d, true
	}
	d.Name = "Tastatur"
	if d.ID != "" {
		d.Name += " " + d.ID
	}
	return d, false
}

// enumDev ist ein Gerät der Liste; Named sagt, ob der Name wirklich gelesen wurde.
type enumDev struct {
	InputDevice
	Named bool
}

// enumDevices listet die Tastaturen (Raw Input) mit Namen (CreateFile und HidD_*: nie im Haken). Ein Leser
// mit mehreren Tastaturknoten erscheint einmal.
func enumDevices() []enumDev {
	var n uint32
	if r, _, _ := pGetRawInputDeviceList.Call(0, uintptr(unsafe.Pointer(&n)), unsafe.Sizeof(rawInputDeviceList{})); int32(r) < 0 || n == 0 {
		return nil
	}
	list := make([]rawInputDeviceList, n)
	r, _, _ := pGetRawInputDeviceList.Call(uintptr(unsafe.Pointer(&list[0])), uintptr(unsafe.Pointer(&n)), unsafe.Sizeof(rawInputDeviceList{}))
	if int32(r) <= 0 {
		return nil
	}
	var out []enumDev
	seen := map[string]bool{}
	for _, e := range list[:r] {
		if e.Type != rimTypeKeyboard {
			continue
		}
		path := rawDeviceName(e.Device)
		if path == "" {
			continue
		}
		d, named := deviceFromPath(path)
		key := d.ID + "|" + d.Name
		if d.ID != "" && seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, enumDev{d, named})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ListInputDevices listet die Tastaturen (Raw Input); dir wird ignoriert.
func ListInputDevices(dir string) []InputDevice {
	var out []InputDevice
	for _, d := range enumDevices() {
		out = append(out, d.InputDevice)
	}
	return out
}

// winSub ist ein Abnehmer. Leser (match) bekommen die verschluckten Tasten ihres Geräts; wer anlernt (learn),
// bekommt die erkannte Folge samt Gerät und löst kein Verschlucken aus.
type winSub struct {
	match  func(InputDevice) bool // Geräte, deren Kartentasten verschluckt werden
	fn     func(dev InputDevice, code uint16, at time.Time)
	learn  func(dev InputDevice, text string)
	warn   func(msg string)    // Meldung des Abfangens (leer: wieder in Ordnung)
	attach func(attached bool) // Leser angesteckt oder abgezogen (vom Auflöser, nie im Haken)
	att    attachTracker       // nur vom Auflöser benutzt
	lrn    learner             // nur vom Hub-Thread benutzt
}

// kbdHub besitzt den einen Thread je Prozess; er läuft, solange es Abnehmer gibt.
type kbdHub struct {
	startMu sync.Mutex // Start und Stopp
	mu      sync.Mutex // subs
	subs    []*winSub
	tid     uint32
	hwnd    uintptr
	done    chan struct{}

	// Auflösen der Geräte (CreateFile, HidD_*) läuft in einem eigenen Goroutine, nie im Haken: Ein Haken,
	// der LowLevelHooksTimeout überschreitet, wird von Windows stillschweigend entfernt.
	warnCh   chan string // Meldungen vom Hub-Thread an warner()
	req      chan struct{}
	attached atomic.Bool // gewählter Leser ist angesteckt (Ergebnis des Auflösers)
	nameMu   sync.Mutex
	names    map[string]string // Pfad -> gelesener Produktname (nur Erfolge)
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
	} else {
		h.request()
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
		if !last {
			h.request()
		}
		if last && h.done != nil {
			pPostThreadMessageW.Call(uintptr(h.tid), wmQuit, 0, 0)
			<-h.done
			h.done = nil
		}
	}, nil
}

// request fordert den Auflöser auf, die Geräteliste neu zu lesen (nicht blockierend, Anfragen verschmelzen).
func (h *kbdHub) request() {
	if h.req == nil {
		return
	}
	select {
	case h.req <- struct{}{}:
	default:
	}
}

// name ist der gelesene Produktname zum Pfad, leer wenn (noch) nicht bekannt.
func (h *kbdHub) name(path string) string {
	h.nameMu.Lock()
	defer h.nameMu.Unlock()
	return h.names[path]
}

// resolve läuft im Auflöser: liest die Geräteliste samt Namen, merkt sich nur gelungene Namen und
// bestimmt, ob abgefangen wird; meldet das Ergebnis per wmRefresh an den Hub-Thread.
func (h *kbdHub) resolve() bool {
	var readers []*winSub
	learning := false
	for _, s := range h.snapshot() {
		if s.learn != nil {
			learning = true
		}
		if s.match != nil {
			readers = append(readers, s)
		}
	}
	names := map[string]string{}
	attached := false
	here := make(map[*winSub]bool, len(readers))
	for _, d := range enumDevices() {
		if d.Named {
			names[d.Path] = d.Name
		}
		for _, s := range readers {
			if s.match(d.InputDevice) {
				attached = true
				here[s] = true
			}
		}
	}
	// Jeder Leser erfährt, ob sein Gerät da ist (Anstecken und Abziehen ohne Neustart).
	for _, s := range readers {
		if s.att.set(here[s]) && s.attach != nil {
			s.attach(here[s])
		}
	}
	h.nameMu.Lock()
	h.names = names
	h.nameMu.Unlock()
	return (attached || forceActive && len(readers) > 0) && !learning
}

// resolver ist der Goroutine, der Anfragen abarbeitet, bis stop geschlossen wird.
func (h *kbdHub) resolver(hwnd uintptr, stop <-chan struct{}, finished chan<- struct{}) {
	defer close(finished)
	for {
		select {
		case <-h.req:
		case <-stop:
			return
		}
		h.attached.Store(h.resolve())
		pPostMessageW.Call(hwnd, wmRefresh, 0, 0)
	}
}

// dispatch reicht eine verschluckte Taste an die Leser; ist das Gerät unbekannt (Zeitmuster), an alle.
func (h *kbdHub) dispatch(dev InputDevice, known bool, code uint16, at time.Time) {
	for _, s := range h.snapshot() {
		if s.match != nil && (!known || s.match(dev)) {
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
		if s.match != nil && s.match(info) {
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

	fg        uintptr // zuletzt gesehenes Vordergrundfenster
	warned    bool
	lastAsked time.Time
}

var cur *loopState

// injectedAsHardware lässt die Prüfung eingespielte Tasten wie Tasten einer Tastatur behandeln, damit
// sich der Weg „halten und wiederholen“ ohne zweite Tastatur ausführen lässt. Nur Tests setzen es.
var injectedAsHardware bool

// forceActive lässt die Prüfung das Abfangen auch ohne angestecktes Lesergerät einschalten.
var forceActive bool

// device liefert das Gerät zum Raw-Input-Handle ohne jede Geräte-E/A: Pfad und Kennung stammen aus
// GetRawInputDeviceInfo, der Name aus dem Speicher des Auflösers. Fehlt er, wird er (höchstens alle 2 s)
// angefordert; der Eintrag bleibt bis zur nächsten Aktualisierung unvollständig und wird nicht festgeschrieben.
func (l *loopState) device(h uintptr) InputDevice {
	if d, ok := l.cache[h]; ok {
		return d
	}
	path := rawDeviceName(h)
	d := InputDevice{Path: path, ID: deviceID(path), Name: l.hub.name(path)}
	if d.Name == "" {
		if time.Since(l.lastAsked) > 2*time.Second {
			l.lastAsked = time.Now()
			l.hub.request()
		}
	}
	l.cache[h] = d
	return d
}

// refresh übernimmt das Ergebnis des Auflösers.
func (l *loopState) refresh() {
	l.cache = map[uintptr]InputDevice{}
	l.apply(l.g.SetActive(l.hub.attached.Load()))
}

// apply führt die Folgen einer Entscheidung aus. Scheitert das Wiederholen (SendInput wird von UIPI
// abgewiesen, wenn das Vordergrundfenster höhere Rechte hat), wird nichts mehr gehalten, bis das
// Vordergrundfenster wechselt, und es wird gemeldet.
func (l *loopState) apply(acts []action) {
	lost := l.g.Exec(acts, replay, func(a action) {
		if code, ok := keyCode(a.Key); ok && !a.Key.Up {
			var d InputDevice
			if a.Dev != 0 {
				d = l.device(a.Dev)
			}
			l.hub.dispatch(d, a.Dev != 0, code, a.At)
		}
	})
	if lost > 0 {
		l.warned = true
		l.hub.warnAll(fmt.Sprintf("USB-Leser: %d Tasten konnten nicht an das Vordergrundfenster weitergegeben werden (Fenster mit höheren Rechten?). Dort wird der Leser nicht abgefangen.", lost))
	}
	l.rearm()
}

// checkForeground prüft bei einem Wechsel des Vordergrundfensters, ob dorthin eingespielt werden darf;
// sonst wird nichts gehalten (Bypass). Ein gemeldeter Fehler wird mit dem Wechsel zurückgenommen.
func (l *loopState) checkForeground() {
	if !l.g.Idle() {
		return
	}
	fg, _, _ := pGetForegroundWindow.Call()
	if fg == l.fg {
		return
	}
	l.fg = fg
	l.g.Bypass = !canInject(fg)
	if !l.g.Bypass && l.warned {
		l.warned = false
		l.hub.warnAll("")
	}
}

var (
	selfElevatedOnce sync.Once
	selfElevated     bool
)

// canInject: Darf SendInput in das Fenster schreiben? UIPI weist Eingaben an Fenster höherer Rechte ab. Sicher
// ist, was gelesen werden kann und nicht erhöht ist; im Zweifel nein.
func canInject(hwnd uintptr) bool {
	if hwnd == 0 {
		return true
	}
	selfElevatedOnce.Do(func() { selfElevated = windows.GetCurrentProcessToken().IsElevated() })
	if selfElevated {
		return true
	}
	var pid uint32
	pGetWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return true
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var tok windows.Token
	if err := windows.OpenProcessToken(h, windows.TOKEN_QUERY, &tok); err != nil {
		return false
	}
	defer tok.Close()
	return !tok.IsElevated()
}

// warnAll reicht eine Meldung an alle Leser weiter.
//
// Läuft im Haken oder in der Fensterprozedur und darf darum weder Sperren nehmen noch schreiben: Die Meldung
// geht nicht blockierend in einen Puffer (voll: verworfen) und wird von warner() ausgeliefert.
func (h *kbdHub) warnAll(msg string) {
	select {
	case h.warnCh <- msg:
	default:
	}
}

// warner liefert die Meldungen des Abfangens aus (Dienststatus, Protokoll), außerhalb des Hub-Threads.
func (h *kbdHub) warner(stop <-chan struct{}, finished chan<- struct{}) {
	defer close(finished)
	for {
		select {
		case msg := <-h.warnCh:
			for _, s := range h.snapshot() {
				if s.warn != nil {
					s.warn(msg)
				}
			}
		case <-stop:
			return
		}
	}
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
func replay(k keyEvent) bool {
	fl := uint32(keyeventfScanCode)
	if k.Up {
		fl |= keyeventfKeyUp
	}
	if k.Ext {
		fl |= keyeventfExtendedKey
	}
	in := kbdInput{Type: inputKeyboard, Scan: k.Scan, Flags: fl, Extra: replayMarker}
	r, _, _ := pSendInput.Call(1, uintptr(unsafe.Pointer(&in)), unsafe.Sizeof(in))
	return r == 1
}

// hookTime ist der Zeitpunkt des Tastendrucks aus dem Zeitstempel des Hakens (Millisekunden der
// Systemuhr, 32 Bit), damit Verzögerungen des Hub-Threads die Abstände nicht verfälschen.
func hookTime(ms uint32) time.Time {
	now, _, _ := pGetTickCount64.Call()
	return time.Now().Add(-time.Duration(uint32(now)-ms) * time.Millisecond)
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
		if !h.Marked && !h.Injected && isCandidate(h.keyEvent) {
			cur.checkForeground()
		}
		block, acts := cur.g.Hook(h, hookTime(k.Time))
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
			l.hub.request()
		case wmRefresh:
			l.refresh()
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
		info := l.device(re.Dev)
		re.Reader = l.hub.classify(info, re.Dev)
		for _, s := range l.hub.snapshot() {
			if s.learn != nil {
				if _, text, done := s.lrn.Key(re.Dev, k, re.At); done {
					s.learn(info, text)
				}
			}
		}
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
	h.hwnd = hwnd
	h.req = make(chan struct{}, 1)
	h.warnCh = make(chan string, 8)
	stopWarn := make(chan struct{})
	warnDone := make(chan struct{})
	go h.warner(stopWarn, warnDone)
	defer func() { close(stopWarn); <-warnDone }()
	h.attached.Store(false)
	stopRes := make(chan struct{})
	resDone := make(chan struct{})
	go h.resolver(hwnd, stopRes, resDone)
	defer func() { close(stopRes); <-resDone }()
	h.request()
	defer func() { h.hwnd, cur = 0, nil }()
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
		for _, a := range cur.g.SetActive(false) {
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
		match:  func(d InputDevice) bool { return deviceMatches(u.sel, d) },
		warn:   u.warn,
		attach: u.attach,
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

// learnPlatform: Nichts wird verschluckt. Die Folge (Abstand höchstens gapMax, Länge einer UID, Enter) wird
// an den Raw-Ereignissen erkannt, das Gerät dieser Ereignisse ist das gesuchte.
func learnPlatform(ctx context.Context, d time.Duration) (InputDevice, string, error) {
	type found struct {
		dev  InputDevice
		text string
	}
	ch := make(chan found, 1)
	unsub, err := hub.subscribe(&winSub{learn: func(dev InputDevice, text string) {
		select {
		case ch <- found{dev, text}:
		default:
		}
	}})
	if err != nil {
		return InputDevice{}, "", fmt.Errorf("Anlernen nicht möglich: %w", err)
	}
	defer unsub()
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return InputDevice{}, "", ctx.Err()
	case <-timer.C:
		return InputDevice{}, "", errors.New("Keine Karte gelesen")
	case f := <-ch:
		dev, _ := deviceFromPath(f.dev.Path) // Name lesen, hier ist es erlaubt (nicht im Haken)
		if dev.Path == "" {
			dev = f.dev
		}
		return dev, f.text, nil
	}
}
