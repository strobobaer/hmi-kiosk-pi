package platform

import (
	"encoding/binary"
	"io"
	"strconv"
	"time"
)

// Ereignistypen und -codes aus linux/input-event-codes.h (plattformneutral, damit
// die Erkennung ohne Linux testbar bleibt).
const (
	evSyn = 0x00
	evKey = 0x01
	evAbs = 0x03

	btnTouch = 0x14a

	absX            = 0x00
	absY            = 0x01
	absMTSlot       = 0x2f
	absMTPositionX  = 0x35
	absMTPositionY  = 0x36
	absMTTrackingID = 0x39
)

const (
	defaultCornerPx   = 80
	defaultCornerPct  = 6 // Anteil der Achse, wenn die Bildschirmgröße unbekannt ist
	defaultCornerHold = 5 * time.Second
)

// struct input_event: timeval (2 Wörter) + type u16 + code u16 + value i32.
var inputEventSize = 2*(strconv.IntSize/8) + 8

type rawEvent struct {
	Type, Code uint16
	Value      int32
}

func decodeInputEvent(buf []byte) rawEvent {
	return rawEvent{
		Type:  binary.NativeEndian.Uint16(buf[inputEventSize-8:]),
		Code:  binary.NativeEndian.Uint16(buf[inputEventSize-6:]),
		Value: int32(binary.NativeEndian.Uint32(buf[inputEventSize-4:])),
	}
}

// InputEvent ist ein dekodiertes struct input_event (Type, Code, Value).
type InputEvent = rawEvent

// ReadInputEvents ruft fn für jedes Ereignis, bis r endet oder fehlschlägt.
func ReadInputEvents(r io.Reader, fn func(InputEvent)) {
	buf := make([]byte, inputEventSize)
	for {
		if _, err := io.ReadFull(r, buf); err != nil {
			return
		}
		fn(decodeInputEvent(buf))
	}
}

// CornerCfg beschreibt das Feld oben rechts und die Haltezeit.
type CornerCfg struct {
	ScreenW, ScreenH int           // Bildschirm in Pixeln; 0 = unbekannt (dann 6 % der Achse)
	RegionPx         int           // Kantenlänge des Felds in Pixeln, Standard 80
	Hold             time.Duration // Haltezeit, Standard 5 s
}

// AxisRange ist der Wertebereich einer Touchachse (EVIOCGABS).
type AxisRange struct{ Min, Max int32 }

// cornerGesture ist die plattformneutrale Erkennung „Zeiger bleibt Hold lang im Feld oben
// rechts“ (x >= minX und y <= maxY). Die Plattformen speisen sie mit Down, Move und Up;
// weil ein ruhender Zeiger keine Ereignisse erzeugt, muss der Aufrufer zusätzlich regelmäßig
// Tick rufen. Jede Methode liefert true genau einmal je Halten. Nicht nebenläufig sicher.
type cornerGesture struct {
	hold       time.Duration
	minX, maxY int64
	down       bool
	x, y       int64
	inField    bool
	since      time.Time
	fired      bool
}

func newCornerGesture(hold time.Duration, minX, maxY int64) *cornerGesture {
	if hold <= 0 {
		hold = defaultCornerHold
	}
	return &cornerGesture{hold: hold, minX: minX, maxY: maxY}
}

// Down: Der Zeiger (erster Finger, linke Maustaste) setzt bei (x, y) auf.
func (g *cornerGesture) Down(x, y int64, now time.Time) bool {
	g.down, g.x, g.y = true, x, y
	return g.evaluate(now)
}

// Move: Der aufgesetzte Zeiger bewegt sich; ohne Aufsetzen ohne Wirkung.
func (g *cornerGesture) Move(x, y int64, now time.Time) bool {
	if !g.down {
		return false
	}
	g.x, g.y = x, y
	return g.evaluate(now)
}

// Up: Der Zeiger hebt ab; das Halten ist beendet.
func (g *cornerGesture) Up(now time.Time) bool {
	g.down = false
	return g.evaluate(now)
}

// Tick prüft die Haltezeit, ohne dass sich etwas bewegt hat.
func (g *cornerGesture) Tick(now time.Time) bool { return g.evaluate(now) }

func (g *cornerGesture) evaluate(now time.Time) bool {
	in := g.down && g.x >= g.minX && g.y <= g.maxY
	if !in {
		g.inField, g.fired = false, false
		return false
	}
	if !g.inField {
		g.inField, g.since, g.fired = true, now, false
		return false
	}
	if !g.fired && now.Sub(g.since) >= g.hold {
		g.fired = true
		return true
	}
	return false
}

// cornerDetector setzt Linux-Eingabeereignisse (evdev) auf cornerGesture um. Nur der erste
// Finger (Slot 0) zählt. Ausgewertet wird bei EV_SYN; weil ein ruhender Finger keine
// Ereignisse erzeugt, muss der Aufrufer zusätzlich regelmäßig ein EV_SYN einspeisen.
type cornerDetector struct {
	g            *cornerGesture
	slot         int32
	touching     bool
	x, y         int32
	haveX, haveY bool
}

func newCornerDetector(cfg CornerCfg, x, y AxisRange) *cornerDetector {
	px := cfg.RegionPx
	if px <= 0 {
		px = defaultCornerPx
	}
	margin := func(span int64, screen int) int64 {
		if screen > 0 {
			return span * int64(px) / int64(screen)
		}
		return span * defaultCornerPct / 100
	}
	minX := int64(x.Max) - margin(int64(x.Max)-int64(x.Min), cfg.ScreenW)
	maxY := int64(y.Min) + margin(int64(y.Max)-int64(y.Min), cfg.ScreenH)
	return &cornerDetector{g: newCornerGesture(cfg.Hold, minX, maxY)}
}

// feed verarbeitet ein Ereignis und liefert true genau einmal je Halten.
func (d *cornerDetector) feed(ev rawEvent, now time.Time) bool {
	switch ev.Type {
	case evKey:
		if ev.Code == btnTouch {
			d.touching = ev.Value != 0
		}
	case evAbs:
		if ev.Code == absMTSlot {
			d.slot = ev.Value
			return false
		}
		if d.slot != 0 {
			return false
		}
		switch ev.Code {
		case absX, absMTPositionX:
			d.x, d.haveX = ev.Value, true
		case absY, absMTPositionY:
			d.y, d.haveY = ev.Value, true
		case absMTTrackingID:
			d.touching = ev.Value >= 0
		}
	case evSyn:
		switch {
		case !d.touching || !d.haveX || !d.haveY:
			return d.g.Up(now)
		case !d.g.down:
			return d.g.Down(int64(d.x), int64(d.y), now)
		default:
			return d.g.Move(int64(d.x), int64(d.y), now)
		}
	}
	return false
}
