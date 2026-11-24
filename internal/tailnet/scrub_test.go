package tailnet

import (
	"context"
	"strings"
	"testing"
)

// Der Key liegt über der 200-Byte-Grenze: erst ersetzen, dann kürzen.
func TestScrubKeyStraddlesCut(t *testing.T) {
	ctx := WithSecret(context.Background(), secretKey)
	msg := strings.Repeat("x", 170) + " " + secretKey + " ende"
	got := Scrub(ctx, msg)
	if strings.Contains(got, "SECRET") || strings.Contains(got, "tskey-auth") || strings.Contains(got, secretKey[:30]) {
		t.Fatalf("Key (Teil) in %q", got)
	}
	if len(got) > MaxOutputMsg {
		t.Fatalf("Länge %d", len(got))
	}
}

// Ein Key ohne gemerkten Wert (nur Präfix tskey-) wird trotzdem unkenntlich.
func TestScrubPartialKeyWithPrefix(t *testing.T) {
	got := Scrub(context.Background(), "invalid key: tskey-auth-kSECRET123CNTRL-abc")
	if strings.Contains(got, "SECRET") || !strings.Contains(got, "tskey-***") {
		t.Fatalf("%q", got)
	}
	// Ersetzen vor dem Kürzen: Das Wort liegt hinter 190 Zeichen und würde angeschnitten.
	got = Scrub(context.Background(), strings.Repeat("y", 190)+"tskey-auth-kSECRET123")
	if strings.Contains(got, "SECRET") || strings.Contains(got, "tskey-auth") {
		t.Fatalf("%q", got)
	}
}

func TestScrubUTF8Boundary(t *testing.T) {
	got := Scrub(context.Background(), strings.Repeat("ä", 150))
	if !strings.HasSuffix(got, "ä") || len(got) > MaxOutputMsg || len(got)%2 != 0 {
		t.Fatalf("Länge %d", len(got))
	}
}
