//go:build linux

package selftest

import (
	"context"
	"crypto/des"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"io"
	"log"
	"math/bits"
	"net"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hmikiosk/internal/client"
	"hmikiosk/internal/config"
	"hmikiosk/internal/coord"
	"hmikiosk/internal/platform"
)

// fakeProfile reicht Host, Port und Passwort an den gefälschten Client weiter.
type fakeProfile struct{}

func (fakeProfile) Name() string { return "remmina" }
func (fakeProfile) Prepare(dir string, s client.Spec) (client.Command, error) {
	return client.Command{Path: "fake", Args: []string{s.Host, strconv.Itoa(s.Port), s.Password, strconv.FormatBool(s.ViewOnly)}}, nil
}

// fakeRunner startet Clients, die den RFB-Handshake wirklich gegen den Testserver führen.
type fakeRunner struct {
	mu         sync.Mutex
	procs      []*fakeProc
	startErr   error
	failStart  func(n int) bool // n zählt ab 1
	starts     int
	exitOnDrop bool
	tap        *image.Point
	// ignoreTerm: Stop beendet erst nach Ablauf der Gnadenfrist (wie SIGKILL).
	ignoreTerm bool
}

func (r *fakeRunner) Start(c client.Command) (client.Process, error) {
	r.mu.Lock()
	r.starts++
	n := r.starts
	err, fail := r.startErr, r.failStart
	r.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if fail != nil && fail(n) {
		return nil, errors.New("Start fehlgeschlagen")
	}
	port, _ := strconv.Atoi(c.Args[1])
	p := &fakeProc{done: make(chan error, 1), stop: make(chan struct{}), r: r}
	r.mu.Lock()
	r.procs = append(r.procs, p)
	p.pid = 40000 + len(r.procs)
	r.mu.Unlock()
	go p.run(net.JoinHostPort(c.Args[0], strconv.Itoa(port)), c.Args[2], c.Args[3] == "true")
	return p, nil
}

func (r *fakeRunner) alive() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, p := range r.procs {
		if !p.exited.Load() {
			n++
		}
	}
	return n
}

type fakeProc struct {
	r      *fakeRunner
	pid    int
	done   chan error
	stop   chan struct{}
	once   sync.Once
	exited atomic.Bool
	err    error
}

func (p *fakeProc) Pid() int           { return p.pid }
func (p *fakeProc) Done() <-chan error { return p.done }
func (p *fakeProc) finish(err error) {
	p.once.Do(func() { p.err = err; p.exited.Store(true); p.done <- err; close(p.done) })
}
func (p *fakeProc) kill() { p.finish(errors.New("signal: killed")); close(p.stop) }
func (p *fakeProc) Stop(g time.Duration) error {
	if p.r.ignoreTerm {
		select {
		case <-p.done:
		case <-time.After(g):
		}
	}
	p.once.Do(func() { p.exited.Store(true); p.done <- nil; close(p.done); close(p.stop) })
	return nil
}

func vncResponse(pw string, ch []byte) []byte {
	key := make([]byte, 8)
	copy(key, pw)
	for i := range key {
		key[i] = bits.Reverse8(key[i])
	}
	c, _ := des.NewCipher(key)
	out := make([]byte, 16)
	c.Encrypt(out[:8], ch[:8])
	c.Encrypt(out[8:], ch[8:])
	return out
}

func (p *fakeProc) run(addr, pw string, viewOnly bool) {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		p.finish(err)
		return
	}
	defer conn.Close()
	go func() { <-p.stop; conn.Close() }()
	if err := p.handshake(conn, pw); err != nil {
		p.finish(err)
		return
	}
	req := []byte{3, 0, 0, 0, 0, 0, 0x04, 0x00, 0x02, 0x58} // 1024x600, nicht inkrementell
	conn.Write(req)
	if p.r.tap != nil && !viewOnly {
		tap := *p.r.tap
		for _, mask := range []byte{1, 0} {
			m := []byte{5, mask, 0, 0, 0, 0}
			binary.BigEndian.PutUint16(m[2:], uint16(tap.X))
			binary.BigEndian.PutUint16(m[4:], uint16(tap.Y))
			conn.Write(m)
		}
	}
	_, err = io.Copy(io.Discard, conn)
	if p.r.exitOnDrop {
		select {
		case <-p.stop:
		default:
			p.finish(fmt.Errorf("Verbindung getrennt: %v", err))
		}
		return
	}
	<-p.stop // bleibt stehen wie ein Client mit offenem Dialog
}

