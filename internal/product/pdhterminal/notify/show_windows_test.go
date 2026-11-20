//go:build windows

package notify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
