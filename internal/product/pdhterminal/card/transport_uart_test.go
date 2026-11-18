package card

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

// fakeSerial liefert die Lese-Bruchstücke der Reihe nach; danach (0, nil) wie bei Zeitablauf.
type fakeSerial struct {
	reads  [][]byte
	writes [][]byte
	closed bool
}

func (s *fakeSerial) Read(p []byte) (int, error) {
	if len(s.reads) == 0 {
		time.Sleep(time.Millisecond)
		return 0, nil
	}
	n := copy(p, s.reads[0])
	if n < len(s.reads[0]) {
		s.reads[0] = s.reads[0][n:]
	} else {
		s.reads = s.reads[1:]
	}
	return n, nil
}
func (s *fakeSerial) Write(p []byte) (int, error) {
	s.writes = append(s.writes, append([]byte(nil), p...))
	return len(p), nil
}
func (s *fakeSerial) Close() error { s.closed = true; return nil }

func TestUARTWakeupOnce(t *testing.T) {
	s := &fakeSerial{}
	tr := newUARTTransport(s)
	f := encodeFrame(0x02, nil)
	for i := 0; i < 2; i++ {
		if err := tr.WriteFrame(context.Background(), f); err != nil {
			t.Fatal(err)
		}
	}
	all := bytes.Join(s.writes, nil)
	want := append(append(append([]byte(nil), uartWakeup...), f...), f...)
	if !bytes.Equal(all, want) {
		t.Errorf("gesendet %x, erwartet %x", all, want)
	}
	if !bytes.HasPrefix(all, []byte{0x55, 0x55, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}) || bytes.Count(all, []byte{0x55}) != 2 {
		t.Errorf("Aufweckfolge %x", all)
	}
	if tr.Name() != "UART" {
		t.Error(tr.Name())
	}
}

func TestUARTFrameFromFragments(t *testing.T) {
	s := &fakeSerial{reads: [][]byte{{0x00}, {0x00, 0xFF}, {0x00, 0xFF, 0x00}}}
	tr := newUARTTransport(s)
	got, err := tr.ReadFrame(context.Background(), 6)
	if err != nil || !bytes.Equal(got, pn532Ack) {
		t.Fatalf("ACK %x, %v", got, err)
	}
	resp := respFrame(0x4A, []byte{1, 1, 0, 4, 8, 4, 0xDE, 0xAD, 0xBE, 0xEF})
	s.reads = [][]byte{resp[:4], resp[4:11], resp[11:]}
	got, err = tr.ReadFrame(context.Background(), pn532RespBuf)
	if err != nil || !bytes.Equal(got, resp) {
		t.Fatalf("Antwort %x, %v", got, err)
	}
}

func TestUARTKeepsBytesAfterFrame(t *testing.T) {
	resp := respFrame(0x4A, []byte{0x00})
	s := &fakeSerial{reads: [][]byte{append(append([]byte(nil), pn532Ack...), resp...)}}
	tr := newUARTTransport(s)
	if got, err := tr.ReadFrame(context.Background(), 6); err != nil || !bytes.Equal(got, pn532Ack) {
		t.Fatalf("ACK %x, %v", got, err)
	}
	if got, err := tr.ReadFrame(context.Background(), pn532RespBuf); err != nil || !bytes.Equal(got, resp) {
		t.Fatalf("Antwort %x, %v", got, err)
	}
}

func TestUARTSkipsLeadingZeros(t *testing.T) {
	s := &fakeSerial{reads: [][]byte{{0x00, 0x00, 0x00, 0x00}, append([]byte{0xFF}, pn532Ack[3:]...)}}
	got, err := newUARTTransport(s).ReadFrame(context.Background(), 6)
	if err != nil || !bytes.Equal(got, pn532Ack) {
		t.Fatalf("%x, %v", got, err)
	}
	s = &fakeSerial{reads: [][]byte{append([]byte{0x12, 0x34, 0x00}, pn532Ack...)}}
	got, err = newUARTTransport(s).ReadFrame(context.Background(), 6)
	if err != nil || !bytes.Equal(got, pn532Ack) {
		t.Fatalf("%x, %v", got, err)
	}
}

func TestUARTTooLong(t *testing.T) {
	resp := respFrame(0x4A, make([]byte, 20))
	s := &fakeSerial{reads: [][]byte{resp}}
	_, err := newUARTTransport(s).ReadFrame(context.Background(), 6)
	if err == nil || !strings.Contains(err.Error(), "Rahmen zu lang") {
		t.Errorf("Fehler %v", err)
	}
}

func TestUARTTimeout(t *testing.T) {
	start := time.Now()
	_, err := newUARTTransport(&fakeSerial{}).ReadFrame(context.Background(), 6)
	if err == nil || err.Error() != "PN532: keine Antwort" {
		t.Fatalf("Fehler %v", err)
	}
	if d := time.Since(start); d > 150*time.Millisecond {
		t.Errorf("Wartezeit %v", d)
	}
}

func TestUARTContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := newUARTTransport(&fakeSerial{}).ReadFrame(ctx, 6); err != context.Canceled {
		t.Errorf("Fehler %v", err)
	}
}

func TestPN532OverUARTPoll(t *testing.T) {
	s := &fakeSerial{reads: [][]byte{pn532Ack, listResp[:7], listResp[7:]}}
	p := newPN532(newUARTTransport(s), "hex")
	got, err := p.poll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != FormatUID([]byte{0xDE, 0xAD, 0xBE, 0xEF}, "hex") {
		t.Errorf("UID %q", got)
	}
}
