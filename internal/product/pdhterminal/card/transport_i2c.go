package card

import (
	"context"
	"errors"
	"time"
)

// i2cConn ist die Verbindung zum PN532 (unter Linux ein /dev/i2c-N mit gesetzter Adresse).
type i2cConn interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Close() error
}

// i2cTransport setzt pn532Transport auf I²C um; jede Lesung beginnt mit einem Statusbyte (Bit 0 = bereit).
type i2cTransport struct{ conn i2cConn }

func newI2CTransport(conn i2cConn) pn532Transport { return &i2cTransport{conn: conn} }

func (t *i2cTransport) Name() string { return "I²C" }

func (t *i2cTransport) WriteFrame(_ context.Context, frame []byte) error {
	_, err := t.conn.Write(frame)
	return err
}

func (t *i2cTransport) ReadFrame(ctx context.Context, max int) ([]byte, error) {
	if err := t.waitReady(ctx); err != nil {
		return nil, err
	}
	buf := make([]byte, 1+max)
	n, err := t.conn.Read(buf)
	if err != nil {
		return nil, err
	}
	if n < 1 {
		return nil, errors.New("PN532: Antwort zu kurz")
	}
	return buf[1:n], nil // Statusbyte abschneiden
}

func (t *i2cTransport) Close() error { return t.conn.Close() }

// waitReady fragt das Statusbyte, bis der PN532 „bereit“ meldet oder pn532ReadyTimeout abläuft.
func (t *i2cTransport) waitReady(ctx context.Context) error {
	deadline := time.Now().Add(pn532ReadyTimeout)
	var b [1]byte
	for {
		if _, err := t.conn.Read(b[:]); err != nil {
			return err
		}
		if b[0]&0x01 == 1 {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("PN532: keine Antwort")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pn532ReadyStep):
		}
	}
}
