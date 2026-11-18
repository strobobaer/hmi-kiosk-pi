package oled

import (
	"bytes"
	"errors"
	"sync"
	"testing"
)

// fakeBus zeichnet alle Schreibungen auf; failAt (1-basiert, 0 = nie) lässt diese Schreibung scheitern.
type fakeBus struct {
	mu     sync.Mutex
	writes [][]byte
	failAt int
	closed bool
}

func (f *fakeBus) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, append([]byte(nil), p...))
	if f.failAt != 0 && len(f.writes) == f.failAt {
		return 0, errors.New("Schreibfehler")
	}
	return len(p), nil
}

func (f *fakeBus) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakeBus) snapshot() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]byte(nil), f.writes...)
}

func TestSH1106Init(t *testing.T) {
	b := &fakeBus{}
	if err := (&sh1106{b: b}).Init(); err != nil {
		t.Fatal(err)
	}
	want := []byte{0x00, 0xAE, 0xD5, 0x80, 0xA8, 0x3F, 0xD3, 0x00, 0x40, 0xAD, 0x8B, 0xA1, 0xC8, 0xDA, 0x12,
		0x81, 0xCF, 0xD9, 0x22, 0xDB, 0x40, 0xA4, 0xA6, 0xAF}
	w := b.snapshot()
	if len(w) != 1 || !bytes.Equal(w[0], want) {
		t.Fatalf("Init schrieb %x, erwartet %x", w, want)
	}
}

func TestSH1106ShowPages(t *testing.T) {
	b := &fakeBus{}
	c := NewCanvas()
	for x := 0; x < Width; x++ {
		for y := 0; y < Height; y += 3 {
			c.Set(x, y)
		}
	}
	if err := (&sh1106{b: b}).Show(c); err != nil {
		t.Fatal(err)
	}
	w := b.snapshot()
	if len(w) != 16 {
		t.Fatalf("%d Schreibungen, erwartet 16", len(w))
	}
	for p := 0; p < 8; p++ {
		if want := []byte{0x00, byte(0xB0 + p), 0x02, 0x10}; !bytes.Equal(w[2*p], want) {
			t.Errorf("Seite %d Befehle %x, erwartet %x", p, w[2*p], want)
		}
		if want := append([]byte{0x40}, c.Page(p)...); !bytes.Equal(w[2*p+1], want) {
			t.Errorf("Seite %d Daten weichen ab", p)
		}
	}
}

func TestSH1106Off(t *testing.T) {
	b := &fakeBus{}
	if err := (&sh1106{b: b}).Off(); err != nil {
		t.Fatal(err)
	}
	w := b.snapshot()
	if len(w) != 17 || !bytes.Equal(w[len(w)-1], []byte{0x00, 0xAE}) {
		t.Fatalf("letzte Schreibung %x (%d Schreibungen)", w[len(w)-1], len(w))
	}
	for p := 0; p < 8; p++ {
		if !bytes.Equal(w[2*p+1][1:], make([]byte, 128)) {
			t.Errorf("Seite %d nicht leer", p)
		}
	}
}
