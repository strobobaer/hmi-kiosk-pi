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

// readInputEvents ruft fn für jedes Ereignis, bis r endet oder fehlschlägt.
func readInputEvents(r io.Reader, fn func(rawEvent)) {
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

// cornerDetector erkennt „Finger bleibt Hold lang oben rechts“. Nur der erste Finger
// (Slot 0) zählt. Ausgewertet wird bei EV_SYN; weil ein ruhender Finger keine
// Ereignisse erzeugt, muss der Aufrufer zusätzlich regelmäßig ein EV_SYN einspeisen.
type cornerDetector struct {
	hold         time.Duration
	minX, maxY   int64 // Feld: x >= minX und y <= maxY
	slot         int32
	touching     bool
	x, y         int32
	haveX, haveY bool
	inField      bool
	since        time.Time
	fired        bool
}

func newCornerDetector(cfg CornerCfg, x, y AxisRange) *cornerDetector {
	d := &cornerDetector{hold: cfg.Hold}
	if d.hold <= 0 {
		d.hold = defaultCornerHold
	}
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
	d.minX = int64(x.Max) - margin(int64(x.Max)-int64(x.Min), cfg.ScreenW)
	d.maxY = int64(y.Min) + margin(int64(y.Max)-int64(y.Min), cfg.ScreenH)
	return d
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
		return d.evaluate(now)
	}
	return false
}

func (d *cornerDetector) evaluate(now time.Time) bool {
	in := d.touching && d.haveX && d.haveY && int64(d.x) >= d.minX && int64(d.y) <= d.maxY
	if !in {
		d.inField, d.fired = false, false
		return false
	}
	if !d.inField {
		d.inField, d.since, d.fired = true, now, false
		return false
	}
	if !d.fired && now.Sub(d.since) >= d.hold {
		d.fired = true
		return true
	}
	return false
}
