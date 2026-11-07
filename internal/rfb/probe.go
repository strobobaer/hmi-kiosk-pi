// Package rfb prüft VNC-Verbindungen und stellt einen kleinen Test-VNC-Server bereit.
package rfb

import (
	"context"
	"crypto/des"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/bits"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	secNone = 1
	secVNC  = 2

	defaultTimeout = 8 * time.Second
	maxNameLen     = 4096
)

// ProbeResult beschreibt das Ergebnis von Probe. Err ist für die Anzeige bestimmt.
type ProbeResult struct {
	Reachable, AuthOK bool
	AuthRejected      bool // der Server hat die Anmeldung abgelehnt
	Width, Height     int
	Name, Err         string
}

func withDefaultTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, defaultTimeout)
}

// TCPReachable meldet, ob ein TCP-Verbindungsaufbau gelingt.
func TCPReachable(ctx context.Context, host string, port int) bool {
	ctx, cancel := withDefaultTimeout(ctx)
	defer cancel()
	var d net.Dialer
	c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// Probe verbindet sich, durchläuft den RFB-Handshake bis ServerInit und trennt dann,
// ohne Eingaben zu senden. Ohne Frist im ctx gelten 8 s.
func Probe(ctx context.Context, host string, port int, password string) ProbeResult {
	ctx, cancel := withDefaultTimeout(ctx)
	defer cancel()

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return ProbeResult{Err: "Keine Verbindung: " + dialReason(ctx, err)}
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()

	res := ProbeResult{Reachable: true}
	if err := probeHandshake(conn, password, &res); err != nil {
		res.AuthRejected = errors.Is(err, errAuthRejected)
		if ctx.Err() != nil || isTimeout(err) {
			res.Err = "Zeitüberschreitung beim VNC-Handshake"
		} else {
			res.Err = err.Error()
		}
	}
	return res
}

func dialReason(ctx context.Context, err error) string {
	if ctx.Err() != nil || isTimeout(err) {
		return "Zeitüberschreitung"
	}
	return "Gegenstelle nicht erreichbar oder Port geschlossen"
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

var errAuthRejected = errors.New("Anmeldung abgelehnt: Passwort falsch oder Zugriff nicht erlaubt")

var errNotVNC = errors.New("Das Gegenüber ist kein VNC-Server")

var errUnsupportedSec = errors.New("Kein unterstütztes VNC-Sicherheitsverfahren (nur ohne Anmeldung und VNC-Passwort)")

func probeHandshake(rw io.ReadWriter, password string, res *ProbeResult) error {
	var ver [12]byte
	if _, err := io.ReadFull(rw, ver[:]); err != nil {
		return errNotVNC
	}
	if string(ver[:4]) != "RFB " || ver[7] != '.' || ver[11] != '\n' {
		return errNotVNC
	}
	major, err1 := strconv.Atoi(string(ver[4:7]))
	minor, err2 := strconv.Atoi(string(ver[8:11]))
	if err1 != nil || err2 != nil {
		return errNotVNC
	}
	if major < 3 {
		return errors.New("VNC-Version wird nicht unterstützt")
	}
	switch {
	case major > 3 || minor >= 8:
		minor = 8
	case minor >= 7:
		minor = 7
	default:
		minor = 3
	}
	if _, err := fmt.Fprintf(rw, "RFB 003.%03d\n", minor); err != nil {
		return err
	}

	var sec uint32
	if minor == 3 {
		if err := binary.Read(rw, binary.BigEndian, &sec); err != nil {
			return err
		}
		if sec == 0 {
			return rejected(rw)
		}
	} else {
		var n [1]byte
		if _, err := io.ReadFull(rw, n[:]); err != nil {
			return err
		}
		if n[0] == 0 {
			return rejected(rw)
		}
		types := make([]byte, n[0])
		if _, err := io.ReadFull(rw, types); err != nil {
			return err
		}
		has := func(t byte) bool {
			for _, x := range types {
				if x == t {
					return true
				}
			}
			return false
		}
		switch {
		case password != "" && has(secVNC):
			sec = secVNC
		case has(secNone):
			sec = secNone
		case has(secVNC):
			sec = secVNC
		default:
			return errUnsupportedSec
		}
		if _, err := rw.Write([]byte{byte(sec)}); err != nil {
			return err
		}
	}

	switch sec {
	case secNone:
		if minor >= 8 {
			if err := readSecurityResult(rw); err != nil {
				return err
			}
		}
	case secVNC:
		var challenge [16]byte
		if _, err := io.ReadFull(rw, challenge[:]); err != nil {
			return err
		}
		resp, err := vncAuthResponse(password, challenge[:])
		if err != nil {
			return err
		}
		if _, err := rw.Write(resp); err != nil {
			return err
		}
		if err := readSecurityResult(rw); err != nil {
			return err
		}
	default:
		return errUnsupportedSec
	}
	res.AuthOK = true

	// ClientInit mit shared=1, damit andere Betrachter nicht getrennt werden.
	if _, err := rw.Write([]byte{1}); err != nil {
		return err
	}
	var si [24]byte
	if _, err := io.ReadFull(rw, si[:]); err != nil {
		return err
	}
	res.Width = int(binary.BigEndian.Uint16(si[0:2]))
	res.Height = int(binary.BigEndian.Uint16(si[2:4]))
	nameLen := binary.BigEndian.Uint32(si[20:24])
	if nameLen > maxNameLen {
		return errors.New("Ungültige VNC-Antwort (Name zu lang)")
	}
	name := make([]byte, nameLen)
	if _, err := io.ReadFull(rw, name); err != nil {
		return err
	}
	res.Name = string(name)
	return nil
}

// rejected liest die Begründung, die der Server laut RFB nach „keine Verfahren“ schickt.
func rejected(r io.Reader) error {
	const base = "Der VNC-Server lehnt die Verbindung ab"
	var n uint32
	if err := binary.Read(r, binary.BigEndian, &n); err != nil || n == 0 {
		return errors.New(base)
	}
	if n > 512 {
		n = 512
	}
	buf := make([]byte, n)
	got, _ := io.ReadFull(r, buf)
	reason := strings.TrimSpace(strings.Map(func(c rune) rune {
		if c < 0x20 || c == 0x7f {
			return ' '
		}
		return c
	}, string(buf[:got])))
	if reason == "" {
		return errors.New(base)
	}
	return errors.New(base + ": „" + reason + "“")
}

func readSecurityResult(r io.Reader) error {
	var v uint32
	if err := binary.Read(r, binary.BigEndian, &v); err != nil {
		return err
	}
	if v != 0 {
		return errAuthRejected
	}
	return nil
}

// vncAuthResponse verschlüsselt die 16-Byte-Challenge mit DES. Der Schlüssel besteht
// aus den ersten 8 Passwort-Bytes (UTF-8, mit Nullen aufgefüllt), je Byte bitweise gespiegelt.
func vncAuthResponse(password string, challenge []byte) ([]byte, error) {
	if len(challenge) != 16 {
		return nil, errors.New("Ungültige VNC-Challenge")
	}
	var key [8]byte
	copy(key[:], password)
	for i := range key {
		key[i] = bits.Reverse8(key[i])
	}
	c, err := des.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	out := make([]byte, 16)
	c.Encrypt(out[:8], challenge[:8])
	c.Encrypt(out[8:], challenge[8:])
	return out, nil
}
