package rfb

import (
	"bytes"
	"context"
	"crypto/des"
	"encoding/binary"
	"encoding/hex"
	"image"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func startServer(t *testing.T, w, h int, password string) (*TestServer, string, int) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := NewTestServer(w, h, password)
	go s.Serve(l)
	t.Cleanup(func() { l.Close(); s.DropClients() })
	addr := l.Addr().(*net.TCPAddr)
	return s, "127.0.0.1", addr.Port
}

func TestProbeReadsResolutionAndName(t *testing.T) {
	for _, pw := range []string{"", "geheim"} {
		_, host, port := startServer(t, 1024, 600, pw)
		r := Probe(context.Background(), host, port, pw)
		if !r.Reachable || !r.AuthOK || r.Width != 1024 || r.Height != 600 || r.Name == "" || r.Err != "" {
			t.Fatalf("pw=%q: %+v", pw, r)
		}
	}
}

func TestProbeWrongPassword(t *testing.T) {
	_, host, port := startServer(t, 800, 480, "richtig")
	r := Probe(context.Background(), host, port, "falsch")
	if !r.Reachable || r.AuthOK || !r.AuthRejected || r.Err == "" {
		t.Fatalf("%+v", r)
	}
	r = Probe(context.Background(), host, port, "")
	if !r.Reachable || r.AuthOK || !r.AuthRejected || r.Err == "" {
		t.Fatalf("ohne Passwort: %+v", r)
	}
}

// rejectingServer antwortet mit Version ver und ohne Sicherheitsverfahren samt Begründung.
func rejectingServer(t *testing.T, ver, reason string) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			c.Write([]byte(ver))
			io.ReadFull(c, make([]byte, 12))
			if ver == "RFB 003.003\n" {
				binary.Write(c, binary.BigEndian, uint32(0))
			} else {
				c.Write([]byte{0})
			}
			binary.Write(c, binary.BigEndian, uint32(len(reason)))
			c.Write([]byte(reason))
			c.Close()
		}
	}()
	return l.Addr().(*net.TCPAddr).Port
}

func TestProbeShowsServerReasonWhenRejected(t *testing.T) {
	for _, ver := range []string{"RFB 003.008\n", "RFB 003.003\n"} {
		port := rejectingServer(t, ver, "Too many security failures")
		r := Probe(context.Background(), "127.0.0.1", port, "")
		if !r.Reachable || r.AuthOK || !strings.Contains(r.Err, "Too many security failures") || !strings.Contains(r.Err, "lehnt die Verbindung ab") {
			t.Fatalf("%q: %+v", ver, r)
		}
	}
}

func TestProbeNonVNCPort(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			c.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"))
			c.Close()
		}
	}()
	r := Probe(context.Background(), "127.0.0.1", l.Addr().(*net.TCPAddr).Port, "")
	if !r.Reachable || r.AuthOK || r.Err == "" {
		t.Fatalf("%+v", r)
	}
}

func TestProbeSilentPortHonoursContext(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	r := Probe(ctx, "127.0.0.1", l.Addr().(*net.TCPAddr).Port, "")
	if !r.Reachable || r.Err == "" || time.Since(start) > 2*time.Second {
		t.Fatalf("%+v nach %v", r, time.Since(start))
	}
}

func TestProbeClosedPort(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	r := Probe(context.Background(), "127.0.0.1", port, "")
	if r.Reachable || r.Err == "" {
		t.Fatalf("%+v", r)
	}
	if TCPReachable(context.Background(), "127.0.0.1", port) {
		t.Fatal("TCPReachable true bei geschlossenem Port")
	}
}

func TestTCPReachable(t *testing.T) {
	_, host, port := startServer(t, 100, 100, "")
	if !TCPReachable(context.Background(), host, port) {
		t.Fatal("erreichbarer Port nicht erkannt")
	}
}

func TestProbeUmlautAndLongPassword(t *testing.T) {
	for _, pw := range []string{"pässwörtchen-sehr-lang", "äöüß€"} {
		_, host, port := startServer(t, 640, 480, pw)
		if r := Probe(context.Background(), host, port, pw); !r.AuthOK {
			t.Fatalf("%q: %+v", pw, r)
		}
	}
	ch := []byte("0123456789abcdef")
	a, _ := vncAuthResponse("12345678", ch)
	b, _ := vncAuthResponse("12345678xyz", ch)
	c, _ := vncAuthResponse("1234567", ch)
	if string(a) != string(b) {
		t.Fatal("nur die ersten 8 Byte dürfen zählen")
	}
	if string(a) == string(c) {
		t.Fatal("kürzeres Passwort muss andere Antwort ergeben")
	}
}

