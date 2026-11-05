package app

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"hmikiosk/internal/client"
)

// startNamed startet eine Kopie von sleep unter dem Namen name in eigener
// Prozessgruppe. Der Name steht damit in /proc/<pid>/cmdline.
func startNamed(t *testing.T, name string) (*exec.Cmd, <-chan struct{}) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("nur Linux")
	}
	src, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("kein sleep")
	}
	dst := filepath.Join(t.TempDir(), name)
	copyFile(t, src, dst)
	cmd := exec.Command(dst, "300")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	gone := make(chan struct{})
	go func() { cmd.Wait(); close(gone) }()
	t.Cleanup(func() {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-gone
	})
	return cmd, gone
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}

func awaitGone(t *testing.T, gone <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-gone:
	case <-time.After(10 * time.Second):
		t.Fatalf("%s läuft noch", what)
	}
}

func TestKillLeftoversOnlyVerifiedGroups(t *testing.T) {
	remmina, remminaGone := startNamed(t, "remmina-rest")
	viewer, viewerGone := startNamed(t, "xtigervncviewer")
	stranger, strangerGone := startNamed(t, "fremdes-programm")

	pidFile := filepath.Join(t.TempDir(), "children.pid")
	content := fmt.Sprintf("%d\n%d\nunsinn\n-1\n0\n1\n%d\n999999999\n", remmina.Process.Pid, viewer.Process.Pid, stranger.Process.Pid)
	if err := os.WriteFile(pidFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	killLeftovers("/proc", pidFile, 2*time.Second)

	awaitGone(t, remminaGone, "remmina")
	awaitGone(t, viewerGone, "vncviewer")
	select {
	case <-strangerGone:
		t.Fatal("fremder Prozess beendet")
	default:
	}
	if _, err := os.Stat(pidFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("PID-Datei bleibt: %v", err)
	}
}

func TestKillLeftoversEscalatesToSigkill(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("nur Linux")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("kein sh")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "chromium-stur")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntrap '' TERM\necho bereit\nwhile :; do sleep 1; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := stdout.Read(make([]byte, 16)); err != nil {
		t.Fatal(err)
	}
	gone := make(chan struct{})
	go func() { cmd.Wait(); close(gone) }()
	t.Cleanup(func() { syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); <-gone })

	pidFile := filepath.Join(dir, "children.pid")
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0o644); err != nil {
		t.Fatal(err)
	}
	killLeftovers("/proc", pidFile, 50*time.Millisecond)
	awaitGone(t, gone, "Prozess, der SIGTERM ignoriert,")
}

func TestPidRecorderKeepsLastGroups(t *testing.T) {
	path := filepath.Join(t.TempDir(), "children.pid")
	r := &pidRecorder{path: path}
	for pid := 100; pid < 100+maxTrackedGroups+3; pid++ {
		r.add(pid)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(string(b))
	if len(lines) != maxTrackedGroups || lines[len(lines)-1] != strconv.Itoa(100+maxTrackedGroups+2) {
		t.Fatalf("Inhalt %q", b)
	}
}

type pidProcess struct {
	client.Process
	pid int
}

func (p pidProcess) Pid() int { return p.pid }

type stubRunner struct {
	p   client.Process
	err error
}

func (r stubRunner) Start(client.Command) (client.Process, error) { return r.p, r.err }

func TestTrackingRunnerRecordsOnlyProcessesWithPid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "children.pid")
	rec := &pidRecorder{path: path}
	if _, err := (trackingRunner{stubRunner{p: &fakeProc{}}, rec}).Start(client.Command{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("Prozess ohne Pid aufgezeichnet")
	}
	if _, err := (trackingRunner{stubRunner{p: pidProcess{pid: 77}}, rec}).Start(client.Command{}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if strings.TrimSpace(string(b)) != "77" {
		t.Fatalf("Inhalt %q", b)
	}
	if _, err := (trackingRunner{stubRunner{err: errors.New("kaputt")}, rec}).Start(client.Command{}); err == nil {
		t.Fatal("Fehler verschluckt")
	}
}
