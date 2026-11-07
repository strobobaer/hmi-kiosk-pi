package helper

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"hmikiosk/internal/helperapi"
)

// Die Befehle der SD-Sicherung. backup-stream, backup-status und backup-cancel laufen
// am Befehls-Mutex vorbei (siehe handleConn), backup-usb ist ein gewöhnlicher Befehl.
const (
	CmdBackupStream = helperapi.CmdBackupStream
	CmdBackupUSB    = helperapi.CmdBackupUSB
	CmdBackupStatus = helperapi.CmdBackupStatus
	CmdBackupCancel = helperapi.CmdBackupCancel
	CmdBackupInfo   = helperapi.CmdBackupInfo
)

const (
	// openTimeout begrenzt den Bau des Abbilds (mkfs aus /data), infoTimeout das Ausmessen.
	openTimeout = helperapi.BackupOpenTimeout
	syncTimeout = helperapi.BackupSyncTimeout
	infoTimeout = 30 * time.Second
)

const (
	errBackupBusy   = "Eine SD-Sicherung läuft gerade."
	msgCancelled    = "Die Sicherung wurde abgebrochen."
	msgDisconnected = "Die Verbindung zum Browser wurde getrennt."

	defaultIdleTimeout = 60 * time.Second
	copyChunk          = 1 << 20
)

var nameRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,40}$`)

// Während einer Sicherung ändern diese Befehle das System; sie werden abgelehnt.
var blockedDuringBackup = map[string]bool{
	CmdReboot: true, CmdRestartService: true, CmdUpdateActivate: true, CmdUpdateRollback: true,
	CmdWifiOff: true, CmdWifiOn: true, CmdBtOff: true, CmdBtOn: true,
}

// BackupConfig sind die Orte und Zeiten der SD-Sicherung; Leeres gilt als Voreinstellung.
// Tests ersetzen sie durch Ersatzdateien.
type BackupConfig struct {
	// Source ersetzt die Erkennung: eine Datei oder ein Gerät mit MBR, das statt der Karte gelesen wird.
	Source string
	// OpenSource ersetzt die Quelle ganz (Nahtstelle für das Abbildformat); Tests nutzen sie.
	OpenSource    OpenSource
	DataDir       string // /data
	RunDir        string // /run (tmpfs für die Temp-Datei)
	ImageRelease  string // /etc/hmi-kiosk/image-release
	MemInfo       string // /proc/meminfo
	MountInfo     string // /proc/self/mountinfo
	SysClassBlock string // /sys/class/block
	SysBlock      string // /sys/block
	DevDir        string // /dev
	BootMount     string // /boot/firmware
	UsbMount      string // /run/hmi-kiosk-usb
	// IdleTimeout: so lange darf ein Schreibvorgang zum Dienst warten.
	IdleTimeout time.Duration
	FreeBytes   func(path string) (int64, error)
	Now         func() time.Time

	onChunk func(total int64) // nur Tests
}

func (b BackupConfig) or(v, def string) string {
	if v != "" {
		return v
	}
	return def
}

func (b BackupConfig) mountInfo() string { return b.or(b.MountInfo, "/proc/self/mountinfo") }
func (b BackupConfig) sysClass() string  { return b.or(b.SysClassBlock, "/sys/class/block") }
func (b BackupConfig) sysBlock() string  { return b.or(b.SysBlock, "/sys/block") }
func (b BackupConfig) devDir() string    { return b.or(b.DevDir, "/dev") }
func (b BackupConfig) bootMount() string { return b.or(b.BootMount, bootMount) }
func (b BackupConfig) usbMount() string  { return b.or(b.UsbMount, "/run/hmi-kiosk-usb") }
func (b BackupConfig) dataDir() string   { return b.or(b.DataDir, "/data") }
func (b BackupConfig) runDir() string    { return b.or(b.RunDir, "/run") }
func (b BackupConfig) imageRelease() string {
	return b.or(b.ImageRelease, "/etc/hmi-kiosk/image-release")
}

func (b BackupConfig) idle() time.Duration {
	if b.IdleTimeout > 0 {
		return b.IdleTimeout
	}
	return defaultIdleTimeout
}

func (b BackupConfig) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

func (b BackupConfig) free(path string) (int64, error) {
	if b.FreeBytes != nil {
		return b.FreeBytes(path)
	}
	return freeBytes(path)
}

// backupState hält die eine laufende Sicherung und den Stand der letzten.
type backupState struct {
	mu        sync.Mutex
	running   bool
	cancelled bool
	cancel    context.CancelFunc
	st        helperapi.BackupStatus
	started   time.Time
	read      atomic.Int64
}

// backupRun ist der Griff der laufenden Sicherung.
type backupRun struct {
	b   *backupState
	ctx context.Context
}

// begin nimmt die Sicherungssperre. Es gibt höchstens eine Sicherung zugleich.
func (b *backupState) begin(parent context.Context, mode, target string) (*backupRun, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.running {
		return nil, errors.New(errBackupBusy)
	}
	ctx, cancel := context.WithCancel(parent)
	b.running, b.cancelled, b.cancel = true, false, cancel
	b.started = time.Now()
	b.read.Store(0)
	b.st = helperapi.BackupStatus{Running: true, Mode: mode, Target: target}
	return &backupRun{b: b, ctx: ctx}, nil
}

func (b *backupState) isRunning() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.running
}

func (b *backupState) status() helperapi.BackupStatus {
	b.mu.Lock()
	defer b.mu.Unlock()
	st := b.st
	if b.running {
		st.ReadBytes = b.read.Load()
		st.ElapsedSec = time.Since(b.started).Seconds()
	}
	return st
}

func (b *backupState) userCancel() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.running {
		return errors.New("Es läuft keine SD-Sicherung.")
	}
	b.cancelled = true
	b.cancel()
	return nil
}

func (r *backupRun) setTarget(t string) {
	r.b.mu.Lock()
	r.b.st.Target = t
	r.b.mu.Unlock()
}

func (r *backupRun) setTotal(n int64) {
	r.b.mu.Lock()
	r.b.st.TotalBytes = n
	r.b.mu.Unlock()
}

func (r *backupRun) total() int64 {
	r.b.mu.Lock()
	defer r.b.mu.Unlock()
	return r.b.st.TotalBytes
}

func (r *backupRun) userCancelled() bool {
	r.b.mu.Lock()
	defer r.b.mu.Unlock()
	return r.b.cancelled
}

// end gibt die Sperre frei und hält das Ergebnis fest. Bei Erfolg ist file die fertige Datei.
func (r *backupRun) end(err error, file string) {
	b := r.b
	b.mu.Lock()
	defer b.mu.Unlock()
	b.st.Running = false
	b.st.ReadBytes = b.read.Load()
	b.st.ElapsedSec = time.Since(b.started).Seconds()
	switch {
	case err != nil && b.cancelled:
		b.st.Error = msgCancelled
	case errors.Is(err, context.Canceled):
		b.st.Error = msgDisconnected
	case err != nil:
		b.st.Error = err.Error()
		b.st.File = file
	default:
		b.st.Finished, b.st.File = true, file
	}
	b.running = false
	b.cancel()
}

func (r *backupRun) add(n int) { r.b.read.Add(int64(n)) }

// sinkError markiert Fehler beim Schreiben des Ziels (gegenüber dem Lesen der Karte).
type sinkError struct{ err error }

func (e sinkError) Error() string { return e.err.Error() }
func (e sinkError) Unwrap() error { return e.err }

// copyCard liest die Karte in Blöcken, komprimiert sie mit gzip (BestSpeed) in w und
// zählt die gelesenen Rohbytes. Beendet wird sie auch durch ctx.
func (s *Server) copyCard(run *backupRun, src io.Reader, w io.Writer) error {
	gz, err := gzip.NewWriterLevel(w, gzip.BestSpeed)
	if err != nil {
		return err
	}
	buf := make([]byte, copyChunk)
	for {
		if err := run.ctx.Err(); err != nil {
			return err
		}
		n, rerr := io.ReadFull(src, buf)
		if n > 0 {
			if _, err := gz.Write(buf[:n]); err != nil {
				return sinkError{err}
			}
			run.add(n)
			if s.Backup.onChunk != nil {
				s.Backup.onChunk(run.b.read.Load())
			}
		}
		if rerr == io.EOF || rerr == io.ErrUnexpectedEOF {
			break
		}
		if rerr != nil {
			return errors.New("Die SD-Karte ließ sich nicht lesen: " + rerr.Error())
		}
	}
	if err := run.ctx.Err(); err != nil {
		return err
	}
	if got, want := run.b.read.Load(), run.total(); got != want {
		return fmt.Errorf("Die SD-Karte lieferte nur %d von %d Bytes.", got, want)
	}
	if err := gz.Close(); err != nil {
		return sinkError{err}
	}
	return run.ctx.Err()
}

// baseCtx ist der Kontext des laufenden Servers (für Arbeit, die einen Befehl überlebt).
func (s *Server) baseCtx() context.Context {
	if s.base != nil {
		return s.base
	}
	return context.Background()
}

func validBackupName(name string) bool { return nameRe.MatchString(name) }
