package card

import (
	"context"
	"strings"
	"time"

	"hmikiosk/internal/platform"
)

const (
	keyBufMax = 32
	keyGap    = time.Second

	evKey    = 0x01
	keyEnter = 28
	keyKPEnt = 96
)

// keyChars bildet Linux-Tastencodes auf die Zeichen einer Karten-UID ab.
var keyChars = map[uint16]byte{
	2: '1', 3: '2', 4: '3', 5: '4', 6: '5', 7: '6', 8: '7', 9: '8', 10: '9', 11: '0',
	71: '7', 72: '8', 73: '9', 75: '4', 76: '5', 77: '6', 79: '1', 80: '2', 81: '3', 82: '0',
	16: 'Q', 17: 'W', 18: 'E', 19: 'R', 20: 'T', 21: 'Y', 22: 'U', 23: 'I', 24: 'O', 25: 'P',
	30: 'A', 31: 'S', 32: 'D', 33: 'F', 34: 'G', 35: 'H', 36: 'J', 37: 'K', 38: 'L',
	44: 'Z', 45: 'X', 46: 'C', 47: 'V', 48: 'B', 49: 'N', 50: 'M',
}

// keyBuffer sammelt die Tastenanschläge eines Tastaturlesers bis Enter.
type keyBuffer struct {
	buf      []byte
	overflow bool
	last     time.Time
}

// Key verarbeitet einen Tastendruck. done ist true, wenn Enter eine UID abschließt.
// Mehr als keyBufMax Zeichen verwerfen die Eingabe bis Enter; eine Pause über keyGap
// leert den Puffer.
func (b *keyBuffer) Key(code uint16, at time.Time) (uid string, done bool) {
	if !b.last.IsZero() && at.Sub(b.last) > keyGap {
		b.buf, b.overflow = b.buf[:0], false
	}
	b.last = at
	if code == keyEnter || code == keyKPEnt {
		defer func() { b.buf, b.overflow = b.buf[:0], false }()
		if b.overflow || len(b.buf) == 0 {
			return "", false
		}
		return string(b.buf), true
	}
	c, ok := keyChars[code]
	if !ok || b.overflow {
		return "", false
	}
	if len(b.buf) >= keyBufMax {
		b.overflow = true
		b.buf = b.buf[:0]
		return "", false
	}
	b.buf = append(b.buf, c)
	return "", false
}

// InputDevice beschreibt ein Eingabegerät (/dev/input/eventN).
type InputDevice struct {
	Path, Name string
	ID         string // vvvv:pppp, Hex klein
}

// matchDevice wählt ein Gerät: sel ist vvvv:pppp (exakt) oder Teil des Namens (ohne Groß/klein).
func matchDevice(devs []InputDevice, sel string) (InputDevice, bool) {
	sel = strings.TrimSpace(sel)
	if sel == "" {
		return InputDevice{}, false
	}
	for _, d := range devs {
		if d.ID == strings.ToLower(sel) {
			return d, true
		}
	}
	for _, d := range devs {
		if strings.Contains(strings.ToLower(d.Name), strings.ToLower(sel)) {
			return d, true
		}
	}
	return InputDevice{}, false
}

// usbReader liest einen Tastatur-Leser (HID, tippt die UID und Enter).
type usbReader struct {
	dev InputDevice
}

func (u *usbReader) Probe(ctx context.Context) (string, error) {
	return "USB: " + u.dev.Name, nil
}

// Run übernimmt das Gerät exklusiv (sonst landet die UID als Tastatureingabe im Browser)
// und liest erst danach.
func (u *usbReader) Run(ctx context.Context, emit func(string)) error {
	var buf keyBuffer
	return readExclusive(ctx, u.dev.Path, func(ev platform.InputEvent) {
		if ev.Type != evKey || ev.Value != 1 {
			return
		}
		if uid, done := buf.Key(ev.Code, time.Now()); done {
			emit(strings.ToUpper(strings.TrimSpace(uid)))
		}
	})
}
