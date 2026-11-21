//go:build windows

package notify

import (
	"context"
	"encoding/base64"
	"log"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Benachrichtigung unter Windows.
//
// Toast (A): Windows-Runtime-Toast über AppUserModelID PDH.Terminal. Ohne CGO und ohne COM-Bindung
// startet das Programm powershell.exe mit dem festen Skript toast.ps1 aus dem Programmordner (-File, kein
// -EncodedCommand, kein Bypass); der Text kommt Base64-kodiert über stdin und landet nur als InnerText im XML.
// Fehlt das Skript, gibt es keine Mitteilung (Protokoll). Die Mitteilung erscheint nur, wenn der Startmenü-Eintrag
// mit dieser Kennung besteht (legt das Setup an).
//
// Überlagerung (B) und Banner (C): eigene Fenster (GDI), die auf einem eigenen Thread mit Nachrichtenschleife
// leben. Der Thread entsteht beim ersten Gebrauch; Show reiht nur ein und weckt ihn per PostMessage, es blockiert
// nie. Fensterklasse und Fensterprozedur gibt es genau einmal je Prozess (syscall.NewCallback gibt nie frei).

const (
	appID        = "PDH.Terminal"
	showDuration = 4 * time.Second // B und C schließen nach dieser Zeit
	bannerHeight = 48
	overlayW     = 520
	overlayH     = 120
	toastTimeout = 20 * time.Second
	clockLineH   = 34 // Höhe der Zeile mit der Uhrzeit in der Überlagerung
	maxQueue     = 8  // so viele offene Wünsche an den Fensterthread; weitere fallen weg
	hintW        = 760
	hintH        = 190
	kindHint     = "hint"     // dauerhafter Hinweis (Edge fehlt): ohne Zeitablauf, nicht per Klick schließbar
	kindHintOff  = "hint-off" // Wunsch: den Hinweis schließen
)

const (
	wmDestroy       = 0x0002
	wmPaint         = 0x000F
	wmEraseBkgnd    = 0x0014
	wmMouseActivate = 0x0021
	wmTimer         = 0x0113
	wmLButtonDown   = 0x0201
	wmApp           = 0x8000

	maNoActivate = 3

	wsPopup         = 0x80000000
	wsExTopmost     = 0x00000008
	wsExToolWindow  = 0x00000080
	wsExNoActivate  = 0x08000000
	swShowNoActive  = 4
	smCXScreen      = 0
	smCYScreen      = 1
	swpNoActivate   = 0x0010
	hwndTopmost     = ^uintptr(0) // (HWND)-1
	hwndMessage     = ^uintptr(2) // (HWND)-3
	dtCenter        = 0x1
	dtVCenter       = 0x4
	dtSingleLine    = 0x20
	dtEndEllipsis   = 0x8000
	dtNoPrefix      = 0x800
	transparentMode = 1
	fwSemiBold      = 600
	timerClose      = 1
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	pRegisterClassExW = user32.NewProc("RegisterClassExW")
	pCreateWindowExW  = user32.NewProc("CreateWindowExW")
	pDestroyWindow    = user32.NewProc("DestroyWindow")
	pDefWindowProcW   = user32.NewProc("DefWindowProcW")
	pGetMessageW      = user32.NewProc("GetMessageW")
	pTranslateMessage = user32.NewProc("TranslateMessage")
	pDispatchMessageW = user32.NewProc("DispatchMessageW")
	pPostMessageW     = user32.NewProc("PostMessageW")
	pShowWindow       = user32.NewProc("ShowWindow")
	pSetTimer         = user32.NewProc("SetTimer")
	pKillTimer        = user32.NewProc("KillTimer")
	pBeginPaint       = user32.NewProc("BeginPaint")
	pEndPaint         = user32.NewProc("EndPaint")
	pFillRect         = user32.NewProc("FillRect")
	pDrawTextW        = user32.NewProc("DrawTextW")
	pGetClientRect    = user32.NewProc("GetClientRect")
	pGetSystemMetrics = user32.NewProc("GetSystemMetrics")
	pSetWindowPos     = user32.NewProc("SetWindowPos")
	pLoadCursorW      = user32.NewProc("LoadCursorW")
	pGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
	pCreateSolidBrush = gdi32.NewProc("CreateSolidBrush")
	pCreateFontW      = gdi32.NewProc("CreateFontW")
	pDeleteObject     = gdi32.NewProc("DeleteObject")
	pSelectObject     = gdi32.NewProc("SelectObject")
	pSetBkMode        = gdi32.NewProc("SetBkMode")
	pSetTextColor     = gdi32.NewProc("SetTextColor")
)

type wndClassEx struct {
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

type winMsg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      [2]int32
}

type rect struct{ Left, Top, Right, Bottom int32 }

type paintStruct struct {
	HDC       uintptr
	Erase     int32
	Paint     rect
	Restore   int32
	IncUpdate int32
	Reserved  [32]byte
}

// popup ist ein offenes Fenster B oder C; nur der Fensterthread liest und schreibt popups.
type popup struct {
	text  string
	tone  string
	kind  string
	clock string // Uhrzeit, nur in der Überlagerung
}

// request ist ein Wunsch an den Fensterthread.
type request struct{ kind, text, tone, detail string }

// Rückruf und Zustand des Fensterthreads: genau einmal je Prozess.
var (
	wndProcCb = syscall.NewCallback(wndProc)

	uiOnce  sync.Once
	uiReady = make(chan struct{})
	uiErr   error
	uiHwnd  uintptr // Nachrichtenfenster; gültig nach uiReady ohne uiErr

	queueMu sync.Mutex
	queue   []request

	popups  = map[uintptr]*popup{} // nur im Fensterthread
	current = map[string]uintptr{} // je Art das offene Fenster; nur im Fensterthread
)

// Programmweit angelegt wie in der Rückrufgruppe: die Klasse mit Namen der Fensterprozedur.
var className = windows.StringToUTF16Ptr("PDHTerminalNotify")

// shower ist die Windows-Anzeige; raise holt das Edge-Fenster nach vorn.
type shower struct{ raise func() error }

// New liefert die Anzeige unter Windows. raise (darf nil sein) holt das Browser-Fenster nach vorn.
func New(raise func() error) Shower { return &shower{raise: raise} }

func (s *shower) Show(kinds []string, text, tone string) {
	if slices.Contains(kinds, KindToast) {
		go toast(text)
	}
	if slices.Contains(kinds, KindFront) && s.raise != nil {
		// Windows darf das verweigern (das Fenster blinkt dann nur): bestenfalls ein Protokolleintrag.
		go func() {
			if err := s.raise(); err != nil {
				log.Printf("Fenster nach vorn: %v", err)
			}
		}()
	}
	for _, k := range []string{KindOverlay, KindBanner} {
		if slices.Contains(kinds, k) {
			enqueue(request{kind: k, text: text, tone: tone})
		}
	}
}

// ShowHint zeigt einen dauerhaften Hinweis in der Mitte des Bildschirms (immer oben, ohne den Fokus zu nehmen), bis
// HideHint ihn schließt. Er nutzt dieselbe Fensterklasse und denselben Thread wie die Benachrichtigungen. Ein neuer
// Aufruf ersetzt den offenen Hinweis.
func ShowHint(title, detail string) { enqueue(request{kindHint, title, ToneError, detail}) }

// HideHint schließt den Hinweis (ohne Wirkung, wenn keiner offen ist).
func HideHint() { enqueue(request{kind: kindHintOff}) }

// toast startet das feste Skript toast.ps1; der Text geht Base64-kodiert über stdin.
func toast(text string) {
	exe, _ := os.Executable()
	script := ToastScriptPath(os.Getenv, exe, func(p string) bool { fi, err := os.Stat(p); return err == nil && !fi.IsDir() })
	if script == "" {
		log.Printf("Toast nicht angezeigt: %s nicht im Programmordner gefunden", ToastScriptName)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), toastTimeout)
	defer cancel()
	// RemoteSigned genügt für die lokale, nicht heruntergeladene Datei; Bypass braucht es nicht.
	cmd := exec.CommandContext(ctx, "powershell.exe", toastArgs(script)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	cmd.Stdin = strings.NewReader(base64.StdEncoding.EncodeToString([]byte(text)) + "\r\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		log.Printf("Toast nicht angezeigt: %v %s", err, msg)
	}
}

// toastArgs sind die Argumente für powershell.exe: das Skript als Datei, ohne Profil und ohne Rückfragen.
func toastArgs(script string) []string {
	return []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "RemoteSigned", "-File", script}
}

