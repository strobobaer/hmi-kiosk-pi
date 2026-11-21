//go:build windows

package browser

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// ReadRegistry liest den Standardwert eines Schlüssels der Form HKLM\Pfad (64-Bit-Sicht).
func ReadRegistry(key string) (string, error) {
	root, path, ok := strings.Cut(key, `\`)
	if !ok {
		return "", fmt.Errorf("Registry-Schlüssel %q", key)
	}
	var base registry.Key
	switch root {
	case "HKLM":
		base = registry.LOCAL_MACHINE
	case "HKCU":
		base = registry.CURRENT_USER
	default:
		return "", fmt.Errorf("Registry-Zweig %q", root)
	}
	k, err := registry.OpenKey(base, path, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return "", err
	}
	defer k.Close()
	v, _, err := k.GetStringValue("")
	return v, err
}

var (
	user32               = windows.NewLazySystemDLL("user32.dll")
	pEnumWindows         = user32.NewProc("EnumWindows")
	pGetClassNameW       = user32.NewProc("GetClassNameW")
	pGetWindowThreadPID  = user32.NewProc("GetWindowThreadProcessId")
	pIsWindowVisible     = user32.NewProc("IsWindowVisible")
	pIsIconic            = user32.NewProc("IsIconic")
	pGetWindow           = user32.NewProc("GetWindow")
	pGetWindowLongW      = user32.NewProc("GetWindowLongW")
	pGetForegroundWindow = user32.NewProc("GetForegroundWindow")
	pShowWindow          = user32.NewProc("ShowWindow")
	pSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	pAllowSetForeground  = user32.NewProc("AllowSetForegroundWindow")
	pFlashWindowEx       = user32.NewProc("FlashWindowEx")
)

const (
	edgeWindowClass = "Chrome_WidgetWin_1"
	gwOwner         = 4
	gwlExStyle      = -20
	wsExToolWindow  = 0x80
	swRestore       = 9
	swShowMinNoActv = 7
	flashwAll       = 3
	flashwTimerNoFg = 0xC
)

// EdgeWindow findet das App-Fenster von Edge: das sichtbare Fenster der Klasse
// Chrome_WidgetWin_1 eines Prozesses im Job-Objekt. pids liefert die Prozesse des
// laufenden Jobs (client-Prozess ProcessIDs); ohne Job gibt es kein Fenster.
type EdgeWindow struct {
	pids func() ([]uint32, error)
}

func NewEdgeWindow(pids func() ([]uint32, error)) *EdgeWindow { return &EdgeWindow{pids: pids} }

func (w *EdgeWindow) pidSet() map[uint32]bool {
	if w.pids == nil {
		return nil
	}
	ids, err := w.pids()
	if err != nil || len(ids) == 0 {
		return nil
	}
	m := make(map[uint32]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}

func windowPID(h uintptr) uint32 {
	var pid uint32
	pGetWindowThreadPID.Call(h, uintptr(unsafe.Pointer(&pid)))
	return pid
}

// enumState ist der Zustand der laufenden Aufzählung. Der Rückruf entsteht nur einmal je
// Prozess (syscall.NewCallback gibt nie frei, die Laufzeit erlaubt rund 2000), EnumWindows
// arbeitet synchron: enumMu schützt den Zustand.
var (
	enumMu   sync.Mutex
	enumSet  map[uint32]bool
	enumHit  uintptr
	enumOnce sync.Once
	enumCB   uintptr
)

func enumProc(h uintptr, _ uintptr) uintptr {
	if !enumSet[windowPID(h)] {
		return 1
	}
	var name [64]uint16
	n, _, _ := pGetClassNameW.Call(h, uintptr(unsafe.Pointer(&name[0])), uintptr(len(name)))
	if windows.UTF16ToString(name[:n]) != edgeWindowClass {
		return 1
	}
	if v, _, _ := pIsWindowVisible.Call(h); v == 0 {
		return 1
	}
	if o, _, _ := pGetWindow.Call(h, gwOwner); o != 0 {
		return 1
	}
	idx := int32(gwlExStyle)
	ex, _, _ := pGetWindowLongW.Call(h, uintptr(idx))
	if ex&wsExToolWindow != 0 {
		return 1
	}
	enumHit = h
	return 0
}

// find: erstes sichtbares Fenster ohne Besitzer (kein Tooltip, kein Menü) der Job-Prozesse.
func (w *EdgeWindow) find() (uintptr, bool) {
	set := w.pidSet()
	if set == nil {
		return 0, false
	}
	enumOnce.Do(func() { enumCB = syscall.NewCallback(enumProc) })
	enumMu.Lock()
	defer enumMu.Unlock()
	enumSet, enumHit = set, 0
	pEnumWindows.Call(enumCB, 0)
	found := enumHit
	enumSet = nil
	return found, found != 0
}

// Exists: Das Edge-Fenster ist da.
func (w *EdgeWindow) Exists() bool { _, ok := w.find(); return ok }

// Foreground: Das Vordergrundfenster gehört zu einem Prozess des Jobs.
func (w *EdgeWindow) Foreground() bool {
	set := w.pidSet()
	if set == nil {
		return false
	}
	h, _, _ := pGetForegroundWindow.Call()
	return h != 0 && set[windowPID(h)]
}

var errNoWindow = errors.New("kein Edge-Fenster")

// Raise stellt das Fenster wieder her und holt es nach vorn. Windows erlaubt
// SetForegroundWindow nur dem Vordergrundprozess (oder nach AllowSetForegroundWindow);
// gelingt es nicht, blinkt das Fenster in der Taskleiste (FlashWindowEx) und Raise
// meldet das als Fehler.
func (w *EdgeWindow) Raise() error {
	h, ok := w.find()
	if !ok {
		return errNoWindow
	}
	if ic, _, _ := pIsIconic.Call(h); ic != 0 {
		pShowWindow.Call(h, swRestore)
	}
	pAllowSetForeground.Call(^uintptr(0)) // ASFW_ANY
	if r, _, _ := pSetForegroundWindow.Call(h); r != 0 {
		return nil
	}
	fi := struct {
		Size    uint32
		Hwnd    uintptr
		Flags   uint32
		Count   uint32
		Timeout uint32
	}{Hwnd: h, Flags: flashwAll | flashwTimerNoFg, Count: 3}
	fi.Size = uint32(unsafe.Sizeof(fi))
	pFlashWindowEx.Call(uintptr(unsafe.Pointer(&fi)))
	return errors.New("Fenster nicht in den Vordergrund gekommen, es blinkt in der Taskleiste")
}

// MinimizeNoActivate minimiert das Fenster, ohne es zu aktivieren (SW_SHOWMINNOACTIVE).
func (w *EdgeWindow) MinimizeNoActivate() error {
	h, ok := w.find()
	if !ok {
		return errNoWindow
	}
	pShowWindow.Call(h, swShowMinNoActv)
	return nil
}
