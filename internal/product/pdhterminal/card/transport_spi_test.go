package card

import (
	"bytes"
	"context"
	"testing"
	"time"
)

// fakeSPI zeichnet die gesendeten Bytes auf und liefert vorbereitete Antworten (auf die Länge von tx
// gekürzt oder mit Nullen aufgefüllt); ohne Vorrat antwortet sie mit Nullen („nicht bereit“).
type fakeSPI struct {
	rx     [][]byte
	tx     [][]byte
	closed bool
}

func (s *fakeSPI) Transfer(tx []byte) ([]byte, error) {
	s.tx = append(s.tx, append([]byte(nil), tx...))
	out := make([]byte, len(tx))
	if len(s.rx) > 0 {
		copy(out, s.rx[0])
		s.rx = s.rx[1:]
	}
	return out, nil
}
func (s *fakeSPI) Close() error { s.closed = true; return nil }

var (
	spiNotReady = []byte{0x00, 0x00}
	spiReady    = []byte{0x00, 0x80} // Statusbyte 0x01, LSB zuerst auf dem Draht
)

// spiData baut eine Datenlesung: Dummybyte, dann die Bytes bitweise gedreht wie auf dem Draht.
func spiData(frame []byte) []byte { return append([]byte{0x00}, rev(frame)...) }

func TestBitReverseTable(t *testing.T) {
	if got := rev([]byte{0x01, 0xD4}); !bytes.Equal(got, []byte{0x80, 0x2B}) {
		t.Errorf("rev: %x", got)
	}
	for i := 0; i < 256; i++ {
		b := []byte{byte(i)}
		if got := rev(rev(b)); got[0] != byte(i) {
			t.Fatalf("rev(rev(%#x)) = %#x", i, got[0])
		}
	}
}

func TestSPIWriteFramePrefix(t *testing.T) {
	s := &fakeSPI{}
	frame := encodeFrame(0x02, nil)
	if err := newSPITransport(s).WriteFrame(context.Background(), frame); err != nil {
		t.Fatal(err)
	}
	want := rev(append([]byte{0x01}, frame...))
	if len(s.tx) != 1 || !bytes.Equal(s.tx[0], want) {
		t.Errorf("tx %x, erwartet %x", s.tx, want)
	}
	if !bytes.HasPrefix(s.tx[0], []byte{0x80, 0x00, 0x00, 0xFF}) {
		t.Errorf("Präfix %x", s.tx[0][:4])
	}
}

func TestSPIReadyAndRead(t *testing.T) {
	s := &fakeSPI{rx: [][]byte{spiNotReady, spiNotReady, spiReady, spiData(pn532Ack)}}
	got, err := newSPITransport(s).ReadFrame(context.Background(), len(pn532Ack))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, pn532Ack) {
		t.Errorf("got %x", got)
	}
	if len(s.tx) != 4 {
		t.Fatalf("%d Übertragungen", len(s.tx))
	}
	if want := rev([]byte{0x02, 0x00}); !bytes.Equal(s.tx[0], want) {
		t.Errorf("Statusabfrage %x", s.tx[0])
	}
	if want := rev(append([]byte{0x03}, make([]byte, len(pn532Ack))...)); !bytes.Equal(s.tx[3], want) {
		t.Errorf("Datenlesung %x", s.tx[3])
	}
}

func TestSPIReadyTimeout(t *testing.T) {
	s := &fakeSPI{}
	start := time.Now()
	_, err := newSPITransport(s).ReadFrame(context.Background(), 6)
	if err == nil {
		t.Fatal("Fehler erwartet")
	}
	if d := time.Since(start); d > 150*time.Millisecond {
		t.Errorf("Timeout nach %v", d)
	}
}

func TestPN532OverSPIProbe(t *testing.T) {
	s := &fakeSPI{rx: [][]byte{
		nil, spiReady, spiData(pn532Ack), // Schreiben, Status, ACK
		spiReady, spiData(respFrame(0x02, []byte{0x32, 0x01, 0x06, 0x07})), // Status, Antwort
	}}
	got, err := newPN532(newSPITransport(s), "").Probe(context.Background())
	if err != nil || got != "PN532 über SPI, Firmware 1.6" {
		t.Errorf("got %q err %v", got, err)
	}
}
