package card

import (
	"sort"
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

func TestMatchDevices(t *testing.T) {
	devs := []InputDevice{
		{Path: "/dev/input/event2", Name: "Sycreader RFID Keyboard", ID: "ffff:0035"},
		{Path: "/dev/input/event3", Name: "Sycreader RFID Control", ID: "ffff:0035"},
		{Path: "/dev/input/event4", Name: "Logitech USB Keyboard", ID: "046d:c31c"},
	}
	paths := func(d []InputDevice) []string {
		var p []string
		for _, x := range d {
			p = append(p, x.Path)
		}
		return p
	}
	cases := []struct {
		sel  string
		want []string
	}{
		{"FFFF:0035", []string{"/dev/input/event2", "/dev/input/event3"}}, // alle Knoten der Kennung
		{"sycreader", []string{"/dev/input/event2", "/dev/input/event3"}}, // Namensteil
		{"keyboard", []string{"/dev/input/event2", "/dev/input/event4"}},
		{"/dev/input/event4", []string{"/dev/input/event4"}}, // genauer Pfad
		{"/dev/input/event", nil},                            // kein Teilpfad
		{"  ", nil},
		{"gibtesnicht", nil},
	}
	for _, c := range cases {
		got := paths(matchDevices(devs, c.sel))
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%q: %v, erwartet %v", c.sel, got, c.want)
		}
	}
}

func TestEventPathLess(t *testing.T) {
	in := []string{"/dev/input/event10", "/dev/input/event2", "/dev/input/event1", "/dev/input/event11"}
	sort.Slice(in, func(i, j int) bool { return eventPathLess(in[i], in[j]) })
	want := "/dev/input/event1,/dev/input/event2,/dev/input/event10,/dev/input/event11"
	if got := strings.Join(in, ","); got != want {
		t.Fatalf("%s", got)
	}
}

func TestUSBReaderFormatOnlyFromUSBFormat(t *testing.T) {
	const typed = "0123456789"
	// Format ist PN532-Sache: ein altes format=dec ändert die UID des USB-Lesers nicht.
	r := newUSBReader(Settings{Type: TypeUSB, USBDevice: "x", Format: "dec"}, nil)
	if got := normalizeUID(typed, r.format); got != typed {
		t.Errorf("Format dec ohne USBFormat: %q, erwartet unverändert", got)
	}
	r = newUSBReader(Settings{Type: TypeUSB, USBDevice: "x", USBFormat: "dec"}, nil)
	if got := normalizeUID(typed, r.format); got != "15CD5B07" {
		t.Errorf("USBFormat dec: %q, erwartet 15CD5B07", got)
	}
}
