package oled

import "fmt"

// bus ist die Schreibseite des I²C-Anschlusses (ein Write = eine Übertragung).
type bus interface {
	Write([]byte) (int, error)
	Close() error
}

// sh1106 steuert ein SH1106-OLED (128×64) an.
type sh1106 struct{ b bus }

// initSeq ist die Startfolge (nach dem Steuerbyte 0x00).
var initSeq = []byte{0xAE, 0xD5, 0x80, 0xA8, 0x3F, 0xD3, 0x00, 0x40, 0xAD, 0x8B, 0xA1, 0xC8,
	0xDA, 0x12, 0x81, 0xCF, 0xD9, 0x22, 0xDB, 0x40, 0xA4, 0xA6, 0xAF}

// Init schaltet das Display ein und stellt es ein.
func (d *sh1106) Init() error {
	return d.write(append([]byte{0x00}, initSeq...))
}

// Show überträgt das Bild seitenweise; der SH1106 hat 132 Spalten, sichtbar ab Spalte 2.
func (d *sh1106) Show(c *Canvas) error {
	for p := 0; p < Height/8; p++ {
		if err := d.write([]byte{0x00, byte(0xB0 + p), 0x02, 0x10}); err != nil {
			return err
		}
		if err := d.write(append([]byte{0x40}, c.Page(p)...)); err != nil {
			return err
		}
	}
	return nil
}

// Off löscht das Bild und schaltet das Display ab.
func (d *sh1106) Off() error {
	if err := d.Show(NewCanvas()); err != nil {
		return err
	}
	return d.write([]byte{0x00, 0xAE})
}

func (d *sh1106) write(p []byte) error {
	n, err := d.b.Write(p)
	if err != nil {
		return err
	}
	if n != len(p) {
		return fmt.Errorf("kurze Schreibung (%d von %d Byte)", n, len(p))
	}
	return nil
}

// Probe prüft, ob das Display unter dev/addr antwortet (öffnet, schreibt {0x00, 0xE3} = NOP, schließt).
func Probe(dev string, addr int) error {
	b, err := openBus(dev, addr)
	if err != nil {
		return err
	}
	defer b.Close()
	return (&sh1106{b: b}).write([]byte{0x00, 0xE3})
}
