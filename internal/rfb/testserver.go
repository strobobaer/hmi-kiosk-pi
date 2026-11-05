package rfb

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

const (
	incrementalInterval = 100 * time.Millisecond
	testServerName      = "hmi-kiosk-testvnc"
)

type pixelFormat struct {
	bpp                    int
	bigEndian              bool
	rmax, gmax, bmax       uint32
	rshift, gshift, bshift uint8
}

var serverFormat = pixelFormat{bpp: 32, rmax: 255, gmax: 255, bmax: 255, rshift: 16, gshift: 8, bshift: 0}

func (p pixelFormat) bytes() []byte {
	b := make([]byte, 16)
	b[0] = byte(p.bpp)
	b[1] = 24
	if p.bpp == 16 {
		b[1] = 16
	}
	if p.bigEndian {
		b[2] = 1
	}
	b[3] = 1
	binary.BigEndian.PutUint16(b[4:], uint16(p.rmax))
	binary.BigEndian.PutUint16(b[6:], uint16(p.gmax))
	binary.BigEndian.PutUint16(b[8:], uint16(p.bmax))
	b[10], b[11], b[12] = p.rshift, p.gshift, p.bshift
	return b
}

func parsePixelFormat(b []byte) (pixelFormat, error) {
	p := pixelFormat{
		bpp:       int(b[0]),
		bigEndian: b[2] != 0,
		rmax:      uint32(binary.BigEndian.Uint16(b[4:])),
		gmax:      uint32(binary.BigEndian.Uint16(b[6:])),
		bmax:      uint32(binary.BigEndian.Uint16(b[8:])),
		rshift:    b[10], gshift: b[11], bshift: b[12],
	}
	if b[3] == 0 {
		return p, errors.New("Farbpalette wird nicht unterstützt")
	}
	if p.bpp != 32 && p.bpp != 16 {
		return p, fmt.Errorf("%d Bit pro Pixel werden nicht unterstützt", p.bpp)
	}
	if p.rmax == 0 || p.gmax == 0 || p.bmax == 0 {
		return p, errors.New("ungültiges Pixelformat")
	}
	return p, nil
}

// TestServer ist ein minimaler VNC-Server (RFB 3.8, Raw-Kodierung) mit festem Testbild.
type TestServer struct {
	w, h     int
	password string
	rgb      []byte // w*h*3

	mu       sync.Mutex
	conns    map[net.Conn]struct{}
	pointers []image.Point
}

// NewTestServer erzeugt einen Server; mit leerem Passwort gilt Sicherheitsverfahren "keine".
func NewTestServer(w, h int, password string) *TestServer {
	return &TestServer{w: w, h: h, password: password, conns: map[net.Conn]struct{}{}, rgb: renderTestImage(w, h)}
}

// renderTestImage: dunkler Grund, 2 Pixel weißer Rand, Marker alle 100 Pixel.
func renderTestImage(w, h int) []byte {
	img := make([]byte, w*h*3)
	set := func(x, y int, r, g, b byte) {
		if x < 0 || y < 0 || x >= w || y >= h {
			return
		}
		o := (y*w + x) * 3
		img[o], img[o+1], img[o+2] = r, g, b
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if x < 2 || y < 2 || x >= w-2 || y >= h-2 {
				set(x, y, 255, 255, 255)
			} else {
				set(x, y, 20, 24, 32)
			}
		}
	}
	for gy := 100; gy < h; gy += 100 {
		for gx := 100; gx < w; gx += 100 {
			for dy := -3; dy <= 3; dy++ {
				for dx := -3; dx <= 3; dx++ {
					set(gx+dx, gy+dy, 255, 190, 0)
				}
			}
		}
	}
	return img
}

// Serve nimmt Verbindungen an, bis der Listener geschlossen wird.
func (s *TestServer) Serve(l net.Listener) error {
	for {
		c, err := l.Accept()
		if err != nil {
			return err
		}
		s.mu.Lock()
		s.conns[c] = struct{}{}
		s.mu.Unlock()
		go func() {
			defer func() {
				c.Close()
				s.mu.Lock()
				delete(s.conns, c)
				s.mu.Unlock()
			}()
			if err := s.handle(c); err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				log.Printf("Test-VNC: Verbindung von %s beendet: %v", c.RemoteAddr(), err)
			}
		}()
	}
}

