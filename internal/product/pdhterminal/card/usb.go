package card

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
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

// matchDevices wählt alle Geräte der Auswahl sel: ein Pfad (/dev/input/eventN, genau),
// sonst vvvv:pppp (exakt, alle Knoten mit dieser Kennung), sonst ein Namensteil (ohne
// Groß/klein, alle passenden Knoten). Ein Leser meldet sich oft mit mehreren Knoten
// (Tastatur, Steuerung); getippt wird nur über einen, welcher, ist nicht vorhersagbar,
// darum werden alle übernommen.
func matchDevices(devs []InputDevice, sel string) []InputDevice {
	sel = strings.TrimSpace(sel)
	if sel == "" {
		return nil
	}
	var out []InputDevice
	pick := func(match func(InputDevice) bool) bool {
		for _, d := range devs {
			if match(d) {
				out = append(out, d)
			}
		}
		return len(out) > 0
	}
	low := strings.ToLower(sel)
	if pick(func(d InputDevice) bool { return d.Path == sel }) ||
		pick(func(d InputDevice) bool { return d.ID == low }) ||
		pick(func(d InputDevice) bool { return strings.Contains(strings.ToLower(d.Name), low) }) {
		return out
	}
	return nil
}

// eventPathLess ordnet /dev/input/event2 vor event10 (Zahl statt Text).
func eventPathLess(a, b string) bool {
	split := func(s string) (string, int) {
		i := len(s)
		for i > 0 && s[i-1] >= '0' && s[i-1] <= '9' {
			i--
		}
		n, err := strconv.Atoi(s[i:])
		if err != nil {
			return s, -1
		}
		return s[:i], n
	}
	pa, na := split(a)
	pb, nb := split(b)
	if pa != pb {
		return pa < pb
	}
	return na < nb
}

// usbNames fasst die Namen der Knoten ohne Doppel zusammen.
func usbNames(devs []InputDevice) string {
	var names []string
	for _, d := range devs {
		if !slices.Contains(names, d.Name) {
			names = append(names, d.Name)
		}
	}
	return strings.Join(names, ", ")
}

// usbReader liest einen Tastatur-Leser (HID, tippt die UID und Enter). sel ist die Auswahl
// (vvvv:pppp oder Namensteil); devs sind die beim Öffnen gefundenen Geräte.
type usbReader struct {
	sel    string
	format string // was der Leser tippt, siehe normalizeUID; leer: unverändert
	devs   []InputDevice
	warn   func(msg string) // Meldung des Abfangens (Windows); leer: wieder in Ordnung
}

func (u *usbReader) Probe(ctx context.Context) (string, error) {
	if len(u.devs) == 0 {
		return "", fmt.Errorf("USB-Leser %q nicht gefunden", u.sel)
	}
	return "USB: " + usbNames(u.devs), nil
}

// Run sammelt die Tasten aller Knoten des Lesers in einem gemeinsamen Puffer; readKeys liefert
// sie plattformabhängig (Linux: exklusiv gegriffene evdev-Knoten, Windows: Raw Input mit Abfangen).
func (u *usbReader) Run(ctx context.Context, emit func(string)) error {
	var (
		mu  sync.Mutex
		buf keyBuffer
	)
	return u.readKeys(ctx, func(code uint16, at time.Time) {
		mu.Lock()
		uid, done := buf.Key(code, at)
		mu.Unlock()
		if done {
			emit(normalizeUID(uid, u.format))
		}
	})
}