// rawClient führt Handshake 3.8 ohne Passwort aus.
func rawClient(t *testing.T, host string, port int) net.Conn {
	t.Helper()
	c, err := net.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	c.SetDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 12)
	io.ReadFull(c, buf)
	c.Write([]byte("RFB 003.008\n"))
	sec := make([]byte, 2)
	io.ReadFull(c, sec)
	c.Write([]byte{sec[1]})
	res := make([]byte, 4)
	io.ReadFull(c, res)
	c.Write([]byte{1})
	si := make([]byte, 24+len(testServerName))
	if _, err := io.ReadFull(c, si); err != nil {
		t.Fatal(err)
	}
	return c
}

func request(c net.Conn, incremental byte, x, y, w, h uint16) {
	b := []byte{3, incremental, 0, 0, 0, 0, 0, 0, 0, 0}
	binary.BigEndian.PutUint16(b[2:], x)
	binary.BigEndian.PutUint16(b[4:], y)
	binary.BigEndian.PutUint16(b[6:], w)
	binary.BigEndian.PutUint16(b[8:], h)
	c.Write(b)
}

func TestServerUpdatesPointersAndFormats(t *testing.T) {
	s, host, port := startServer(t, 300, 200, "")
	c := rawClient(t, host, port)

	// unbekannte Nachrichten in definierter Länge werden ignoriert
	c.Write([]byte{2, 0, 0, 2, 0, 0, 0, 0, 0, 0, 0, 1})    // SetEncodings, 2 Kodierungen
	c.Write([]byte{4, 1, 0, 0, 0, 0, 0, 65})               // KeyEvent
	c.Write([]byte{6, 0, 0, 0, 0, 0, 0, 3, 'a', 'b', 'c'}) // ClientCutText
	c.Write([]byte{5, 0, 0, 10, 0, 10})                    // Zeiger ohne Taste
	c.Write([]byte{5, 1, 0, 42, 0, 17})                    // Zeiger mit Taste
	request(c, 0, 0, 0, 300, 200)
	hdr := make([]byte, 16)
	if _, err := io.ReadFull(c, hdr); err != nil {
		t.Fatal(err)
	}
	if hdr[0] != 0 || binary.BigEndian.Uint16(hdr[2:]) != 1 || binary.BigEndian.Uint16(hdr[8:]) != 300 || binary.BigEndian.Uint16(hdr[10:]) != 200 {
		t.Fatalf("Kopf %v", hdr)
	}
	px := make([]byte, 300*200*4)
	if _, err := io.ReadFull(c, px); err != nil {
		t.Fatal(err)
	}
	if px[0] != 255 || px[1] != 255 || px[2] != 255 { // weißer Rand
		t.Fatalf("Randpixel %v", px[:4])
	}
	if o := (50*300 + 50) * 4; px[o] != 32 || px[o+1] != 24 || px[o+2] != 20 { // B,G,R
		t.Fatalf("Hintergrund %v", px[o:o+4])
	}
	got := s.Pointers()
	if len(got) != 1 || got[0] != image.Pt(42, 17) {
		t.Fatalf("Zeiger %v", got)
	}
	got[0] = image.Pt(0, 0)
	if s.Pointers()[0] != image.Pt(42, 17) {
		t.Fatal("Pointers liefert keine Kopie")
	}

	// 16 bpp: 5/6/5, little endian
	pf := []byte{0, 0, 0, 0, 16, 16, 0, 1, 0, 31, 0, 63, 0, 31, 11, 5, 0, 0, 0, 0}
	c.Write(pf)
	request(c, 0, 0, 0, 2, 1)
	small := make([]byte, 16+2*2)
	if _, err := io.ReadFull(c, small); err != nil {
		t.Fatal(err)
	}
	if small[16] != 0xff || small[17] != 0xff {
		t.Fatalf("16-bpp-Weiß %v", small[16:])
	}

	// inkrementell: leere Aktualisierung, nicht schneller als alle 100 ms
	start := time.Now()
	for i := 0; i < 3; i++ {
		request(c, 1, 0, 0, 300, 200)
		e := make([]byte, 4)
		if _, err := io.ReadFull(c, e); err != nil {
			t.Fatal(err)
		}
		if e[0] != 0 || binary.BigEndian.Uint16(e[2:]) != 0 {
			t.Fatalf("keine leere Aktualisierung: %v", e)
		}
	}
	if time.Since(start) < 150*time.Millisecond {
		t.Fatalf("zu schnell: %v", time.Since(start))
	}

	if s.ClientCount() != 1 {
		t.Fatalf("ClientCount %d", s.ClientCount())
	}
	s.DropClients()
	deadline := time.Now().Add(2 * time.Second)
	for s.ClientCount() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if s.ClientCount() != 0 {
		t.Fatal("Clients nicht getrennt")
	}
}

