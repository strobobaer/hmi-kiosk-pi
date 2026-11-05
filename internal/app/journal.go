package app

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// MaxLogBytes begrenzt alle Protokolldateien zusammen.
	MaxLogBytes int64 = 50 << 20

	logFileName   = "kiosk.log"
	logFileCount  = 5 // kiosk.log und kiosk.log.1 bis .4
	journalBuffer = 1024
	maxLineBytes  = 64 << 10
)

// SessionRecord ist eine Zeile des Sitzungsprotokolls.
type SessionRecord struct {
	Time     time.Time
	Terminal string
	HMI      string
	Mode     string
	Duration time.Duration
	Reason   string
}

// Journal schreibt das Dienst- und Sitzungsprotokoll nach stderr und in rotierende
// Dateien mit festen Namen. Eine eigene Goroutine schreibt: Eine volle oder
// hängende Platte kostet nur Zeilen und eine Warnung, sie hält nie den Aufrufer
// auf, auch nicht die Zustands-Goroutine der Sitzung.
type Journal struct {
	dir        string
	fileLimit  int64
	stderr     io.Writer
	retryEvery time.Duration
	closeWait  time.Duration

	lines  chan []byte
	stop   chan struct{}
	done   chan struct{}
	closed atomic.Bool
	once   sync.Once

	dropped atomic.Int64
	mu      sync.Mutex
	ioErr   string

	// Ab hier gehört alles der schreibenden Goroutine.
	f        *os.File
	size     int64
	nextOpen time.Time
}

// NewJournal öffnet das Protokoll in dir. Auch bei einem Fehler ist das Ergebnis
// benutzbar: Zeilen gehen dann nach stderr, und das Öffnen wird später wiederholt.
func NewJournal(dir string, maxBytes int64) (*Journal, error) {
	return newJournal(dir, maxBytes, os.Stderr)
}

func newJournal(dir string, maxBytes int64, stderr io.Writer) (*Journal, error) {
	j := &Journal{
		dir:        dir,
		fileLimit:  max(maxBytes/logFileCount, 1),
		stderr:     stderr,
		retryEvery: 30 * time.Second,
		closeWait:  2 * time.Second,
		lines:      make(chan []byte, journalBuffer),
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
	}
	err := j.open()
	if err != nil {
		j.fileFailed(err)
	}
	go j.run()
	return j, err
}

// Write nimmt eine Zeile entgegen und blockiert nie. Es meldet immer Erfolg, damit
// ein Fehler im Protokoll nirgends zu einem Fehler im Dienst wird.
func (j *Journal) Write(p []byte) (int, error) {
	if j.closed.Load() {
		_, _ = j.stderr.Write(p)
		return len(p), nil
	}
	line := append([]byte(nil), p...)
	select {
	case j.lines <- line:
	default:
		j.dropped.Add(1)
	}
	return len(p), nil
}

// Session schreibt eine Zeile des Sitzungsprotokolls. Namen stehen in Anführungszeichen,
// damit ein Zeilenumbruch darin keine Zeile vortäuschen kann.
func (j *Journal) Session(rec SessionRecord) {
	at := rec.Time
	if at.IsZero() {
		at = time.Now()
	}
	line := fmt.Sprintf("%s Sitzungsende terminal=%q hmi=%q modus=%q dauer=%s grund=%q\n",
		at.Format(time.RFC3339), rec.Terminal, rec.HMI, rec.Mode, rec.Duration.Round(time.Second), rec.Reason)
	_, _ = j.Write([]byte(line))
}

// Warning liefert den aktuellen Hinweis für die Admin-Seite oder "".
func (j *Journal) Warning() string {
	j.mu.Lock()
	ioErr := j.ioErr
	j.mu.Unlock()
	var parts []string
	if ioErr != "" {
		parts = append(parts, "Protokoll nicht schreibbar: "+ioErr)
	}
	if n := j.dropped.Load(); n > 0 {
		parts = append(parts, fmt.Sprintf("Protokoll: %d Zeilen verworfen", n))
	}
	return strings.Join(parts, "; ")
}

// Close schreibt ausstehende Zeilen, wartet aber nur begrenzt auf eine hängende Platte.
func (j *Journal) Close() {
	j.once.Do(func() {
		j.closed.Store(true)
		close(j.stop)
		t := time.NewTimer(j.closeWait)
		defer t.Stop()
		select {
		case <-j.done:
		case <-t.C:
		}
	})
}

func (j *Journal) run() {
	defer close(j.done)
	for {
		select {
		case line := <-j.lines:
			j.handle(line)
		case <-j.stop:
			for {
				select {
				case line := <-j.lines:
					j.handle(line)
				default:
					if j.f != nil {
						j.f.Close()
						j.f = nil
					}
					return
				}
			}
		}
	}
}

func (j *Journal) handle(line []byte) {
	_, _ = j.stderr.Write(line)
	j.writeFile(line)
}

func (j *Journal) path(n int) string {
	if n == 0 {
		return filepath.Join(j.dir, logFileName)
	}
	return filepath.Join(j.dir, logFileName+"."+strconv.Itoa(n))
}

// writeFile verwirft die Zeile, wenn sie nicht geschrieben werden kann.
func (j *Journal) writeFile(line []byte) {
	if limit := min(j.fileLimit, maxLineBytes); int64(len(line)) > limit {
		line = append(line[:limit-1:limit-1], '\n')
	}
	if j.f == nil && time.Now().Before(j.nextOpen) {
		j.dropped.Add(1)
		return
	}
	err := j.ensureRoom(int64(len(line)))
	if err == nil {
		var n int
		n, err = j.f.Write(line)
		j.size += int64(n)
	}
	if err != nil {
		j.fileFailed(err)
		j.dropped.Add(1)
		return
	}
	j.mu.Lock()
	j.ioErr = ""
	j.mu.Unlock()
}

func (j *Journal) ensureRoom(need int64) error {
	if j.f == nil {
		if err := j.open(); err != nil {
			return err
		}
	}
	if j.size+need <= j.fileLimit {
		return nil
	}
	err := j.f.Close()
	j.f = nil
	if err != nil {
		return err
	}
	if err := j.rotate(); err != nil {
		return err
	}
	return j.open()
}

func (j *Journal) rotate() error {
	if err := os.Remove(j.path(logFileCount - 1)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for n := logFileCount - 2; n >= 0; n-- {
		if err := os.Rename(j.path(n), j.path(n+1)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (j *Journal) open() error {
	if err := os.MkdirAll(j.dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(j.path(0), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	j.f, j.size = f, info.Size()
	return nil
}

func (j *Journal) fileFailed(err error) {
	if j.f != nil {
		j.f.Close()
		j.f = nil
	}
	j.nextOpen = time.Now().Add(j.retryEvery)
	j.mu.Lock()
	j.ioErr = err.Error()
	j.mu.Unlock()
}
