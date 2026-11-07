package helper

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	maxRequestBytes = 4096
	readTimeout     = 5 * time.Second
	commandTimeout  = 30 * time.Second
	writeTimeout    = 5 * time.Second
	maxConns        = 8
)

type request struct {
	Cmd string `json:"cmd"`
	Arg string `json:"arg"`
}

type response struct {
	Data  any    `json:"data,omitempty"`
	OK    bool   `json:"ok,omitempty"`
	Error string `json:"error,omitempty"`
}

// Server nimmt pro Verbindung genau eine JSON-Anfrage an. Befehle laufen einzeln.
type Server struct {
	socket     string
	allowedUID int
	exec       Exec
	bootCfg    string
	upd        UpdateOps

	// SocketGID ist die Gruppe des Socket-Dateieintrags; -1 lässt sie unverändert.
	SocketGID int
	// ReapplyNTPOnStart stellt den gespeicherten NTP-Wert nach dem Listen wieder her.
	ReapplyNTPOnStart bool
	// NTPConfPath ist die timesyncd-Datei, NTPStatePath der dauerhafte Wert auf /data.
	NTPConfPath  string
	NTPStatePath string
	// HealthyMarker meldet, dass die gestartete Version gesund ist (Dienst schreibt ihre Version hinein).
	HealthyMarker string

	// ExecOut startet ein Programm und liefert seine Ausgabe (nur blkid).
	ExecOut ExecOut
	// Backup legt Orte und Zeiten der SD-Sicherung fest; die Voreinstellung passt für das Gerät.
	Backup BackupConfig
	// Net legt Orte und Zeiten der Ethernet-Einstellung fest; RecoverNetOnStart setzt eine offene Frist beim Start zurück.
	Net               NetConfig
	RecoverNetOnStart bool

	backup backupState
	netRT  netRuntime
	base   context.Context
	bgWG   sync.WaitGroup

	peerUID func(net.Conn) (int, error)
	mu      sync.Mutex
}

func NewServer(socket string, allowedUID int, e Exec, bootCfg string, upd UpdateOps) *Server {
	return &Server{
		socket: socket, allowedUID: allowedUID, exec: e, bootCfg: bootCfg, upd: upd,
		SocketGID:     -1,
		NTPConfPath:   "/etc/systemd/timesyncd.conf.d/hmi-kiosk.conf",
		NTPStatePath:  "/data/system/ntp-server",
		HealthyMarker: "/run/hmi-kiosk/healthy",
		peerUID:       peerUID,
	}
}

func (s *Server) authorized(uid int) bool { return uid == 0 || uid == s.allowedUID }

// prepareSocketDir gibt den Ordner des Sockets der Socket-Gruppe (Lesen und Durchgehen,
// kein Anlegen). Das hängt nicht davon ab, wie systemd oder install.sh ihn angelegt haben.
func (s *Server) prepareSocketDir() error {
	if s.SocketGID < 0 || os.Geteuid() != 0 {
		return nil
	}
	dir := filepath.Dir(s.socket)
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return errors.New("Ordner des Sockets ist kein Ordner: " + dir)
	}
	if err := os.Lchown(dir, 0, s.SocketGID); err != nil {
		return err
	}
	return os.Chmod(dir, 0o750)
}

// Serve horcht, bis ctx endet.
func (s *Server) Serve(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Dir(s.socket), 0o755); err != nil {
		return err
	}
	if err := s.prepareSocketDir(); err != nil {
		return err
	}
	if fi, err := os.Lstat(s.socket); err == nil {
		if fi.Mode()&os.ModeSocket == 0 {
			return errors.New("Socket-Pfad belegt durch eine andere Datei")
		}
		os.Remove(s.socket)
	}
	ln, err := net.Listen("unix", s.socket)
	if err != nil {
		return err
	}
	defer os.Remove(s.socket)
	if err := os.Chown(s.socket, 0, s.SocketGID); err != nil && os.Geteuid() == 0 {
		ln.Close()
		return err
	}
	if err := os.Chmod(s.socket, 0o660); err != nil {
		ln.Close()
		return err
	}
	s.base = ctx
	go func() { <-ctx.Done(); ln.Close() }()

	slots := make(chan struct{}, maxConns)
	var wg sync.WaitGroup
	defer s.bgWG.Wait()
	defer wg.Wait()
	if s.RecoverNetOnStart {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.RecoverNet(ctx); err != nil {
				log.Printf("Ethernet-Rücksetzung beim Start: %v", err)
			}
		}()
	}
	if s.ReapplyNTPOnStart {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.mu.Lock()
			defer s.mu.Unlock()
			if err := s.ReapplyNTP(ctx); err != nil {
				log.Printf("NTP-Einstellung nicht wiederhergestellt: %v", err)
			}
		}()
	}
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		select {
		case slots <- struct{}{}:
		default:
			c.Close()
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			s.handleConn(ctx, c)
		}()
	}
}

func (s *Server) handleConn(ctx context.Context, c net.Conn) {
	defer c.Close()
	uid, err := s.peerUID(c)
	if err != nil || !s.authorized(uid) {
		log.Printf("Verbindung abgelehnt (UID %d, Fehler %v)", uid, err)
		reply(c, errors.New("Nicht erlaubt"))
		return
	}
	c.SetReadDeadline(time.Now().Add(readTimeout))
	req, err := readRequest(c)
	if err != nil {
		reply(c, err)
		return
	}

	if s.handleBackupConn(ctx, c, req) {
		return
	}

	s.mu.Lock()
	cctx, cancel := context.WithTimeout(ctx, commandTimeout)
	data, err := s.DispatchData(cctx, req.Cmd, req.Arg)
	cancel()
	s.mu.Unlock()
	replyData(c, data, err)
}

func readRequest(r io.Reader) (request, error) {
	br := bufio.NewReaderSize(io.LimitReader(r, maxRequestBytes+1), maxRequestBytes+1)
	line, err := br.ReadBytes('\n')
	if len(line) > maxRequestBytes {
		return request{}, errors.New("Anfrage zu groß")
	}
	if err != nil && err != io.EOF {
		return request{}, errors.New("Anfrage nicht lesbar")
	}
	var req request
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return request{}, errors.New("Anfrage ungültig")
	}
	if _, err := dec.Token(); err != io.EOF {
		return request{}, errors.New("Anfrage ungültig")
	}
	return req, nil
}

func reply(c net.Conn, err error) { replyData(c, nil, err) }

func replyData(c net.Conn, data any, err error) {
	resp := response{OK: true, Data: data}
	if err != nil {
		resp = response{Error: err.Error()}
	}
	b, _ := json.Marshal(resp)
	c.SetWriteDeadline(time.Now().Add(writeTimeout))
	c.Write(append(b, '\n'))
}
