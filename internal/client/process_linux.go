//go:build linux

package client

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

type execRunner struct{}

func NewExecRunner() Runner { return execRunner{} }

type execProcess struct {
	cmd  *exec.Cmd
	done chan error
	exit chan struct{}
	once sync.Once
}

func mergeEnv(base, add []string) []string {
	skip := map[string]bool{}
	for _, kv := range add {
		k, _, _ := strings.Cut(kv, "=")
		skip[k] = true
	}
	out := make([]string, 0, len(base)+len(add))
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if !skip[k] {
			out = append(out, kv)
		}
	}
	return append(out, add...)
}

func (execRunner) Start(c Command) (Process, error) {
	cmd := exec.Command(c.Path, c.Args...)
	cmd.Env = mergeEnv(os.Environ(), c.Env)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	p := &execProcess{cmd: cmd, done: make(chan error, 1), exit: make(chan struct{})}
	go func() {
		err := cmd.Wait()
		close(p.exit)
		p.done <- err
		close(p.done)
	}()
	return p, nil
}

func (p *execProcess) Done() <-chan error { return p.done }

// Pid ist zugleich die Prozessgruppe (Setpgid).
func (p *execProcess) Pid() int { return p.cmd.Process.Pid }

func (p *execProcess) signalGroup(sig syscall.Signal) {
	err := syscall.Kill(-p.cmd.Process.Pid, sig)
	if err != nil && !errors.Is(err, syscall.ESRCH) {
		_ = p.cmd.Process.Signal(sig)
	}
}

// Stop ist mehrfach und nebenläufig aufrufbar.
func (p *execProcess) Stop(grace time.Duration) error {
	select {
	case <-p.exit:
		return nil
	default:
	}
	p.once.Do(func() { p.signalGroup(syscall.SIGTERM) })
	t := time.NewTimer(grace)
	defer t.Stop()
	select {
	case <-p.exit:
		return nil
	case <-t.C:
	}
	p.signalGroup(syscall.SIGKILL)
	<-p.exit
	return nil
}
