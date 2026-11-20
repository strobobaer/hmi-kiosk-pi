//go:build windows

package notify

import (
	"encoding/base64"
	"strings"
	"testing"
	"unicode/utf16"
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

func TestEncodeScript(t *testing.T) {
	raw, err := base64.StdEncoding.DecodeString(encodeScript(toastScript))
	if err != nil || len(raw)%2 != 0 {
		t.Fatalf("Base64: %v", err)
	}
	u := make([]uint16, len(raw)/2)
	for i := range u {
		u[i] = uint16(raw[2*i]) | uint16(raw[2*i+1])<<8
	}
	got := string(utf16.Decode(u))
	if got != toastScript || !strings.Contains(got, "Terminal öffnen") || !strings.Contains(got, "'PDH.Terminal'") {
		t.Errorf("Skript nicht unverändert: %q", got)
	}
	// Der Text aus dem Netz steht nie im Skript.
	if strings.Contains(toastScript, "%s") || strings.Contains(toastScript, "{0}") {
		t.Error("Skript darf keine Platzhalter haben")
	}
}
