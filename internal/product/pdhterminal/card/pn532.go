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

// pn532Transport bewegt Rahmen zum und vom PN532; das Protokoll kennt den Bus nicht.
type pn532Transport interface {
	Name() string // "I²C", "SPI", "UART"
	WriteFrame(ctx context.Context, frame []byte) error
	// ReadFrame wartet höchstens pn532ReadyTimeout auf „bereit“ und liefert bis zu max Bytes ab dem
	// Rahmenanfang (00 00 FF …), ohne Statusbyte.
	ReadFrame(ctx context.Context, max int) ([]byte, error)
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

// decodeFrame prüft eine Antwort (frame beginnt mit 00 00 FF, ohne Statusbyte) und liefert deren Daten.
func decodeFrame(buf []byte, cmd byte) ([]byte, error) {
	if len(buf) < 7 {
		return nil, errors.New("PN532: Antwort zu kurz")
	}
	if !bytes.Equal(buf[0:3], []byte{0x00, 0x00, 0xFF}) {
		return nil, errors.New("PN532: ungültiger Rahmenanfang")
	}
	n := int(buf[3])
	if (n+int(buf[4]))&0xFF != 0 {
		return nil, errors.New("PN532: Längenprüfsumme falsch")
	}
	if n < 2 || len(buf) < 5+n+2 {
		return nil, errors.New("PN532: Rahmen unvollständig")
	}
	body := buf[5 : 5+n]
	if body[0] != 0xD5 || body[1] != cmd+1 {
		return nil, fmt.Errorf("PN532: unerwartete Antwort %02X %02X", body[0], body[1])
	}
	sum := int(buf[5+n])
	for _, b := range body {
		sum += int(b)
	}
	if sum&0xFF != 0 {
		return nil, errors.New("PN532: Datenprüfsumme falsch")
	}
	return body[2:], nil
}

// pn532 liest UIDs über einen pn532Transport.
type pn532 struct {
	t      pn532Transport
	format string

	mu       sync.Mutex // eine Befehlsfolge zur Zeit (Poll und Probe)
	interval time.Duration
}

func newPN532(t pn532Transport, format string) *pn532 {
	return &pn532{t: t, format: format, interval: pn532PollInterval}
}

// exchange sendet einen Befehl, prüft das ACK und liefert die Antwortdaten.
func (p *pn532) exchange(ctx context.Context, cmd byte, data []byte) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.t.WriteFrame(ctx, encodeFrame(cmd, data)); err != nil {
		return nil, err
	}
	ack, err := p.t.ReadFrame(ctx, len(pn532Ack))
	if err != nil {
		return nil, err
	}
	if !bytes.HasPrefix(ack, pn532Ack) {
		return nil, errors.New("PN532: ACK fehlt")
	}
	resp, err := p.t.ReadFrame(ctx, pn532RespBuf)
	if err != nil {
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
	return fmt.Sprintf("PN532 über %s, Firmware %d.%d", p.t.Name(), d[1], d[2]), nil
}

// Close schließt den Transport.
func (p *pn532) Close() error { return p.t.Close() }

// Run richtet den Leser ein und fragt zyklisch nach Karten, bis ctx endet oder ein Fehler auftritt.
func (p *pn532) Run(ctx context.Context, emit func(string)) error {
	defer p.t.Close()
	if _, err := p.exchange(ctx, cmdSAMConfiguration, []byte{0x01, 0x14, 0x01}); err != nil {
		return err
	}
	// Wiederholungen begrenzen, damit die Abfrage ohne Karte schnell zurückkehrt.
	if _, err := p.exchange(ctx, cmdRFConfiguration, []byte{0x05, 0xFF, 0x01, 0x02}); err != nil {
		return err
	}
	var seen presence
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
		if seen.step(uid) {
			emit(uid)
		}
	}
}

// presenceGoneAfter ist die Zahl leerer Abfragen in Folge (bei 250 ms etwa 500 ms), nach der
// eine Karte als entfernt gilt; einzelne Aussetzer des Lesers erzeugen so keine zweite Meldung.
const presenceGoneAfter = 2

// presence merkt die zuletzt gesehene UID: Eine aufliegende Karte wird einmal gemeldet,
// nicht bei jeder Abfrage (sonst hielte sie die Kartensitzung ewig am Leben).
type presence struct {
	uid   string
	empty int
}

// step wertet eine Abfrage aus und sagt, ob die UID gemeldet werden soll
// (keine Karte → Karte, oder eine andere UID).
func (p *presence) step(uid string) bool {
	if uid == "" {
		if p.uid != "" {
			p.empty++
			if p.empty >= presenceGoneAfter {
				p.uid, p.empty = "", 0
			}
		}
		return false
	}
	p.empty = 0
	if uid == p.uid {
		return false
	}
	p.uid = uid
	return true
}
