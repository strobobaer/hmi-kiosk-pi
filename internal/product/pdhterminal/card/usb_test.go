package card

import (
	"strings"
	"testing"
	"time"
)

// feedKeys speist die Tasten im Abstand von 10 ms ein und liefert das letzte Ergebnis.
func feedKeys(b *keyBuffer, at time.Time, codes ...uint16) (string, bool) {
	var uid string
	var done bool
	for _, c := range codes {
		uid, done = b.Key(c, at)
		at = at.Add(10 * time.Millisecond)
	}
	return uid, done
}

func TestKeyBuffer(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	var b keyBuffer
	// 0 4 A 1 Enter
	uid, done := feedKeys(&b, t0, 11, 5, 30, 2, 28)
	if !done || uid != "04A1" {
		t.Errorf("uid %q done %v", uid, done)
	}
	// Ziffernblock und KP-Enter
	uid, done = feedKeys(&b, t0.Add(time.Minute), 79, 82, 96)
	if !done || uid != "10" {
		t.Errorf("Ziffernblock: %q %v", uid, done)
	}
	// 33 Zeichen -> verworfen
	long := make([]uint16, 33)
	for i := range long {
		long[i] = 2
	}
	if uid, done := feedKeys(&b, t0.Add(2*time.Minute), append(long, 28)...); done {
		t.Errorf("Ueberlauf lieferte %q", uid)
	}
	// 32 Zeichen sind noch erlaubt, und der Puffer ist nach dem Ueberlauf wieder frei.
	if uid, done := feedKeys(&b, t0.Add(3*time.Minute), append(long[:32], 28)...); !done || uid != strings.Repeat("1", 32) {
		t.Errorf("32 Zeichen: %q %v", uid, done)
	}
	// Pause 1,5 s mitten drin
	at := t0.Add(10 * time.Minute)
	b.Key(2, at)
	b.Key(3, at.Add(10*time.Millisecond))
	b.Key(4, at.Add(1510*time.Millisecond)) // nach der Pause
	uid, done = b.Key(28, at.Add(1520*time.Millisecond))
	if !done || uid != "3" {
		t.Errorf("Pause: %q %v", uid, done)
	}
	// Enter ohne Zeichen liefert nichts
	if _, done := b.Key(28, at.Add(1530*time.Millisecond)); done {
		t.Error("leeres Enter")
	}
}