// enqueue reiht den Wunsch ein und weckt den Fensterthread; es blockiert nie.
func enqueue(r request) {
	uiOnce.Do(func() { go uiThread() })
	select {
	case <-uiReady:
	case <-time.After(2 * time.Second):
		log.Print("Benachrichtigungsfenster: Thread nicht bereit")
		return
	}
	if uiErr != nil {
		return
	}
	queueMu.Lock()
	if len(queue) >= maxQueue {
		queueMu.Unlock()
		return
	}
	queue = append(queue, r)
	queueMu.Unlock()
	pPostMessageW.Call(uiHwnd, wmApp, 0, 0)
}

func uiThread() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	fail := func(err error) {
		uiErr = err
		close(uiReady)
		log.Printf("Benachrichtigungsfenster: %v", err)
	}
	hInst, _, _ := pGetModuleHandleW.Call(0)
	cur, _, _ := pLoadCursorW.Call(0, 32512) // IDC_ARROW
	wc := wndClassEx{
		WndProc:   wndProcCb,
		Instance:  hInst,
		Cursor:    cur,
		ClassName: className,
	}
	wc.Size = uint32(unsafe.Sizeof(wc))
	if a, _, e := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); a == 0 {
		fail(e)
		return
	}
	hwnd, _, e := pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(className)), 0, 0, 0, 0, 0, 0, hwndMessage, 0, hInst, 0)
	if hwnd == 0 {
		fail(e)
		return
	}
	uiHwnd = hwnd
	close(uiReady)

	var m winMsg
	for {
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// wndProc läuft im Fensterthread und blockiert nie (keine Protokollausgabe, keine langen Sperren).
func wndProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	switch msg {
	case wmApp:
		drain()
		return 0
	case wmMouseActivate:
		return maNoActivate
	case wmEraseBkgnd:
		return 1
	case wmPaint:
		if p := popups[hwnd]; p != nil {
			paint(hwnd, p)
			return 0
		}
	case wmLButtonDown:
		if p := popups[hwnd]; p != nil && p.kind != kindHint { // der Hinweis bleibt
			pDestroyWindow.Call(hwnd)
			return 0
		}
	case wmTimer:
		if p := popups[hwnd]; p != nil && p.kind != kindHint {
			pDestroyWindow.Call(hwnd)
			return 0
		}
	case wmDestroy:
		if p := popups[hwnd]; p != nil {
			pKillTimer.Call(hwnd, timerClose)
			delete(popups, hwnd)
			if current[p.kind] == hwnd {
				delete(current, p.kind)
			}
		}
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, msg, wparam, lparam)
	return r
}

