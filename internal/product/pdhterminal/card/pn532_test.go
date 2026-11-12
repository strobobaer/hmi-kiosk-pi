package card

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestEncodeFrame(t *testing.T) {
	got := encodeFrame(0x02, nil)
	want := []byte{0x00, 0x00, 0xFF, 0x02, 0xFE, 0xD4, 0x02, 0x2A, 0x00}
	if !bytes.Equal(got, want) {
		t.Errorf("GetFirmwareVersion: %x, want %x", got, want)
	}
	got = encodeFrame(0x4A, []byte{1, 0})
	want = []byte{0x00, 0x00, 0xFF, 0x04, 0xFC, 0xD4, 0x4A, 0x01, 0x00, 0xE1, 0x00}
	if !bytes.Equal(got, want) {
		t.Errorf("InListPassiveTarget: %x, want %x", got, want)
	}
}

var listResp = []byte{0x01, 0x00, 0x00, 0xFF, 0x0C, 0xF4, 0xD5, 0x4B, 0x01, 0x01, 0x00, 0x04, 0x08, 0x04, 0xDE, 0xAD, 0xBE, 0xEF, 0x96, 0x00}

// respFrame baut eine Antwort (mit Statusbyte) fuer cmd mit den Daten.
func respFrame(cmd byte, data []byte) []byte {
	body := append([]byte{0xD5, cmd + 1}, data...)
	sum := 0
	for _, b := range body {
		sum += int(b)
	}
	n := byte(len(body))
	out := []byte{0x01, 0x00, 0x00, 0xFF, n, byte(0x100-int(n)) & 0xFF}
	out = append(out, body...)
	return append(out, byte(0x100-sum)&0xFF, 0x00)
}

func TestDecodeFrame(t *testing.T) {
	data, err := decodeFrame(listResp, 0x4A)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{0x01, 0x01, 0x00, 0x04, 0x08, 0x04, 0xDE, 0xAD, 0xBE, 0xEF}
	if !bytes.Equal(data, want) {
		t.Errorf("Daten %x, want %x", data, want)
	}
	if !bytes.Equal(respFrame(0x4A, want), listResp) {
		t.Errorf("respFrame weicht ab: %x", respFrame(0x4A, want))
	}
	mut := func(f func(b []byte)) []byte {
		b := append([]byte(nil), listResp...)
		f(b)
		return b
	}
	bad := map[string][]byte{
		"DCS":      mut(func(b []byte) { b[18] ^= 1 }),
		"CMD+1":    mut(func(b []byte) { b[7] = 0x4C }),
		"Status 0": mut(func(b []byte) { b[0] = 0 }),
		"LCS":      mut(func(b []byte) { b[5] = 0 }),
		"D5":       mut(func(b []byte) { b[6] = 0xD4 }),
		"kurz":     listResp[:10],
		"leer":     nil,
	}
	for name, b := range bad {
		if _, err := decodeFrame(b, 0x4A); err == nil {
			t.Errorf("%s: Fehler erwartet", name)
		}
	}
}

// fakeConn liefert die Lesevorgaenge der Reihe nach; danach ein Fehler.
type fakeConn struct {
	reads  [][]byte
	writes [][]byte
	closed bool
}

func (c *fakeConn) Read(p []byte) (int, error) {
	if len(c.reads) == 0 {
		return 0, errors.New("script leer")
	}
	n := copy(p, c.reads[0])
	c.reads = c.reads[1:]
	return n, nil
}
func (c *fakeConn) Write(p []byte) (int, error) {
	c.writes = append(c.writes, append([]byte(nil), p...))
	return len(p), nil
}
func (c *fakeConn) Close() error { c.closed = true; return nil }

var (
	ack   = []byte{0x01, 0x00, 0x00, 0xFF, 0x00, 0xFF, 0x00}
	ready = []byte{0x01}
)

func TestPN532Poll(t *testing.T) {
	c := &fakeConn{reads: [][]byte{ready, ack, ready, listResp}}
	r := newPN532(c, "hex")
	uid, err := r.poll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if uid != "DEADBEEF" {
		t.Errorf("uid %q", uid)
	}
	if want := encodeFrame(0x4A, []byte{1, 0}); !bytes.Equal(c.writes[0], want) {
		t.Errorf("gesendet %x", c.writes[0])
	}

	// NbTg = 0: keine Karte
	c = &fakeConn{reads: [][]byte{ready, ack, ready, respFrame(0x4A, []byte{0x00})}}
	uid, err = newPN532(c, "hex").poll(context.Background())
	if err != nil || uid != "" {
		t.Errorf("ohne Karte: uid %q err %v", uid, err)
	}
}

func TestPN532EmptyUIDNotEmitted(t *testing.T) {
	// NbTg=1, aber NFCIDLen=0: leere UID, darf nicht als "0" ausgegeben werden.
	c := &fakeConn{reads: [][]byte{ready, ack, ready, respFrame(0x4A, []byte{1, 1, 0, 4, 8, 0})}}
	uid, err := newPN532(c, "dec").poll(context.Background())
	if err != nil || uid != "" {
		t.Errorf("uid %q err %v", uid, err)
	}
}

func TestPN532Format(t *testing.T) {
	c := &fakeConn{reads: [][]byte{ready, ack, ready, listResp}}
	uid, err := newPN532(c, "dec").poll(context.Background())
	if err != nil || uid != "4022250974" {
		t.Errorf("uid %q err %v", uid, err)
	}
}

func TestPN532Probe(t *testing.T) {
	c := &fakeConn{reads: [][]byte{ready, ack, ready, respFrame(0x02, []byte{0x32, 0x01, 0x06, 0x07})}}
	got, err := newPN532(c, "").Probe(context.Background())
	if err != nil || got != "PN532 v1.6" {
		t.Errorf("got %q err %v", got, err)
	}
}

func TestPresenceEmitsOnTransitionOnly(t *testing.T) {
	var p presence
	seq := []struct {
		uid  string
		emit bool
	}{
		{"A", true}, {"A", false}, {"A", false}, // liegt auf dem Leser
		{"", false}, {"A", false}, // eine leere Abfrage zählt nicht als „weg“
		{"", false}, {"", false}, {"A", true}, // zwei leere Abfragen: Karte war weg
		{"B", true}, {"A", true}, // andere UID
	}
	for i, s := range seq {
		if got := p.step(s.uid); got != s.emit {
			t.Fatalf("Schritt %d (%q): emit %v, erwartet %v", i, s.uid, got, s.emit)
		}
	}
}

// pollReads ist die Antwortfolge einer Befehlsrunde (bereit, ACK, bereit, Antwort).
func pollReads(resp []byte) [][]byte { return [][]byte{ready, ack, ready, resp} }

func TestPN532RunEmitsOncePerPresence(t *testing.T) {
	none := respFrame(0x4A, []byte{0x00})
	var reads [][]byte
	reads = append(reads, pollReads(respFrame(0x14, nil))...)
	reads = append(reads, pollReads(respFrame(0x32, nil))...)
	for _, r := range [][]byte{listResp, listResp, listResp, listResp, none, none, listResp} {
		reads = append(reads, pollReads(r)...)
	}
	c := &fakeConn{reads: reads}
	r := newPN532(c, "hex")
	r.interval = time.Millisecond
	var got []string
	err := r.Run(context.Background(), func(u string) { got = append(got, u) })
	if err == nil {
		t.Fatal("Fehler am Skriptende erwartet")
	}
	if want := []string{"DEADBEEF", "DEADBEEF"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ausgegeben %q, erwartet %q", got, want)
	}
}
