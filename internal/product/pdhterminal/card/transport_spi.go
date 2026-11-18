package card

import (
	"context"
	"errors"
	"time"
)

// spiBus ist ein vollduplexer SPI-Zugang; rx ist genauso lang wie tx.
type spiBus interface {
	Transfer(tx []byte) ([]byte, error)
	Close() error
}

// SPI-Kommandobytes des PN532 (UM0701-02, 6.2.5).
const (
	spiDataWrite  = 0x01
	spiStatusRead = 0x02
	spiDataRead   = 0x03
)

// bitRev dreht die Bitreihenfolge eines Bytes; der PN532 erwartet LSB zuerst, spidev sendet MSB zuerst.
var bitRev = func() (t [256]byte) {
	for i := range t {
		var r byte
		for b := 0; b < 8; b++ {
			if i&(1<<b) != 0 {
				r |= 0x80 >> b
			}
		}
		t[i] = r
	}
	return
}()

// rev liefert eine Kopie von b mit gedrehten Bits in jedem Byte.
func rev(b []byte) []byte {
	out := make([]byte, len(b))
	for i, v := range b {
		out[i] = bitRev[v]
	}
	return out
}

// spiTransport setzt pn532Transport auf SPI um.
type spiTransport struct{ bus spiBus }

func newSPITransport(bus spiBus) pn532Transport { return &spiTransport{bus: bus} }

func (t *spiTransport) Name() string { return "SPI" }

func (t *spiTransport) WriteFrame(_ context.Context, frame []byte) error {
	_, err := t.bus.Transfer(rev(append([]byte{spiDataWrite}, frame...)))
	return err
}

func (t *spiTransport) ReadFrame(ctx context.Context, max int) ([]byte, error) {
	if err := t.waitReady(ctx); err != nil {
		return nil, err
	}
	tx := make([]byte, 1+max)
	tx[0] = spiDataRead
	rx, err := t.bus.Transfer(rev(tx))
	if err != nil {
		return nil, err
	}
	if len(rx) < 1 {
		return nil, errors.New("PN532: Antwort zu kurz")
	}
	return rev(rx[1:]), nil // Dummybyte abschneiden
}

func (t *spiTransport) Close() error { return t.bus.Close() }

// waitReady fragt das Statusbyte, bis der PN532 „bereit“ meldet oder pn532ReadyTimeout abläuft.
func (t *spiTransport) waitReady(ctx context.Context) error {
	deadline := time.Now().Add(pn532ReadyTimeout)
	for {
		rx, err := t.bus.Transfer(rev([]byte{spiStatusRead, 0x00}))
		if err != nil {
			return err
		}
		if len(rx) >= 2 && bitRev[rx[1]]&0x01 == 1 {
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
