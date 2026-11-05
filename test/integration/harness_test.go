//go:build integration

package integration

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hmikiosk/internal/app"
	"hmikiosk/internal/client"
	"hmikiosk/internal/config"
	"hmikiosk/internal/rfb"
	"hmikiosk/internal/secrets"
)

const (
	waitLimit = 25 * time.Second
	testKey   = "gemeinsamer-schluessel-fuer-tests"
)

// vncServer ist der echte Test-Server auf festem Port, damit er neu starten kann.
type vncServer struct {
	t    *testing.T
	srv  *rfb.TestServer
	port int

	mu sync.Mutex
	l  net.Listener
}

func startVNC(t *testing.T) *vncServer {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	v := &vncServer{t: t, srv: rfb.NewTestServer(64, 48, ""), port: l.Addr().(*net.TCPAddr).Port, l: l}
	go v.srv.Serve(l)
	t.Cleanup(v.stop)
	return v
}

func (v *vncServer) stop() {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.l != nil {
		v.l.Close()
		v.l = nil
	}
	v.srv.DropClients()
}

func (v *vncServer) restart() {
	v.t.Helper()
	v.mu.Lock()
	defer v.mu.Unlock()
	l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(v.port)))
	if err != nil {
		v.t.Fatal(err)
	}
	v.l = l
	go v.srv.Serve(l)
}

// fakeClient verhält sich wie der echte VNC-Client: Er baut die Verbindung auf
// und läuft weiter, auch wenn der Server sie trennt (Remmina zeigt dann nur einen
// Dialog). Die Erkennung muss also über die TCP-Verbindung laufen.
type fakeClient struct {
	conn net.Conn
	done chan error
	once sync.Once
}

func (c *fakeClient) Done() <-chan error { return c.done }

func (c *fakeClient) Stop(time.Duration) error {
	c.once.Do(func() {
		if c.conn != nil {
			c.conn.Close()
		}
		close(c.done)
	})
	return nil
}

func rfbHandshake(c net.Conn) error {
	c.SetDeadline(time.Now().Add(5 * time.Second))
	defer c.SetDeadline(time.Time{})
	version := make([]byte, 12)
	if _, err := io.ReadFull(c, version); err != nil {
		return err
	}
	if _, err := c.Write([]byte("RFB 003.008\n")); err != nil {
		return err
	}
	sec := make([]byte, 2)
	if _, err := io.ReadFull(c, sec); err != nil {
		return err
	}
	if _, err := c.Write(sec[1:]); err != nil {
		return err
	}
	if _, err := io.ReadFull(c, make([]byte, 4)); err != nil {
		return err
	}
	if _, err := c.Write([]byte{1}); err != nil {
		return err
	}
	head := make([]byte, 24)
	if _, err := io.ReadFull(c, head); err != nil {
		return err
	}
	nameLen := int(head[20])<<24 | int(head[21])<<16 | int(head[22])<<8 | int(head[23])
	_, err := io.ReadFull(c, make([]byte, nameLen))
	return err
}

type fakeRunner struct {
	addr    string
	started atomic.Int32

	mu    sync.Mutex
	procs []*fakeClient
}

func (r *fakeRunner) Start(client.Command) (client.Process, error) {
	p := &fakeClient{done: make(chan error, 1)}
	conn, err := net.Dial("tcp", r.addr)
	if err == nil {
		if err = rfbHandshake(conn); err != nil {
			conn.Close()
		} else {
			p.conn = conn
		}
	}
	r.mu.Lock()
	r.procs = append(r.procs, p)
	r.mu.Unlock()
	r.started.Add(1)
	return p, nil
}

func (r *fakeRunner) running() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, p := range r.procs {
		select {
		case <-p.done:
		default:
			n++
		}
	}
	return n
}

type wireSnapshot struct {
	State       string   `json:"state"`
	HMI         string   `json:"hmi"`
	Notice      string   `json:"notice"`
	ConfirmName string   `json:"confirmName"`
	Unreachable []string `json:"unreachable"`
	BadKey      []string `json:"badKey"`
}

type terminal struct {
	t      *testing.T
	name   string
	bound  app.Bound
	runner *fakeRunner
	cancel context.CancelFunc
	done   chan error
	killed atomic.Bool

	mu   sync.Mutex
	snap wireSnapshot
}