func TestServerClosesOnUnsupportedPixelFormat(t *testing.T) {
	_, host, port := startServer(t, 100, 100, "")
	c := rawClient(t, host, port)
	c.Write([]byte{0, 0, 0, 0, 8, 8, 0, 1, 0, 7, 0, 7, 0, 3, 5, 2, 0, 0, 0, 0})
	if _, err := c.Read(make([]byte, 1)); err == nil {
		t.Fatal("Verbindung hätte geschlossen werden müssen")
	}
}

func TestMain(m *testing.M) {
	log.SetOutput(io.Discard)
	os.Exit(m.Run())
}

func TestUpdateRequestMergeKeepsFullRequest(t *testing.T) {
	full := updateRequest{pending: true, x: 0, y: 0, w: 300, h: 200}
	inc := updateRequest{pending: true, incremental: true, x: 0, y: 0, w: 300, h: 200}
	if m := full.merge(inc); m.incremental || m.w != 300 || m.h != 200 {
		t.Fatalf("%+v", m)
	}
	if m := inc.merge(full); m.incremental {
		t.Fatalf("%+v", m)
	}
	if m := inc.merge(inc); !m.incremental {
		t.Fatalf("%+v", m)
	}
	a := updateRequest{pending: true, x: 0, y: 0, w: 10, h: 10}
	b := updateRequest{pending: true, x: 20, y: 30, w: 10, h: 10}
	if m := a.merge(b); m.x != 0 || m.y != 0 || m.w != 30 || m.h != 40 {
		t.Fatalf("Vereinigung %+v", m)
	}
	if m := (updateRequest{}).merge(b); m != b {
		t.Fatalf("%+v", m)
	}
}

func TestServerFullThenIncrementalStillDeliversImage(t *testing.T) {
	_, host, port := startServer(t, 120, 80, "")
	c := rawClient(t, host, port)
	b := append([]byte(nil), 3, 0, 0, 0, 0, 0, 0, 120, 0, 80)
	b = append(b, 3, 1, 0, 0, 0, 0, 0, 120, 0, 80)
	c.Write(b)
	hdr := make([]byte, 16)
	if _, err := io.ReadFull(c, hdr); err != nil {
		t.Fatal(err)
	}
	if binary.BigEndian.Uint16(hdr[2:]) != 1 {
		t.Fatalf("kein Rechteck: %v", hdr)
	}
}

func TestServerTreats3889As38(t *testing.T) {
	_, host, port := startServer(t, 100, 100, "geheim")
	c, err := net.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	io.ReadFull(c, make([]byte, 12))
	c.Write([]byte("RFB 003.889\n"))
	sec := make([]byte, 2) // 3.8: Liste (Anzahl, Typ); 3.3 hätte u32 gesendet
	if _, err := io.ReadFull(c, sec); err != nil || sec[0] != 1 || sec[1] != secVNC {
		t.Fatalf("%v %v", sec, err)
	}
}

func TestVNCAuthKnownAnswer(t *testing.T) {
	challenge := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	// "password" = 70 61 73 73 77 6f 72 64, jedes Byte bitweise gespiegelt (von Hand).
	key := []byte{0x0e, 0x86, 0xce, 0xce, 0xee, 0xf6, 0x4e, 0x26}
	blk, err := des.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	want := make([]byte, 16)
	blk.Encrypt(want[:8], challenge[:8])
	blk.Encrypt(want[8:], challenge[8:])
	got, _ := vncAuthResponse("password", challenge)
	if !bytes.Equal(got, want) {
		t.Fatalf("%x != %x", got, want)
	}
	if hex.EncodeToString(got) != "b866924125c8eebb9debc1db61c538e2" {
		t.Fatalf("Antwort %x", got)
	}
}