// ClientCount liefert die Zahl aktuell offener Verbindungen.
func (s *TestServer) ClientCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns)
}

// DropClients trennt alle Verbindungen.
func (s *TestServer) DropClients() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range s.conns {
		c.Close()
	}
}

// Pointers liefert eine Kopie der Zeigerpositionen, bei denen eine Taste gedrückt war.
func (s *TestServer) Pointers() []image.Point {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]image.Point(nil), s.pointers...)
}

func (s *TestServer) handshake(c net.Conn) error {
	c.SetDeadline(time.Now().Add(10 * time.Second))
	defer c.SetDeadline(time.Time{})
	if _, err := c.Write([]byte("RFB 003.008\n")); err != nil {
		return err
	}
	var ver [12]byte
	if _, err := io.ReadFull(c, ver[:]); err != nil {
		return err
	}
	if !bytes.HasPrefix(ver[:], []byte("RFB 003.")) {
		return errors.New("kein RFB-Client")
	}
	minor := 3
	switch string(ver[8:11]) {
	case "008":
		minor = 8
	case "007":
		minor = 7
	}
	sec := byte(secNone)
	if s.password != "" {
		sec = secVNC
	}
	if minor == 3 {
		if err := binary.Write(c, binary.BigEndian, uint32(sec)); err != nil {
			return err
		}
	} else {
		if _, err := c.Write([]byte{1, sec}); err != nil {
			return err
		}
		var pick [1]byte
		if _, err := io.ReadFull(c, pick[:]); err != nil {
			return err
		}
		if pick[0] != sec {
			return errors.New("unerwartetes Sicherheitsverfahren")
		}
	}
	if sec == secVNC {
		challenge := make([]byte, 16)
		if _, err := rand.Read(challenge); err != nil {
			return err
		}
		if _, err := c.Write(challenge); err != nil {
			return err
		}
		resp := make([]byte, 16)
		if _, err := io.ReadFull(c, resp); err != nil {
			return err
		}
		want, _ := vncAuthResponse(s.password, challenge)
		ok := bytes.Equal(resp, want)
		var res uint32
		if !ok {
			res = 1
		}
		if err := binary.Write(c, binary.BigEndian, res); err != nil {
			return err
		}
		if !ok {
			if minor >= 8 {
				msg := "Passwort falsch"
				binary.Write(c, binary.BigEndian, uint32(len(msg)))
				c.Write([]byte(msg))
			}
			return errors.New("Anmeldung mit falschem Passwort")
		}
	} else if minor >= 8 {
		if err := binary.Write(c, binary.BigEndian, uint32(0)); err != nil {
			return err
		}
	}
	var shared [1]byte
	if _, err := io.ReadFull(c, shared[:]); err != nil {
		return err
	}
	init := make([]byte, 0, 24+len(testServerName))
	init = binary.BigEndian.AppendUint16(init, uint16(s.w))
	init = binary.BigEndian.AppendUint16(init, uint16(s.h))
	init = append(init, serverFormat.bytes()...)
	init = binary.BigEndian.AppendUint32(init, uint32(len(testServerName)))
	init = append(init, testServerName...)
	_, err := c.Write(init)
	return err
}

type updateRequest struct {
	pending     bool
	incremental bool
	x, y, w, h  int
}

