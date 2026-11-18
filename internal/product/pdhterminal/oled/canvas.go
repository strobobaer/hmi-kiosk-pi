// Package oled zeichnet das Statusbild für das SH1106-Display des PDH-Terminals.
package oled

import "strings"

// Bildgröße des Displays in Pixeln.
const Width, Height = 128, 64

// Canvas ist ein Ein-Bit-Bild mit Seiten-Aufbau (8 Zeilen je Seite).
type Canvas struct {
	px [Width * Height]bool
}

// NewCanvas liefert ein leeres (schwarzes) Bild.
func NewCanvas() *Canvas { return &Canvas{} }

// Set setzt einen Pixel; Koordinaten außerhalb werden ignoriert.
func (c *Canvas) Set(x, y int) {
	if x < 0 || x >= Width || y < 0 || y >= Height {
		return
	}
	c.px[y*Width+x] = true
}

// Text zeichnet s mit der 5×7-Schrift ab (x, y) links oben. Jedes Zeichen
// belegt eine Zelle von 6×8 Pixeln, scale vergrößert ganzzahlig.
func (c *Canvas) Text(x, y int, s string, scale int) {
	if scale < 1 {
		scale = 1
	}
	for _, r := range s {
		g := glyph(r)
		for col := 0; col < 5; col++ {
			for row := 0; row < 7; row++ {
				if g[col]&(1<<row) == 0 {
					continue
				}
				for dx := 0; dx < scale; dx++ {
					for dy := 0; dy < scale; dy++ {
						c.Set(x+col*scale+dx, y+row*scale+dy)
					}
				}
			}
		}
		x += 6 * scale
	}
}

// Rect zeichnet einen Umriss; beide Ecken gehören zum Rechteck.
func (c *Canvas) Rect(x0, y0, x1, y1 int) {
	for x := x0; x <= x1; x++ {
		c.Set(x, y0)
		c.Set(x, y1)
	}
	for y := y0; y <= y1; y++ {
		c.Set(x0, y)
		c.Set(x1, y)
	}
}

// FillRect füllt ein Rechteck; beide Ecken gehören dazu.
func (c *Canvas) FillRect(x0, y0, x1, y1 int) {
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			c.Set(x, y)
		}
	}
}

// Page liefert die 128 Bytes der Seite p (8 Zeilen); Bit 0 ist die oberste Zeile.
func (c *Canvas) Page(p int) []byte {
	out := make([]byte, Width)
	for x := 0; x < Width; x++ {
		for bit := 0; bit < 8; bit++ {
			if c.px[(p*8+bit)*Width+x] {
				out[x] |= 1 << bit
			}
		}
	}
	return out
}

// ASCII gibt das Bild als 64 Zeilen aus '#' (an) und '.' (aus) aus.
func (c *Canvas) ASCII() string {
	var b strings.Builder
	for y := 0; y < Height; y++ {
		for x := 0; x < Width; x++ {
			if c.px[y*Width+x] {
				b.WriteByte('#')
			} else {
				b.WriteByte('.')
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}