func drain() {
	queueMu.Lock()
	reqs := queue
	queue = nil
	queueMu.Unlock()
	for _, r := range reqs {
		open(r)
	}
}

// open öffnet das Fenster der Art; ein noch offenes derselben Art wird ersetzt.
func open(r request) {
	if r.kind == kindHintOff {
		if old := current[kindHint]; old != 0 {
			pDestroyWindow.Call(old)
		}
		return
	}
	if old := current[r.kind]; old != 0 {
		pDestroyWindow.Call(old)
	}
	sw, _, _ := pGetSystemMetrics.Call(smCXScreen)
	sh, _, _ := pGetSystemMetrics.Call(smCYScreen)
	w, h, x, y := int32(overlayW), int32(overlayH), (int32(sw)-overlayW)/2, (int32(sh)-overlayH)/2
	if r.kind == KindBanner {
		w, h, x, y = int32(sw), bannerHeight, 0, 0
	}
	if r.kind == kindHint {
		w, h, x, y = int32(hintW), int32(hintH), (int32(sw)-hintW)/2, (int32(sh)-hintH)/2
	}
	hInst, _, _ := pGetModuleHandleW.Call(0)
	hwnd, _, _ := pCreateWindowExW.Call(wsExTopmost|wsExToolWindow|wsExNoActivate,
		uintptr(unsafe.Pointer(className)), 0, wsPopup, uintptr(x), uintptr(y), uintptr(w), uintptr(h), 0, 0, hInst, 0)
	if hwnd == 0 {
		return
	}
	p := &popup{text: r.text, tone: r.tone, kind: r.kind}
	if r.kind == KindOverlay {
		p.clock = ClockText(time.Now())
	}
	if r.kind == kindHint {
		p.clock = r.detail // zweite, kleinere Zeile
	}
	popups[hwnd] = p
	current[r.kind] = hwnd
	if r.kind != kindHint {
		pSetTimer.Call(hwnd, timerClose, uintptr(showDuration/time.Millisecond), 0)
	}
	pSetWindowPos.Call(hwnd, hwndTopmost, uintptr(x), uintptr(y), uintptr(w), uintptr(h), swpNoActivate)
	pShowWindow.Call(hwnd, swShowNoActive)
}

