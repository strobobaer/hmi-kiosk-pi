package platform

import (
	"testing"
	"time"
)

var (
	testX = AxisRange{0, 1919}
	testY = AxisRange{0, 1079}
	t0    = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
)

func testCfg() CornerCfg { return CornerCfg{ScreenW: 1920, ScreenH: 1080} }

// press sendet Berührung an (x, y) bei t0 (Variante: Einzelpunkt oder Multitouch).
func press(d *cornerDetector, mt bool, x, y int32) {
	if mt {
		d.feed(rawEvent{evAbs, absMTTrackingID, 7}, t0)
		d.feed(rawEvent{evAbs, absMTPositionX, x}, t0)
		d.feed(rawEvent{evAbs, absMTPositionY, y}, t0)
	} else {
		d.feed(rawEvent{evKey, btnTouch, 1}, t0)
		d.feed(rawEvent{evAbs, absX, x}, t0)
		d.feed(rawEvent{evAbs, absY, y}, t0)
	}
	d.feed(rawEvent{evSyn, 0, 0}, t0)
}

func release(d *cornerDetector, mt bool, at time.Time) {
	if mt {
		d.feed(rawEvent{evAbs, absMTTrackingID, -1}, at)
	} else {
		d.feed(rawEvent{evKey, btnTouch, 0}, at)
	}
	d.feed(rawEvent{evSyn, 0, 0}, at)
}

func tick(d *cornerDetector, after time.Duration) bool {
	return d.feed(rawEvent{Type: evSyn}, t0.Add(after))
}

func TestCornerHoldFiresOnce(t *testing.T) {
	for _, mt := range []bool{false, true} {
		d := newCornerDetector(testCfg(), testX, testY)
		press(d, mt, 1900, 10)
		if tick(d, 4900*time.Millisecond) {
			t.Fatalf("mt=%v: 4,9 s darf nicht treffen", mt)
		}
		if !tick(d, 5*time.Second) {
			t.Fatalf("mt=%v: 5 s muss treffen", mt)
		}
		if tick(d, 6*time.Second) || tick(d, 9*time.Second) {
			t.Fatalf("mt=%v: nur ein Treffer je Halten", mt)
		}
		release(d, mt, t0.Add(10*time.Second))
		press(d, mt, 1900, 10) // erneut berühren: neues Halten ab t0
		if !tick(d, 5*time.Second) {
			t.Fatalf("mt=%v: nach Loslassen erneut möglich", mt)
		}
	}
}

func TestCornerLeavingFieldResets(t *testing.T) {
	d := newCornerDetector(testCfg(), testX, testY)
	press(d, false, 1900, 10)
	tick(d, 3*time.Second)
	d.feed(rawEvent{evAbs, absX, 900}, t0.Add(3*time.Second)) // Feld verlassen
	if tick(d, 3100*time.Millisecond) {
		t.Fatal("außerhalb kein Treffer")
	}
	d.feed(rawEvent{evAbs, absX, 1900}, t0.Add(3200*time.Millisecond)) // zurück: Zeit beginnt neu
	tick(d, 3200*time.Millisecond)
	if tick(d, 5500*time.Millisecond) {
		t.Fatal("Zeit muss nach Verlassen neu beginnen")
	}
	if !tick(d, 8200*time.Millisecond) {
		t.Fatal("5 s nach Wiedereintritt muss treffen")
	}
}

func TestCornerOtherCornersAndEdges(t *testing.T) {
	for _, c := range []struct {
		name string
		x, y int32
		hit  bool
	}{
		{"unten links", 10, 1070, false},
		{"unten rechts", 1900, 1070, false},
		{"oben links", 10, 10, false},
		{"Feldkante innen", 1919 - 79, 79, true},
		{"knapp außerhalb", 1919 - 80, 10, false},
	} {
		d := newCornerDetector(testCfg(), testX, testY)
		press(d, false, c.x, c.y)
		tick(d, time.Second)
		if got := tick(d, 6*time.Second); got != c.hit {
			t.Errorf("%s: %v, erwartet %v", c.name, got, c.hit)
		}
	}
}

func TestCornerOnlyFirstSlot(t *testing.T) {
	d := newCornerDetector(testCfg(), testX, testY)
	d.feed(rawEvent{evAbs, absMTSlot, 1}, t0) // zweiter Finger oben rechts
	d.feed(rawEvent{evAbs, absMTTrackingID, 9}, t0)
	d.feed(rawEvent{evAbs, absMTPositionX, 1900}, t0)
	d.feed(rawEvent{evAbs, absMTPositionY, 10}, t0)
	d.feed(rawEvent{evSyn, 0, 0}, t0)
	if tick(d, time.Second) || tick(d, 6*time.Second) {
		t.Fatal("Slot 1 darf nicht zählen")
	}
	d.feed(rawEvent{evAbs, absMTSlot, 0}, t0)
	press(d, true, 1900, 10) // erster Finger
	if !tick(d, 5*time.Second) {
		t.Fatal("Slot 0 muss zählen")
	}
}

func TestCornerScreenSizeUnknownUsesSixPercent(t *testing.T) {
	cfg := CornerCfg{} // ScreenW/H 0
	d := newCornerDetector(cfg, AxisRange{0, 1000}, AxisRange{0, 1000})
	press(d, false, 950, 50) // innerhalb der oberen rechten 6 % (>= 940, <= 60)
	tick(d, time.Second)
	if !tick(d, 6*time.Second) {
		t.Fatal("innerhalb 6 % muss treffen")
	}
	d = newCornerDetector(cfg, AxisRange{0, 1000}, AxisRange{0, 1000})
	press(d, false, 930, 50)
	tick(d, time.Second)
	if tick(d, 6*time.Second) {
		t.Fatal("außerhalb 6 % darf nicht treffen")
	}
}

func TestCornerNonZeroAxisMin(t *testing.T) {
	d := newCornerDetector(testCfg(), AxisRange{100, 2019}, AxisRange{200, 1279})
	press(d, true, 2000, 210)
	tick(d, time.Second)
	if !tick(d, 6*time.Second) {
		t.Fatal("Bereich mit Min > 0 muss treffen")
	}
}
