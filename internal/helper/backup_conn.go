package helper

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"time"
)

// handleBackupConn bedient die Befehle der SD-Sicherung, die nicht über den
// Befehls-Mutex laufen. Es meldet, ob die Verbindung damit erledigt ist.
func (s *Server) handleBackupConn(ctx context.Context, c net.Conn, req request) bool {
	switch req.Cmd {
	case CmdBackupStream:
		if req.Arg != "" {
			reply(c, errors.New("Dieser Befehl nimmt keinen Wert an"))
			return true
		}
		s.serveBackupStream(ctx, c)
	case CmdBackupStatus, CmdBackupCancel, CmdBackupInfo:
		if req.Arg != "" {
			reply(c, errors.New("Dieser Befehl nimmt keinen Wert an"))
			return true
		}
		switch req.Cmd {
		case CmdBackupStatus:
			replyData(c, s.backup.status(), nil)
		case CmdBackupInfo:
			// Misst /data; das kann dauern, hält aber weder Mutex noch Sperre.
			ictx, cancel := context.WithTimeout(ctx, infoTimeout)
			replyData(c, s.backupInfo(ictx), nil)
			cancel()
		default:
			reply(c, s.backup.userCancel())
		}
	default:
		return false
	}
	return true
}

// idleWriter setzt vor jedem Schreiben eine Frist: Liest der Dienst nicht mehr,
// bricht die Sicherung ab und die Sperre wird frei.
type idleWriter struct {
	c    net.Conn
	idle time.Duration
}

func (w idleWriter) Write(p []byte) (int, error) {
	w.c.SetWriteDeadline(time.Now().Add(w.idle))
	return w.c.Write(p)
}

// serveBackupStream antwortet nicht mit einer einzelnen JSON-Zeile: erst eine Kopfzeile
// {"ok":true,"size":N} (oder {"error":...}), dann rohe gzip-Bytes bis zum Ende der Verbindung.
// Der Stand wird vor dem Schließen festgehalten, damit der Dienst danach prüfen kann,
// ob der Strom vollständig war.
func (s *Server) serveBackupStream(ctx context.Context, c net.Conn) {
	if err := s.precheckSource(); err != nil {
		reply(c, err)
		return
	}
	// Unter dem Mutex, damit kein Befehl zwischen Prüfung und Sperre schlüpft.
	s.mu.Lock()
	run, err := s.backup.begin(ctx, "download", "Browser")
	s.mu.Unlock()
	if err != nil {
		reply(c, err)
		return
	}
	// Abbrechen löst auch ein blockiertes Schreiben.
	defer context.AfterFunc(run.ctx, func() { c.Close() })()
	// Der Dienst sendet nach der Anfrage nichts mehr; Lesefehler oder Ende heißt: er ist weg.
	// So endet auch der Bau des Abbilds, bevor die erste Kopfzeile geschrieben ist.
	c.SetReadDeadline(time.Time{})
	cancel := run.b.cancel
	go func() {
		var b [1]byte
		if _, err := c.Read(b[:]); err != nil {
			cancel()
		}
	}()

	headerSent := false
	err = func() error {
		sctx, cancel := context.WithTimeout(run.ctx, commandTimeout)
		defer cancel()
		if err := s.run(sctx, "sync"); err != nil {
			return err
		}
		octx, ocancel := context.WithTimeout(run.ctx, openTimeout)
		defer ocancel()
		f, size, err := s.openCard(octx)
		if err != nil {
			return err
		}
		defer f.Close()
		run.setTotal(size)
		head, _ := json.Marshal(struct {
			OK   bool  `json:"ok"`
			Size int64 `json:"size"`
		}{true, size})
		w := idleWriter{c, s.Backup.idle()}
		if _, err := w.Write(append(head, '\n')); err != nil {
			return streamErr(err)
		}
		headerSent = true
		err = s.copyCard(run, f, w)
		var se sinkError
		if errors.As(err, &se) {
			return streamErr(se.err)
		}
		return err
	}()
	run.end(err, "")
	if err != nil && !headerSent {
		reply(c, err)
	}
}

func streamErr(err error) error {
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return errors.New("Die Sicherung wurde nicht abgeholt (Zeitüberschreitung).")
	}
	return errors.New("Die Verbindung zum Dienst wurde unterbrochen.")
}
