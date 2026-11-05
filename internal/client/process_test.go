//go:build linux

package client

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func sh(script string, env ...string) Command {
	return Command{Path: "/bin/sh", Args: []string{"-c", script}, Env: env}
}

func waitFile(t *testing.T, p string) string {
	t.Helper()
	for i := 0; i < 200; i++ {
		if b, err := os.ReadFile(p); err == nil && strings.HasSuffix(string(b), "\n") {
			return strings.TrimSpace(string(b))
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("%s not written", p)
	return ""
}

// alive: Zombies zählen als tot (Container ohne init reapt verwaiste Kinder nicht).
func alive(pid int) bool {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	s := string(b)
	return s[strings.LastIndex(s, ")")+2] != 'Z'
}

func waitDone(t *testing.T, p Process) error {
	t.Helper()
	select {
	case err := <-p.Done():
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("process did not finish")
		return nil
	}
}

func TestProcessStopKillsWholeGroup(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	p, err := NewExecRunner().Start(sh(`sleep 60 & echo $! > "$PIDFILE.tmp"; mv "$PIDFILE.tmp" "$PIDFILE"; wait`, "PIDFILE="+pidFile))
	if err != nil {
		t.Fatal(err)
	}
	child, _ := strconv.Atoi(waitFile(t, pidFile))
	if !alive(child) {
		t.Fatal("child not running")
	}
	if err := p.Stop(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	waitDone(t, p)
	for i := 0; i < 100 && alive(child); i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if alive(child) {
		t.Fatal("child survived Stop")
	}
	if err := p.Stop(time.Second); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
}

func TestProcessStopEscalatesToKillAfterGrace(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	p, err := NewExecRunner().Start(sh(`trap '' TERM; echo up > "$READY.tmp"; mv "$READY.tmp" "$READY"; while :; do sleep 1; done`, "READY="+ready))
	if err != nil {
		t.Fatal(err)
	}
	waitFile(t, ready)
	start := time.Now()
	if err := p.Stop(300 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < 250*time.Millisecond || d > 3*time.Second {
		t.Fatalf("Stop took %v", d)
	}
	if err := waitDone(t, p); err == nil {
		t.Fatal("expected kill exit error")
	}
}

func TestProcessDoneOnExit(t *testing.T) {
	p, err := NewExecRunner().Start(sh("exit 3"))
	if err != nil {
		t.Fatal(err)
	}
	if err := waitDone(t, p); err == nil || !strings.Contains(err.Error(), "exit status 3") {
		t.Fatalf("err = %v", err)
	}
	if _, open := <-p.Done(); open {
		t.Fatal("Done not closed")
	}
	p, _ = NewExecRunner().Start(sh("exit 0"))
	if err := waitDone(t, p); err != nil {
		t.Fatalf("err = %v", err)
	}
	if err := p.Stop(time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestProcessEnvAppendedAndOverrides(t *testing.T) {
	t.Setenv("HMI_KEEP", "keep")
	t.Setenv("HMI_OVER", "old")
	out := filepath.Join(t.TempDir(), "out")
	p, err := NewExecRunner().Start(sh(`echo "$HMI_KEEP $HMI_OVER $HMI_NEW" > "$OUT"`, "HMI_OVER=new", "HMI_NEW=x", "OUT="+out))
	if err != nil {
		t.Fatal(err)
	}
	if err := waitDone(t, p); err != nil {
		t.Fatal(err)
	}
	if got := waitFile(t, out); got != "keep new x" {
		t.Fatalf("got %q", got)
	}
}

func TestProcessStartFailure(t *testing.T) {
	if _, err := NewExecRunner().Start(Command{Path: "/nonexistent/x"}); err == nil {
		t.Fatal("expected error")
	}
}
