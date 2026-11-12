package card

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// PN532-Befehle und Zeiten (NXP UM0701-02).
const (
	cmdGetFirmwareVersion = 0x02
	cmdSAMConfiguration   = 0x14
	cmdRFConfiguration    = 0x32
	cmdInListPassive      = 0x4A

	pn532PollInterval = 250 * time.Millisecond
	pn532ReadyTimeout = 100 * time.Millisecond
	pn532ReadyStep    = 5 * time.Millisecond
	pn532RespBuf      = 64
)

var pn532Ack = []byte{0x00, 0x00, 0xFF, 0x00, 0xFF, 0x00}

// i2cConn ist der Transport zum PN532 (unter Linux ein /dev/i2c-N mit gesetzter Adresse).
type i2cConn interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Close() error
}

// encodeFrame baut den Befehlsrahmen 00 00 FF LEN LCS D4 CMD DATA… DCS 00.
func encodeFrame(cmd byte, data []byte) []byte {
	n := 2 + len(data)
	sum := 0xD4 + int(cmd)
	for _, b := range data {
		sum += int(b)
	}
	out := []byte{0x00, 0x00, 0xFF, byte(n), byte(0x100-n) & 0xFF, 0xD4, cmd}
	out = append(out, data...)
	return append(out, byte(0x100-sum)&0xFF, 0x00)
}

// decodeFrame prüft eine Antwort (buf beginnt mit dem I²C-Statusbyte) und liefert deren Daten.
func decodeFrame(buf []byte, cmd byte) ([]byte, error) {
	if len(buf) < 8 {
		return nil, errors.New("PN532: Antwort zu kurz")
	}
	if buf[0]&0x01 == 0 {
		return nil, errors.New("PN532: Antwort nicht bereit")
	}
	if !bytes.Equal(buf[1:4], []byte{0x00, 0x00, 0xFF}) {
		return nil, errors.New("PN532: ungültiger Rahmenanfang")
	}
	n := int(buf[4])
	if (n+int(buf[5]))&0xFF != 0 {
		return nil, errors.New("PN532: Längenprüfsumme falsch")
	}
	if n < 2 || len(buf) < 6+n+2 {
		return nil, errors.New("PN532: Rahmen unvollständig")
	}
	body := buf[6 : 6+n]
	if body[0] != 0xD5 || body[1] != cmd+1 {
		return nil, fmt.Errorf("PN532: unerwartete Antwort %02X %02X", body[0], body[1])
	}
	sum := int(buf[6+n])
	for _, b := range body {
		sum += int(b)
	}
	if sum&0xFF != 0 {
		return nil, errors.New("PN532: Datenprüfsumme falsch")
	}
	return body[2:], nil
}

// pn532 liest UIDs über einen i2cConn.
type pn532 struct {
	conn   i2cConn
	format string

	mu       sync.Mutex // eine Befehlsfolge zur Zeit (Poll und Probe)
	interval time.Duration
}

func newPN532(conn i2cConn, format string) *pn532 {
	return &pn532{conn: conn, format: format, interval: pn532PollInterval}
}

func (p *pn532) waitReady(ctx context.Context) error {
	deadline := time.Now().Add(pn532ReadyTimeout)
	var b [1]byte
	for {
		if _, err := p.conn.Read(b[:]); err != nil {
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

// exchange sendet einen Befehl, prüft das ACK und liefert die Antwortdaten.
func (p *pn532) exchange(ctx context.Context, cmd byte, data []byte) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, err := p.conn.Write(encodeFrame(cmd, data)); err != nil {
		return nil, err
	}
	if err := p.waitReady(ctx); err != nil {
		return nil, err
	}
	ack := make([]byte, 1+len(pn532Ack))
	if _, err := p.conn.Read(ack); err != nil {
		return nil, err
	}
	if !bytes.Equal(ack[1:], pn532Ack) {
		return nil, errors.New("PN532: ACK fehlt")
	}
	if err := p.waitReady(ctx); err != nil {
		return nil, err
	}
	resp := make([]byte, pn532RespBuf)
	if _, err := p.conn.Read(resp); err != nil {
		return nil, err
	}
	return decodeFrame(resp, cmd)
}

// poll fragt einmal nach einer Karte; "" heißt: keine Karte (oder leere UID).
func (p *pn532) poll(ctx context.Context) (string, error) {
	d, err := p.exchange(ctx, cmdInListPassive, []byte{0x01, 0x00})
	if err != nil {
		return "", err
	}
	// NbTg Tg SENS_RES(2) SEL_RES NFCIDLen NFCID…
	if len(d) < 1 || d[0] == 0 {
		return "", nil
	}
	if len(d) < 6 {
		return "", errors.New("PN532: Zielantwort zu kurz")
	}
	n := int(d[5])
	if n == 0 {
		return "", nil // FormatUID(nil, "dec") wäre "0"
	}
	if len(d) < 6+n {
		return "", errors.New("PN532: UID unvollständig")
	}
	return FormatUID(d[6:6+n], p.format), nil
}

// Probe liest die Firmwareversion.
func (p *pn532) Probe(ctx context.Context) (string, error) {
	d, err := p.exchange(ctx, cmdGetFirmwareVersion, nil)
	if err != nil {
		return "", err
	}
	if len(d) < 4 {
		return "", errors.New("PN532: Versionsantwort zu kurz")
	}
	return fmt.Sprintf("PN532 v%d.%d", d[1], d[2]), nil
}

// Close schließt den Transport.
func (p *pn532) Close() error { return p.conn.Close() }

// Run richtet den Leser ein und fragt zyklisch nach Karten, bis ctx endet oder ein Fehler auftritt.
func (p *pn532) Run(ctx context.Context, emit func(string)) error {
	defer p.conn.Close()
	if _, err := p.exchange(ctx, cmdSAMConfiguration, []byte{0x01, 0x14, 0x01}); err != nil {
		return err
	}
	// Wiederholungen begrenzen, damit die Abfrage ohne Karte schnell zurückkehrt.
	if _, err := p.exchange(ctx, cmdRFConfiguration, []byte{0x05, 0xFF, 0x01, 0x02}); err != nil {
		return err
	}
	t := time.NewTicker(p.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		uid, err := p.poll(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if uid != "" {
			emit(uid)
		}
	}
}