func (s *TestServer) handle(c net.Conn) error {
	if err := s.handshake(c); err != nil {
		return err
	}

	var mu sync.Mutex // schützt pf und req
	pf := serverFormat
	var req updateRequest
	notify := make(chan struct{}, 1)
	done := make(chan struct{})
	defer close(done)

	// Nur diese Goroutine schreibt nach dem Handshake.
	go func() {
		var last time.Time
		for {
			select {
			case <-done:
				return
			case <-notify:
			}
			mu.Lock()
			r, f := req, pf
			req.pending = false
			mu.Unlock()
			if !r.pending {
				continue
			}
			var msg []byte
			if r.incremental {
				if wait := time.Until(last.Add(incrementalInterval)); wait > 0 {
					select {
					case <-done:
						return
					case <-time.After(wait):
					}
				}
				msg = []byte{0, 0, 0, 0}
			} else {
				msg = s.fullUpdate(r, f)
			}
			last = time.Now()
			if _, err := c.Write(msg); err != nil {
				c.Close()
				return
			}
		}
	}()

	var hdr [1]byte
	for {
		if _, err := io.ReadFull(c, hdr[:]); err != nil {
			return err
		}
		switch hdr[0] {
		case 0: // SetPixelFormat
			var b [19]byte
			if _, err := io.ReadFull(c, b[:]); err != nil {
				return err
			}
			p, err := parsePixelFormat(b[3:])
			if err != nil {
				return fmt.Errorf("Pixelformat des Clients nicht möglich: %w", err)
			}
			mu.Lock()
			pf = p
			mu.Unlock()
		case 2: // SetEncodings
			var b [3]byte
			if _, err := io.ReadFull(c, b[:]); err != nil {
				return err
			}
			if _, err := io.CopyN(io.Discard, c, int64(binary.BigEndian.Uint16(b[1:]))*4); err != nil {
				return err
			}
		case 3: // FramebufferUpdateRequest
			var b [9]byte
			if _, err := io.ReadFull(c, b[:]); err != nil {
				return err
			}
			mu.Lock()
			req = updateRequest{
				pending: true, incremental: b[0] != 0,
				x: int(binary.BigEndian.Uint16(b[1:])), y: int(binary.BigEndian.Uint16(b[3:])),
				w: int(binary.BigEndian.Uint16(b[5:])), h: int(binary.BigEndian.Uint16(b[7:])),
			}
			mu.Unlock()
			select {
			case notify <- struct{}{}:
			default:
			}
		case 4: // KeyEvent
			if _, err := io.CopyN(io.Discard, c, 7); err != nil {
				return err
			}
		case 5: // PointerEvent
			var b [5]byte
			if _, err := io.ReadFull(c, b[:]); err != nil {
				return err
			}
			if b[0] != 0 {
				s.mu.Lock()
				s.pointers = append(s.pointers, image.Pt(int(binary.BigEndian.Uint16(b[1:])), int(binary.BigEndian.Uint16(b[3:]))))
				s.mu.Unlock()
			}
		case 6: // ClientCutText
			var b [7]byte
			if _, err := io.ReadFull(c, b[:]); err != nil {
				return err
			}
			if _, err := io.CopyN(io.Discard, c, int64(binary.BigEndian.Uint32(b[3:]))); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unbekannte Client-Nachricht %d", hdr[0])
		}
	}
}

// fullUpdate kodiert den angeforderten Bereich als ein Raw-Rechteck.
func (s *TestServer) fullUpdate(r updateRequest, p pixelFormat) []byte {
	x0, y0 := min(r.x, s.w), min(r.y, s.h)
	x1, y1 := min(r.x+r.w, s.w), min(r.y+r.h, s.h)
	w, h := x1-x0, y1-y0
	if w <= 0 || h <= 0 {
		return []byte{0, 0, 0, 0}
	}
	bytesPP := p.bpp / 8
	msg := make([]byte, 4, 16+w*h*bytesPP)
	binary.BigEndian.PutUint16(msg[2:], 1)
	msg = binary.BigEndian.AppendUint16(msg, uint16(x0))
	msg = binary.BigEndian.AppendUint16(msg, uint16(y0))
	msg = binary.BigEndian.AppendUint16(msg, uint16(w))
	msg = binary.BigEndian.AppendUint16(msg, uint16(h))
	msg = binary.BigEndian.AppendUint32(msg, 0) // Raw
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			o := (y*s.w + x) * 3
			v := (uint32(s.rgb[o])*p.rmax+127)/255<<p.rshift |
				(uint32(s.rgb[o+1])*p.gmax+127)/255<<p.gshift |
				(uint32(s.rgb[o+2])*p.bmax+127)/255<<p.bshift
			switch {
			case bytesPP == 4 && p.bigEndian:
				msg = binary.BigEndian.AppendUint32(msg, v)
			case bytesPP == 4:
				msg = binary.LittleEndian.AppendUint32(msg, v)
			case p.bigEndian:
				msg = binary.BigEndian.AppendUint16(msg, uint16(v))
			default:
				msg = binary.LittleEndian.AppendUint16(msg, uint16(v))
			}
		}
	}
	return msg
}