type terminalSpec struct {
	name      string
	role      config.Role
	localPort int
	adminPort int
	peers     []config.Peer
	key       string
	hmi       config.HMI
	permanent bool
	vnc       *vncServer
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func startTerminal(t *testing.T, spec terminalSpec) *terminal {
	t.Helper()
	data, run := t.TempDir(), t.TempDir()
	box, err := secrets.OpenBox(filepath.Join(data, "secrets.key"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Terminal.ID = "id-" + spec.name
	cfg.Terminal.Name = spec.name
	cfg.Terminal.Role = spec.role
	cfg.Peers = spec.peers
	if spec.key != "" {
		if cfg.SharedKeyEnc, err = box.Seal(spec.key); err != nil {
			t.Fatal(err)
		}
	}
	h := spec.hmi
	if h.PasswordEnc, err = box.Seal(""); err != nil {
		t.Fatal(err)
	}
	cfg.HMIs = []config.HMI{h}
	cfg.Kiosk.InactivityMinutes = 0
	if spec.permanent {
		cfg.Kiosk.PermanentHMI = h.ID
	}
	if err := config.Open(data).Save(cfg); err != nil {
		t.Fatal(err)
	}

	runner := &fakeRunner{addr: net.JoinHostPort("127.0.0.1", strconv.Itoa(spec.vnc.port))}
	ready := make(chan app.Bound, 1)
	ctx, cancel := context.WithCancel(context.Background())
	term := &terminal{t: t, name: spec.name, runner: runner, cancel: cancel, done: make(chan error, 1)}
	go func() {
		term.done <- app.Run(ctx, app.Options{
			DataDir: data, RunDir: run,
			LocalAddr:    net.JoinHostPort("127.0.0.1", strconv.Itoa(spec.localPort)),
			AdminAddr:    net.JoinHostPort("127.0.0.1", strconv.Itoa(spec.adminPort)),
			HelperSocket: filepath.Join(run, "kein-helfer.sock"),
			Runner:       runner,
			Ready:        func(b app.Bound) { ready <- b },
		})
	}()
	t.Cleanup(term.kill)
	select {
	case term.bound = <-ready:
	case err := <-term.done:
		t.Fatalf("%s startet nicht: %v", spec.name, err)
	case <-time.After(waitLimit):
		t.Fatalf("%s wird nicht bereit", spec.name)
	}
	term.followEvents(ctx)
	return term
}

// kill beendet den Dienst. Die anderen Terminals erfahren davon nichts: Eine
// Abmeldung gibt es nicht, nur die ablaufende Frist.
func (x *terminal) kill() {
	x.t.Helper()
	if x.killed.Swap(true) {
		return
	}
	x.cancel()
	select {
	case err := <-x.done:
		if err != nil {
			x.t.Errorf("%s endet mit %v", x.name, err)
		}
	case <-time.After(waitLimit):
		x.t.Errorf("%s endet nicht", x.name)
	}
}

func (x *terminal) request(ctx context.Context, method, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, "http://"+x.bound.Local+path, nil)
	if err != nil {
		return nil, err
	}
	// Die Kachelseite lässt nur ihren festen Namen zu.
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Origin", "http://127.0.0.1:8080")
	return http.DefaultClient.Do(req)
}

func (x *terminal) followEvents(ctx context.Context) {
	x.t.Helper()
	resp, err := x.request(ctx, "GET", "/api/events")
	if err != nil {
		x.t.Fatal(err)
	}
	go func() {
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			payload, ok := strings.CutPrefix(sc.Text(), "data: ")
			if !ok {
				continue
			}
			var s wireSnapshot
			if json.Unmarshal([]byte(payload), &s) == nil {
				x.mu.Lock()
				x.snap = s
				x.mu.Unlock()
			}
		}
	}()
}

func (x *terminal) snapshot() wireSnapshot {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.snap
}

func (x *terminal) post(path string) {
	x.t.Helper()
	resp, err := x.request(context.Background(), "POST", path)
	if err != nil {
		x.t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		x.t.Fatalf("%s: POST %s: Status %d", x.name, path, resp.StatusCode)
	}
}

func (x *terminal) tilesPage() string {
	x.t.Helper()
	resp, err := x.request(context.Background(), "GET", "/")
	if err != nil {
		x.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		x.t.Fatal(err)
	}
	return string(b)
}

func await(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(waitLimit)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("warte auf %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (x *terminal) awaitState(state string) wireSnapshot {
	x.t.Helper()
	var s wireSnapshot
	await(x.t, fmt.Sprintf("%s im Zustand %s", x.name, state), func() bool {
		s = x.snapshot()
		return s.State == state
	})
	return s
}

func (x *terminal) awaitNotice(state, part string) wireSnapshot {
	x.t.Helper()
	var s wireSnapshot
	await(x.t, fmt.Sprintf("%s im Zustand %s mit %q", x.name, state, part), func() bool {
		s = x.snapshot()
		return s.State == state && strings.Contains(s.Notice, part)
	})
	return s
}

func (x *terminal) connect(id string) {
	x.t.Helper()
	x.post("/api/hmi/" + id + "/open")
	x.awaitState("connected")
}

// pair startet zwei Terminals, die voneinander wissen.
func pair(t *testing.T, vnc *vncServer, h config.HMI, roleA, roleB config.Role, keyA, keyB string) (a, b *terminal) {
	t.Helper()
	adminA, adminB := freePort(t), freePort(t)
	peer := func(name string, port int) []config.Peer {
		return []config.Peer{{Name: name, Address: net.JoinHostPort("127.0.0.1", strconv.Itoa(port))}}
	}
	a = startTerminal(t, terminalSpec{name: "A", role: roleA, adminPort: adminA, peers: peer("B", adminB), key: keyA, hmi: h, vnc: vnc})
	b = startTerminal(t, terminalSpec{name: "B", role: roleB, adminPort: adminB, peers: peer("A", adminA), key: keyB, hmi: h, vnc: vnc})
	return a, b
}

func press(vnc *vncServer) config.HMI {
	return config.HMI{ID: "presse", Name: "Presse", Host: "127.0.0.1", Port: vnc.port, Takeover: config.StyleConfirm}
}
