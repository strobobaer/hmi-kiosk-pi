package app

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"hmikiosk/internal/selftest"
	"hmikiosk/internal/web"
)

const (
	soakBlockedNotice = "Dauerprüfung läuft"
	soakLastName      = "soak-last.json"
)

// soakJob ist die Dauerprüfung im Dienst (Admin-Seite). Sie teilt sich die Sperre
// selftestMu mit der Selbstprüfung und belegt das Terminal: Kacheln sind gesperrt.
type soakJob struct {
	s *service

	// pending sperrt Sitzungen schon während der Prüfung in Start, ohne den Stand zu ändern.
	pending atomic.Bool

	mu      sync.Mutex
	st      web.SoakStatus
	res     selftest.SoakResult
	cancel  context.CancelFunc
	done    chan struct{}
	logFile *os.File
}

func newSoakJob(s *service) *soakJob { return &soakJob{s: s} }

func (j *soakJob) running() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.st.Running || j.pending.Load()
}

// Start startet die Dauerprüfung in einer eigenen Goroutine. Das Flag steht vor der
// Prüfung auf Sitzungen, damit ein Kachel-Tipp dazwischen nicht durchrutscht.
func (j *soakJob) Start(d time.Duration) error {
	s := j.s
	if !s.selftestMu.TryLock() {
		return errors.New("Eine Selbstprüfung oder Dauerprüfung läuft bereits.")
	}
	ctx := s.rootCtx()
	switch {
	case ctx == nil:
		s.selftestMu.Unlock()
		return errors.New("Der Dienst startet noch.")
	case ctx.Err() != nil:
		s.selftestMu.Unlock()
		return errors.New("Der Dienst wird gerade beendet.")
	}
	j.pending.Store(true)
	if msg := s.sessionBusyMessage(); msg != "" {
		j.pending.Store(false)
		s.selftestMu.Unlock()
		return errors.New(msg)
	}
	j.begin(d)
	j.pending.Store(false)
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	j.mu.Lock()
	j.cancel, j.done = cancel, done
	j.mu.Unlock()
	deps := s.selftestDeps()
	deps.Busy, deps.Show, deps.Touch = nil, nil, nil // die Prüfung läuft hier schon unter der Sperre
	go func() {
		defer close(done)
		defer s.selftestMu.Unlock()
		defer cancel()
		err := selftest.Soak(ctx, deps, d, j.record)
		if errors.Is(err, context.Canceled) {
			err = nil
		}
		j.finish(err)
		// Der Hinweis von abgewiesenen Kachel-Tipps soll nicht stehen bleiben.
		s.mgr.ClearNotice(soakBlockedNotice)
	}()
	return nil
}

// Stop beendet die Dauerprüfung; sie ist kurz danach vorbei.
func (j *soakJob) Stop() {
	j.mu.Lock()
	cancel := j.cancel
	j.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// wait kehrt zurück, wenn keine Dauerprüfung mehr läuft.
func (j *soakJob) wait() {
	j.mu.Lock()
	done := j.done
	j.mu.Unlock()
	if done != nil {
		<-done
	}
}

func (j *soakJob) Status() web.SoakStatus {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.st
}

func (j *soakJob) begin(d time.Duration) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.st = web.SoakStatus{Running: true, Started: time.Now(), Duration: d}
	j.res = selftest.SoakResult{}
	if j.logFile != nil {
		j.logFile.Close()
		j.logFile = nil
	}
	path := filepath.Join(j.s.opts.DataDir, logDirName, soakLogName)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err == nil {
		if f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			j.logFile = f
		} else {
			log.Printf("Dauerprüfung: %s nicht beschreibbar: %v", path, err)
		}
	}
}

func (j *soakJob) record(s selftest.SoakSample) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.res.Add(s)
	j.st.Cycles, j.st.Failures = j.res.Cycles, j.res.Failures
	j.st.RSSBytes = s.RSSBytes
	j.st.Leak, j.st.SlopeBytesPerHour = j.res.Leak, j.res.SlopeBytesPerHour
	if s.Error != "" {
		j.st.LastError = s.Error
	}
	if b, err := json.Marshal(s); err == nil && j.logFile != nil {
		_, _ = j.logFile.Write(append(b, '\n'))
	}
	j.persistLocked()
}

func (j *soakJob) finish(err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.st.Running = false
	j.st.Ended = time.Now()
	if err != nil {
		j.st.LastError = err.Error()
	}
	if j.logFile != nil {
		j.logFile.Close()
		j.logFile = nil
	}
	j.persistLocked()
}

// persistLocked schreibt den Stand in den RAM-Ordner, damit eine neu geladene Seite
// ihn zeigt. Über einen Neustart des Dienstes hinweg bleibt er nicht erhalten.
func (j *soakJob) persistLocked() {
	b, err := json.Marshal(j.st)
	if err != nil {
		return
	}
	if err := writeAtomic(filepath.Join(j.s.opts.RunDir, soakLastName), b, 0o644); err != nil {
		log.Printf("Dauerprüfung: Ergebnisdatei nicht geschrieben: %v", err)
	}
}

// soakBlocked sperrt den Start echter Sitzungen, solange die Dauerprüfung läuft.
func (s *service) soakBlocked() string {
	if s.soak.running() {
		return soakBlockedNotice
	}
	return ""
}
