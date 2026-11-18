package card

import (
	"context"
	"errors"
	"time"
)

// serialPort ist eine serielle Schnittstelle; Read liefert bei Zeitablauf (n=0, err=nil) statt zu blockieren.
type serialPort interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Close() error
}

// uartWakeup weckt den PN532 aus dem Schlaf (UM0701-02, HSU: 0x55 0x55 und Nullen).
var uartWakeup = []byte{0x55, 0x55, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}

// uartTransport setzt pn532Transport auf einen Bytestrom (HSU) um. Bytes hinter dem aktuellen Rahmen
// (ACK und Antwort kommen im selben Strom) bleiben für den nächsten ReadFrame im Puffer.
type uartTransport struct {
	p     serialPort
	woken bool
	buf   []byte
}

func newUARTTransport(p serialPort) pn532Transport { return &uartTransport{p: p} }

func (t *uartTransport) Name() string { return "UART" }

func (t *uartTransport) WriteFrame(_ context.Context, frame []byte) error {
	out := frame
	if !t.woken {
		out = append(append([]byte(nil), uartWakeup...), frame...)
	}
	for len(out) > 0 {
		n, err := t.p.Write(out)
		if err != nil {
			return err
		}
		out = out[n:]
	}
	t.woken = true
	return nil
}

func (t *uartTransport) ReadFrame(ctx context.Context, max int) ([]byte, error) {
	deadline := time.Now().Add(pn532ReadyTimeout)
	tmp := make([]byte, 64)
	for {
		frame, err := t.takeFrame(max)
		if err != nil || frame != nil {
			return frame, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !time.Now().Before(deadline) {
			return nil, errors.New("PN532: keine Antwort")
		}
		n, err := t.p.Read(tmp)
		if n > 0 {
			t.buf = append(t.buf, tmp[:n]...)
		}
		if err != nil {
			return nil, err
		}
	}
}

func (t *uartTransport) Close() error { return t.p.Close() }

// takeFrame entnimmt dem Puffer den nächsten vollständigen Rahmen ab 00 00 FF; nil, wenn er noch fehlt.
func (t *uartTransport) takeFrame(max int) ([]byte, error) {
	for {
		i := 0
		for i+2 < len(t.buf) && !(t.buf[i] == 0x00 && t.buf[i+1] == 0x00 && t.buf[i+2] == 0xFF) {
			i++
		}
		if i+2 >= len(t.buf) {
			// Kein Anfang gefunden: nur die letzten zwei Bytes könnten ein halber Anfang sein.
			if len(t.buf) > 2 {
				t.buf = append(t.buf[:0], t.buf[len(t.buf)-2:]...)
			}
			return nil, nil
		}
		t.buf = t.buf[i:] // führende Bytes und überzählige Nullen verwerfen
		if len(t.buf) < 5 {
			return nil, nil
		}
		ln, lcs := t.buf[3], t.buf[4]
		var total int
		switch {
		case ln == 0x00 && lcs == 0xFF, ln == 0xFF && lcs == 0x00: // ACK, NACK
			total = 6
		case byte(ln+lcs) == 0:
			total = 7 + int(ln) // 00 00 FF LEN LCS Daten DCS 00
		default:
			t.buf = t.buf[1:] // Längenprüfsumme falsch: neu aufsetzen
			continue
		}
		if total > max {
			t.buf = t.buf[1:]
			return nil, errors.New("PN532: Rahmen zu lang")
		}
		if len(t.buf) < total {
			return nil, nil
		}
		frame := append([]byte(nil), t.buf[:total]...)
		t.buf = t.buf[total:]
		return frame, nil
	}
}
