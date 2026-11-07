package helperapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

// Befehlsnamen der SD-Sicherung (Protokoll zum Helfer).
const (
	CmdBackupStream = "backup-stream"
	CmdBackupUSB    = "backup-usb"
	CmdBackupStatus = "backup-status"
	CmdBackupCancel = "backup-cancel"
	CmdBackupInfo   = "backup-info"
)

// MaxBackupNameLen ist die längste Terminalbezeichnung im Dateinamen.
const MaxBackupNameLen = 40

const (
	// Der Helfer baut vor der Kopfzeile die kleine Datenpartition (mkfs aus /data).
	backupHeaderTimeout = 5 * time.Minute
	maxHeaderBytes      = 4096
)

// BackupStatus ist der Stand der SD-Sicherung im Helfer. Finished heißt: die letzte
// Sicherung ist vollständig geschrieben (Download: ganz gesendet).
type BackupStatus struct {
	Running    bool    `json:"running"`
	Mode       string  `json:"mode,omitempty"` // "download" oder "usb"
	Target     string  `json:"target,omitempty"`
	ReadBytes  int64   `json:"read_bytes"`
	TotalBytes int64   `json:"total_bytes"`
	ElapsedSec float64 `json:"elapsed_sec"`
	Finished   bool    `json:"finished,omitempty"`
	File       string  `json:"file,omitempty"`
	Error      string  `json:"error,omitempty"`
}

var germanLetters = strings.NewReplacer("ä", "ae", "ö", "oe", "ü", "ue", "Ä", "Ae", "Ö", "Oe", "Ü", "Ue", "ß", "ss")

// SanitizeBackupName reduziert einen Terminalnamen auf [A-Za-z0-9_-], höchstens
// MaxBackupNameLen Zeichen. Umlaute werden umschrieben, alles andere wird zu "-"
// (mehrfach zusammengefasst). Bleibt nichts übrig, ist das Ergebnis "terminal".
func SanitizeBackupName(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range germanLetters.Replace(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
			dash = false
		default: // auch das echte "-" und alles Unzulässige
			if !dash {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > MaxBackupNameLen {
		s = strings.Trim(s[:MaxBackupNameLen], "-")
	}
	if s == "" {
		return "terminal"
	}
	return s
}

// BackupFileName ist der Name der Sicherungsdatei: hmikiosk-<Name>-JJJJ-MM-TT-hhmm.img.gz.
func BackupFileName(terminal string, t time.Time) string {
	return "hmikiosk-" + SanitizeBackupName(terminal) + "-" + t.Format("2006-01-02-1504") + ".img.gz"
}

// BackupUSB startet die Sicherung auf einen USB-Stick; der Helfer antwortet sofort.
func (c *Client) BackupUSB(ctx context.Context, terminalName string) error {
	return c.Call(ctx, CmdBackupUSB, SanitizeBackupName(terminalName))
}

// BackupCancel bricht eine laufende Sicherung ab.
func (c *Client) BackupCancel(ctx context.Context) error {
	return c.Call(ctx, CmdBackupCancel, "")
}

// BackupStatus fragt den Stand ab.
func (c *Client) BackupStatus(ctx context.Context) (BackupStatus, error) {
	raw, err := c.callData(ctx, CmdBackupStatus)
	if err != nil {
		return BackupStatus{}, err
	}
	var st BackupStatus
	if err := json.Unmarshal(raw, &st); err != nil {
		return BackupStatus{}, errors.New("Ungültige Antwort des Helfers")
	}
	return st, nil
}

// BackupStream öffnet den Strom des gzip-Abbilds der SD-Karte. size ist die
// Rohgröße. Der Aufrufer muss den Strom schließen; ein Abbruch von ctx beendet ihn.
// Ein sauberes Ende des Stroms beweist nicht, dass das Abbild vollständig ist:
// danach BackupStatus prüfen (Finished).
func (c *Client) BackupStream(ctx context.Context) (size int64, body io.ReadCloser, err error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.socket)
	if err != nil {
		return 0, nil, fmt.Errorf("Der Helfer ist nicht erreichbar (%v)", err)
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	fail := func(e error) (int64, io.ReadCloser, error) {
		stop()
		conn.Close()
		return 0, nil, e
	}
	conn.SetDeadline(time.Now().Add(backupHeaderTimeout))
	req, _ := json.Marshal(map[string]string{"cmd": CmdBackupStream, "arg": ""})
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return fail(c.ioErr(ctx, err))
	}
	br := bufio.NewReaderSize(conn, 64<<10)
	line, err := readHeaderLine(br)
	if err != nil {
		return fail(c.ioErr(ctx, err))
	}
	var head struct {
		OK    bool   `json:"ok"`
		Size  int64  `json:"size"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(line, &head); err != nil {
		return fail(errors.New("Ungültige Antwort des Helfers"))
	}
	if !head.OK {
		if head.Error == "" {
			return fail(errors.New("Ungültige Antwort des Helfers"))
		}
		return fail(errors.New(head.Error))
	}
	conn.SetDeadline(time.Time{})
	return head.Size, &streamBody{r: br, conn: conn, stop: stop}, nil
}

func readHeaderLine(br *bufio.Reader) ([]byte, error) {
	var line []byte
	for len(line) <= maxHeaderBytes {
		b, err := br.ReadByte()
		if err != nil {
			return nil, err
		}
		if b == '\n' {
			return line, nil
		}
		line = append(line, b)
	}
	return nil, errors.New("Kopfzeile zu lang")
}

type streamBody struct {
	r    io.Reader
	conn net.Conn
	stop func() bool
}

func (s *streamBody) Read(p []byte) (int, error) { return s.r.Read(p) }

func (s *streamBody) Close() error {
	s.stop()
	return s.conn.Close()
}

// BackupInfo sagt vorab, ob und wie groß eine Sicherung würde. Problem ist der deutsche
// Grund, warum jetzt keine möglich ist (kein Abbild-System, zu viele Daten, zu wenig Speicher).
type BackupInfo struct {
	RawBytes int64  `json:"raw_bytes,omitempty"` // Rohgröße des Abbilds: Ende von Partition 3
	Problem  string `json:"problem,omitempty"`
}

// BackupInfo fragt die erwartete Rohgröße und etwaige Hindernisse ab.
func (c *Client) BackupInfo(ctx context.Context) (BackupInfo, error) {
	raw, err := c.callData(ctx, CmdBackupInfo)
	if err != nil {
		return BackupInfo{}, err
	}
	var info BackupInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return BackupInfo{}, errors.New("Ungültige Antwort des Helfers")
	}
	return info, nil
}
