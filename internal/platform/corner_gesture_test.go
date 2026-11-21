package platform

import (
	"testing"
	"time"
)

// Feld 1840..1920 × 0..80 auf einem Bildschirm von 1920 × 1080.
func testGesture() *cornerGesture { return newCornerGesture(5*time.Second, 1840, 80) }

func TestGestureHoldFiresOnce(t *testing.T) {
	g := testGesture()
	if g.Down(1900, 10, t0) {
		t.Fatal("Aufsetzen darf nicht treffen")
	}
	if g.Tick(t0.Add(4900 * time.Millisecond)) {
		t.Fatal("4,9 s darf nicht treffen")
	}
	if !g.Tick(t0.Add(5 * time.Second)) {
		t.Fatal("5 s muss treffen")
	}
	if g.Tick(t0.Add(6*time.Second)) || g.Move(1901, 11, t0.Add(7*time.Second)) {
		t.Fatal("nur ein Treffer je Halten")
	}
	g.Up(t0.Add(8 * time.Second))
	g.Down(1900, 10, t0.Add(9*time.Second))
	if !g.Tick(t0.Add(14 * time.Second)) {
		t.Fatal("nach Loslassen erneut möglich")
	}
}

func TestGestureTapDoesNothing(t *testing.T) {
	g := testGesture()
	g.Down(1900, 10, t0)
	if g.Up(t0.Add(200 * time.Millisecond)) {
		t.Fatal("Antippen darf nicht treffen")
	}
	// Auch ein späterer Takt ohne Aufsetzen trifft nie.
	if g.Tick(t0.Add(10 * time.Second)) {
		t.Fatal("ohne Aufsetzen kein Treffer")
	}
}

func TestGestureOutsideDoesNothing(t *testing.T) {
	g := testGesture()
	g.Down(900, 500, t0)
	if g.Tick(t0.Add(30 * time.Second)) {
		t.Fatal("Halten außerhalb des Felds darf nicht treffen")
	}
	g = testGesture()
	g.Down(1900, 200, t0) // rechts, aber zu tief
	if g.Tick(t0.Add(30 * time.Second)) {
		t.Fatal("zu tief")
	}
}

func TestGestureDragOutAborts(t *testing.T) {
	g := testGesture()
	g.Down(1900, 10, t0)
	g.Move(1000, 500, t0.Add(3*time.Second)) // hinaus gezogen
	if g.Tick(t0.Add(6 * time.Second)) {
		t.Fatal("nach dem Ziehen hinaus darf nichts treffen")
	}
	// Zurück ins Feld: die Haltezeit beginnt neu.
	g.Move(1900, 10, t0.Add(7*time.Second))
	if g.Tick(t0.Add(11 * time.Second)) {
		t.Fatal("Haltezeit zählt ab der Rückkehr")
	}
	if !g.Tick(t0.Add(12 * time.Second)) {
		t.Fatal("5 s nach der Rückkehr muss treffen")
	}
}

func TestGestureMoveWithoutDownIgnored(t *testing.T) {
	g := testGesture()
	g.Move(1900, 10, t0) // Maus ohne Taste
	if g.Tick(t0.Add(30 * time.Second)) {
		t.Fatal("Bewegen ohne Taste darf nicht treffen")
	}
}

func TestGestureMoveWithinFieldKeepsHold(t *testing.T) {
	g := testGesture()
	g.Down(1850, 5, t0)
	g.Move(1900, 60, t0.Add(2*time.Second))
	if !g.Tick(t0.Add(5 * time.Second)) {
		t.Fatal("Bewegen im Feld darf die Haltezeit nicht zurücksetzen")
	}
}