// toneColor ist der Hintergrund als COLORREF (0x00BBGGRR).
func toneColor(tone string) uintptr {
	switch tone {
	case ToneOK:
		return 0x27 | 0xae<<8 | 0x60<<16 // #27ae60
	case ToneError:
		return 0xc0 | 0x39<<8 | 0x2b<<16 // #c0392b
	default:
		return 0x7f | 0x8c<<8 | 0x8d<<16 // #7f8c8d
	}
}

func paint(hwnd uintptr, p *popup) {
	var ps paintStruct
	hdc, _, _ := pBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
	if hdc == 0 {
		return
	}
	defer pEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
	var rc rect
	pGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&rc)))
	br, _, _ := pCreateSolidBrush.Call(toneColor(p.tone))
	pFillRect.Call(hdc, uintptr(unsafe.Pointer(&rc)), br)
	pDeleteObject.Call(br)

	height := 22
	if p.kind == KindOverlay || p.kind == kindHint {
		height = 28
	}
	neg := ^uintptr(height - 1) // -height: Zeichenhöhe in Pixeln
	font, _, _ := pCreateFontW.Call(neg, 0, 0, 0, fwSemiBold, 0, 0, 0, 1 /* DEFAULT_CHARSET */, 0, 0, 5 /* CLEARTYPE */, 0,
		uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("Segoe UI"))))
	old, _, _ := pSelectObject.Call(hdc, font)
	pSetBkMode.Call(hdc, transparentMode)
	pSetTextColor.Call(hdc, 0xFFFFFF)
	rc.Left += 16
	rc.Right -= 16
	drawLine := func(text string, r *rect) {
		txt := utf16.Encode([]rune(text))
		if len(txt) > 0 {
			pDrawTextW.Call(hdc, uintptr(unsafe.Pointer(&txt[0])), uintptr(len(txt)), uintptr(unsafe.Pointer(r)),
				dtCenter|dtVCenter|dtSingleLine|dtEndEllipsis|dtNoPrefix)
		}
	}
	if p.clock == "" {
		drawLine(p.text, &rc)
		pSelectObject.Call(hdc, old)
		pDeleteObject.Call(font)
		return
	}
	// Überlagerung: oben Name und Ergebnis, unten die Uhrzeit in kleinerer Schrift.
	clockRc := rc
	rc.Bottom -= clockLineH
	clockRc.Top = rc.Bottom
	drawLine(p.text, &rc)
	pSelectObject.Call(hdc, old)
	pDeleteObject.Call(font)
	small, _, _ := pCreateFontW.Call(^uintptr(17), 0, 0, 0, 400, 0, 0, 0, 1, 0, 0, 5, 0,
		uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("Segoe UI"))))
	old, _, _ = pSelectObject.Call(hdc, small)
	drawLine(p.clock, &clockRc)
	pSelectObject.Call(hdc, old)
	pDeleteObject.Call(small)
}
