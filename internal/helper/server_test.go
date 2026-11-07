package helper

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"hmikiosk/internal/helperapi"
)

func linuxOnly(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("nur Linux")
	}
}

func startServer(t *testing.T, e *env) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "h")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s")
	e.srv.socket = sock
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- e.srv.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})
	for i := 0; i < 200; i++ {
		if c, err := net.Dial("unix", sock); err == nil {
			c.Close()
			return sock
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Server startet nicht")
	return ""
}

func rawCall(t *testing.T, sock, payload string) map[string]any {
	t.Helper()
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	c.Write([]byte(payload))
	line, err := bufio.NewReader(c).ReadBytes('\n')
	if err != nil {
		t.Fatalf("Antwort: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(line, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// systemd legt den Ordner des Sockets als root:root an; der Dienst (Gruppe SocketGID)
// muss trotzdem hineinkommen, darf dort aber nichts anlegen.
func TestServeGivesSocketDirToSocketGroup(t *testing.T) {
	linuxOnly(t)
	if os.Geteuid() != 0 {
		t.Skip("braucht root")
	}
	e := newEnv(t, "")
	e.srv.SocketGID = 4242
	sock := startServer(t, e)
	dir := filepath.Dir(sock)
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	st := fi.Sys().(*syscall.Stat_t)
	if st.Uid != 0 || st.Gid != 4242 || fi.Mode().Perm() != 0o750 {
		t.Fatalf("Ordner %d:%d %o, erwartet 0:4242 750", st.Uid, st.Gid, fi.Mode().Perm())
	}
	si, err := os.Stat(sock)
	if err != nil {
		t.Fatal(err)
	}
	if g := si.Sys().(*syscall.Stat_t).Gid; g != 4242 {
		t.Fatalf("Socket-Gruppe %d", g)
	}
}

func TestServerClientRoundTrip(t *testing.T) {
	linuxOnly(t)
	e := newEnv(t, "[all]\n")
	sock := startServer(t, e)
	cl := helperapi.New(sock)
	if err := cl.Call(context.Background(), CmdRestartService, ""); err != nil {
		t.Fatal(err)
	}
	err := cl.Call(context.Background(), "nope", "")
	if err == nil || err.Error() != "Unbekannter Befehl" {
		t.Fatalf("err: %v", err)
	}
	if err := cl.Call(context.Background(), CmdSetNTP, "-x"); err == nil || err.Error() != "NTP-Server ungültig" {
		t.Fatalf("err: %v", err)
	}
	fi, err := os.Stat(sock)
	if err != nil || fi.Mode().Perm() != 0o660 {
		t.Fatalf("Modus: %v %v", fi, err)
	}
}

func TestPeerUIDIsReal(t *testing.T) {
	linuxOnly(t)
	e := newEnv(t, "")
	var mu sync.Mutex
	got := -1
	e.srv.peerUID = func(c net.Conn) (int, error) {
		uid, err := peerUID(c)
		mu.Lock()
		got = uid
		mu.Unlock()
		return uid, err
	}
	sock := startServer(t, e)
	if m := rawCall(t, sock, `{"cmd":"reboot"}`+"\n"); m["ok"] != true {
		t.Fatalf("resp: %v", m)
	}
	mu.Lock()
	defer mu.Unlock()
	if got != os.Getuid() {
		t.Fatalf("uid %d, erwartet %d", got, os.Getuid())
	}
}

func TestPeerCredRejectsOtherUID(t *testing.T) {
	linuxOnly(t)
	e := newEnv(t, "")
	uid := 4242
	var mu sync.Mutex
	e.srv.peerUID = func(net.Conn) (int, error) { mu.Lock(); defer mu.Unlock(); return uid, nil }
	sock := startServer(t, e)
	m := rawCall(t, sock, `{"cmd":"reboot"}`+"\n")
	if m["error"] != "Nicht erlaubt" {
		t.Fatalf("resp: %v", m)
	}
	if len(e.fx.got()) != 0 {
		t.Fatal("Befehl trotz fremder UID ausgeführt")
	}
	if !e.srv.authorized(0) || !e.srv.authorized(1000) || e.srv.authorized(1001) {
		t.Fatal("authorized")
	}
	mu.Lock()
	uid = 1000
	mu.Unlock()
	if m := rawCall(t, sock, `{"cmd":"reboot"}`+"\n"); m["ok"] != true {
		t.Fatalf("erlaubte UID abgelehnt: %v", m)
	}
}

func TestMalformedRequests(t *testing.T) {
	linuxOnly(t)
	e := newEnv(t, "")
	sock := startServer(t, e)
	for name, p := range map[string]string{
		"kein JSON":               "reboot\n",
		"Zusatzfeld":              `{"cmd":"reboot","x":1}` + "\n",
		"zu groß":                 `{"cmd":"reboot","arg":"` + strings.Repeat("a", 5000) + `"}` + "\n",
		"zu groß ohne Zeilenende": strings.Repeat("a", 6000),
	} {
		m := rawCall(t, sock, p)
		if m["error"] == nil {
			t.Errorf("%s: %v", name, m)
		}
	}
	if len(e.fx.got()) != 0 {
		t.Fatalf("Aufrufe: %v", e.fx.got())
	}
}

func TestReadDeadline(t *testing.T) {
	linuxOnly(t)
	e := newEnv(t, "")
	sock := startServer(t, e)
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Write([]byte(`{"cmd":`)) // unvollständig, kein Zeilenende
	c.SetDeadline(time.Now().Add(8 * time.Second))
	start := time.Now()
	line, err := bufio.NewReader(c).ReadBytes('\n')
	if err != nil {
		t.Fatalf("keine Antwort: %v", err)
	}
	if d := time.Since(start); d < 4*time.Second || d > 7*time.Second {
		t.Fatalf("Frist %v", d)
	}
	if !strings.Contains(string(line), "error") {
		t.Fatalf("resp: %s", line)
	}
}

func TestCommandsRunOneAtATime(t *testing.T) {
	linuxOnly(t)
	e := newEnv(t, "")
	var mu sync.Mutex
	active, maxActive := 0, 0
	e.srv.exec = func(_ context.Context, _ string, _ ...string) error {
		mu.Lock()
		active++
		if active > maxActive {
			maxActive = active
		}
		mu.Unlock()
		time.Sleep(50 * time.Millisecond)
		mu.Lock()
		active--
		mu.Unlock()
		return nil
	}
	sock := startServer(t, e)
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := helperapi.New(sock).Call(context.Background(), CmdReboot, ""); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if maxActive != 1 {
		t.Fatalf("parallel: %d", maxActive)
	}
}

func TestClientHonoursContextDeadline(t *testing.T) {
	linuxOnly(t)
	e := newEnv(t, "")
	block := make(chan struct{})
	e.srv.exec = func(_ context.Context, _ string, _ ...string) error { <-block; return nil }
	sock := startServer(t, e)
	t.Cleanup(func() { close(block) })
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := helperapi.New(sock).Call(ctx, CmdReboot, "")
	if err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("err=%v nach %v", err, time.Since(start))
	}
}

func TestClientUnreachable(t *testing.T) {
	err := helperapi.New(filepath.Join(t.TempDir(), "nix")).Call(context.Background(), CmdReboot, "")
	if err == nil || !strings.Contains(err.Error(), "nicht erreichbar") {
		t.Fatalf("err: %v", err)
	}
}