func (p *fakeProc) handshake(c net.Conn, pw string) error {
	var ver [12]byte
	if _, err := io.ReadFull(c, ver[:]); err != nil {
		return err
	}
	c.Write([]byte("RFB 003.008\n"))
	var n [1]byte
	io.ReadFull(c, n[:])
	types := make([]byte, n[0])
	io.ReadFull(c, types)
	sec := types[0]
	c.Write([]byte{sec})
	if sec == 2 {
		ch := make([]byte, 16)
		if _, err := io.ReadFull(c, ch); err != nil {
			return err
		}
		c.Write(vncResponse(pw, ch))
	}
	var res [4]byte
	if _, err := io.ReadFull(c, res[:]); err != nil {
		return err
	}
	if binary.BigEndian.Uint32(res[:]) != 0 {
		return errors.New("Anmeldung abgelehnt")
	}
	c.Write([]byte{1})
	hdr := make([]byte, 24)
	if _, err := io.ReadFull(c, hdr); err != nil {
		return err
	}
	_, err := io.CopyN(io.Discard, c, int64(binary.BigEndian.Uint32(hdr[20:])))
	return err
}

// recorder merkt sich die Seiten, die Show angezeigt bekam.
type recorder struct {
	mu      sync.Mutex
	pages   []string
	onTouch func()
}

func (r *recorder) show(page string) {
	r.mu.Lock()
	r.pages = append(r.pages, page)
	f := r.onTouch
	r.mu.Unlock()
	if page == "touch" && f != nil {
		go f()
	}
}

func (r *recorder) got() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.pages...)
}

const goodMounts = `overlay / overlay rw,relatime,lowerdir=/media/root-ro,upperdir=/media/root-rw/overlay,workdir=/media/root-rw/work 0 0
/dev/mmcblk0p1 /boot/firmware vfat ro,relatime,fmask=0022 0 0
/dev/mmcblk0p3 /data ext4 rw,relatime 0 0
`

func fastTiming() Timing {
	return Timing{
		Poll: 10 * time.Millisecond, Connect: 3 * time.Second, ViewHold: 20 * time.Millisecond,
		WrongPassword: 150 * time.Millisecond, TouchWait: 1500 * time.Millisecond, PointerWait: 1500 * time.Millisecond,
		StopWithin: 2 * time.Second, DropWithin: 3 * time.Second, ExitWithin: 1 * time.Second,
		LostAfter: 150 * time.Millisecond, Window: 200 * time.Millisecond, StopGrace: 100 * time.Millisecond, SoakHold: time.Millisecond,
	}
}

type fixture struct {
	t      *testing.T
	runner *fakeRunner
	rec    *recorder
	cfg    config.Config
	d      Deps
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, runner: &fakeRunner{exitOnDrop: true}, rec: &recorder{}, cfg: config.Default()}
	f.cfg.Display.GraphicsProfile = "labwc"
	f.cfg.Client.Scale = true
	board := NewTouchBoard()
	f.rec.onTouch = func() {
		for i := 0; i < TouchTargets; i++ {
			board.Hit(i)
		}
	}
	tap := image.Pt(203, 298) // 3 px neben dem Marker (200,300)
	f.runner.tap = &tap
	f.d = Deps{
		Runner:  f.runner,
		Profile: func() client.Profile { return fakeProfile{} },
		Config:  func() config.Config { return f.cfg },
		Screen:  func() (int, int) { return 1280, 800 },
		Established: func(ip net.IP, port int) (bool, error) {
			return platform.HasEstablished("/proc", ip, port)
		},
		RAMDir: func() (string, func(), error) {
			d, err := os.MkdirTemp("", "selftest-*")
			return d, func() { os.RemoveAll(d) }, err
		},
		Peer: func(context.Context, config.Peer) coord.PeerState {
			return coord.PeerState{Reachable: true, KeyOK: true}
		},
		Mounts:     func() ([]byte, error) { return []byte(goodMounts), nil },
		BootConfig: func() (string, error) { return "[all]\ndtoverlay=disable-wifi\n", nil },
		Exec: func(_ context.Context, name string, args ...string) (string, error) {
			if name == "systemctl" {
				return "RuntimeWatchdogUSec=15s\n", nil
			}
			return "", fmt.Errorf("%s nicht vorhanden", name)
		},
		Show:  f.rec.show,
		Touch: board,
		Kill: func(p client.Process) error {
			p.(*fakeProc).kill()
			return nil
		},
		GroupRSS: func(int) (int64, error) { return 50 << 20, nil },
		Timing:   fastTiming(),
	}
	f.cfg.Network.WifiOff = true
	return f
}

func byName(t *testing.T, rs []StepResult) map[string]StepResult {
	t.Helper()
	m := map[string]StepResult{}
	for _, r := range rs {
		m[r.Name] = r
	}
	return m
}

var _ = context.Background

func TestMain(m *testing.M) {
	log.SetOutput(io.Discard)
	os.Exit(m.Run())
}
