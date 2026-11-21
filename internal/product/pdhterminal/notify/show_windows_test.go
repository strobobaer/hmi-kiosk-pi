//go:build windows

package notify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"
)

func TestToneColor(t *testing.T) {
	// COLORREF ist 0x00BBGGRR.
	for tone, want := range map[string]uintptr{
		ToneOK:      0x60ae27,
		ToneError:   0x2b39c0,
		ToneNeutral: 0x8d8c7f,
		"":          0x8d8c7f,
	} {
		if got := toneColor(tone); got != want {
			t.Errorf("toneColor(%q) = %#x, want %#x", tone, got, want)
		}
	}
}

func TestToastArgs(t *testing.T) {
	got := strings.Join(toastArgs(`C:\Program Files\PDH-Terminal\toast.ps1`), " ")
	for _, bad := range []string{"EncodedCommand", "Bypass", "-Command"} {
		if strings.Contains(got, bad) {
			t.Errorf("Argumente enthalten %s: %s", bad, got)
		}
	}
	for _, want := range []string{"-NoProfile", "-NonInteractive", "-File", "RemoteSigned", "toast.ps1"} {
		if !strings.Contains(got, want) {
			t.Errorf("Argumente ohne %s: %s", want, got)
		}
	}
}

// Das ausgelieferte Skript ist fest: kein Platzhalter, der Text kommt nur über stdin.
func TestToastScriptFile(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "packaging", "windows", ToastScriptName))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"Terminal öffnen", "'PDH.Terminal'", "[Console]::In.ReadLine()", "InnerText"} {
		if !strings.Contains(s, want) {
			t.Errorf("Skript ohne %q", want)
		}
	}
	if strings.Contains(s, "%s") || strings.Contains(s, "{0}") || strings.Contains(s, "Invoke-Expression") {
		t.Error("Skript darf keine Platzhalter oder Ausführung von Text haben")
	}
	if !strings.HasPrefix(s, "\xef\xbb\xbf") {
		t.Error("Skript braucht die UTF-8-Marke, sonst liest Windows PowerShell 5.1 das ö falsch")
	}
}

// Der Hinweis (Edge fehlt) ist dauerhaft: ein Klick schließt ihn nicht, erst HideHint. Ein echtes Fenster, kurz sichtbar.
func TestHintWindowPersistsUntilHidden(t *testing.T) {
	pFindWindowEx := user32.NewProc("FindWindowExW")
	pIsVisible := user32.NewProc("IsWindowVisible")
	// Das (unsichtbare) Nachrichtenfenster hat dieselbe Klasse: gesucht wird das sichtbare Fenster.
	find := func() uintptr {
		var prev uintptr
		for {
			h, _, _ := pFindWindowEx.Call(0, prev, uintptr(unsafe.Pointer(className)), 0)
			if h == 0 {
				return 0
			}
			if v, _, _ := pIsVisible.Call(h); v != 0 {
				return h
			}
			prev = h
		}
	}
	waitFor := func(want bool) uintptr {
		for i := 0; i < 100; i++ {
			if h := find(); (h != 0) == want {
				return h
			}
			time.Sleep(20 * time.Millisecond)
		}
		return find()
	}
	ShowHint(HintEdgeTitle, HintEdgeDetail)
	h := waitFor(true)
	if h == 0 {
		t.Fatal("Hinweisfenster erscheint nicht")
	}
	defer HideHint()
	pPostMessageW.Call(h, wmLButtonDown, 0, 0) // ein Klick darf ihn nicht schließen
	time.Sleep(300 * time.Millisecond)
	if find() == 0 {
		t.Fatal("Klick schloss den Hinweis")
	}
	HideHint()
	if waitFor(false) != 0 {
		t.Error("HideHint schloss das Fenster nicht")
	}
}
