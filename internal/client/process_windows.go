//go:build windows

package client

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type execRunner struct{}

// NewExecRunner startet Prozesse in einem eigenen Job-Objekt mit „Kill on close“: Endet
// der Dienst, endet auch jeder Prozess des Jobs samt seinen Kindern.
func NewExecRunner() Runner { return execRunner{} }

type execProcess struct {
	cmd  *exec.Cmd
	job  windows.Handle
	done chan error
	exit chan struct{}

	mu     sync.Mutex // schützt job gegen gleichzeitiges Schließen
	closed bool
}

func (execRunner) Start(c Command) (Process, error) {
	job, err := newKillOnCloseJob()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(c.Path, c.Args...)
	cmd.Env = mergeEnv(os.Environ(), c.Env)
	if err := cmd.Start(); err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	if err := assignToJob(job, cmd.Process.Pid); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = windows.CloseHandle(job)
		return nil, fmt.Errorf("Prozess nicht an das Job-Objekt gebunden: %w", err)
	}
	p := &execProcess{cmd: cmd, job: job, done: make(chan error, 1), exit: make(chan struct{})}
	go func() {
		err := cmd.Wait()
		close(p.exit)
		p.done <- err
		close(p.done)
	}()
	return p, nil
}

func newKillOnCloseJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("Job-Objekt: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return 0, fmt.Errorf("Job-Objekt: %w", err)
	}
	return job, nil
}

func assignToJob(job windows.Handle, pid int) error {
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return windows.AssignProcessToJobObject(job, h)
}

// jobBasicAccounting ist JOBOBJECT_BASIC_ACCOUNTING_INFORMATION (x/sys/windows kennt sie nicht).
type jobBasicAccounting struct {
	TotalUserTime, TotalKernelTime, ThisPeriodTotalUserTime, ThisPeriodTotalKernelTime int64
	TotalPageFaultCount, TotalProcesses, ActiveProcesses, TotalTerminatedProcesses     uint32
}

const jobObjectBasicAccountingInformation = 1

func (p *execProcess) Done() <-chan error { return p.done }

// Pid ist die Kennung des gestarteten Prozesses.
func (p *execProcess) Pid() int { return p.cmd.Process.Pid }

// Signal: Windows kennt keine Signale; os.Kill beendet den Prozess selbst, alles andere
// ist ein Fehler. Nach dem Ende meldet es os.ErrProcessDone.
func (p *execProcess) Signal(sig os.Signal) error {
	select {
	case <-p.exit:
		return os.ErrProcessDone
	default:
	}
	if sig != os.Kill {
		return errors.New("Signal unter Windows nicht unterstützt")
	}
	return p.cmd.Process.Kill()
}

// ActiveProcesses liefert die Zahl der Prozesse, die im Job noch laufen. Das können mehr
// sein als der gestartete: Startet dieser nur einen Folgeprozess und endet, lebt der Job weiter.
func (p *execProcess) ActiveProcesses() (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return 0, nil
	}
	var acc jobBasicAccounting
	if err := windows.QueryInformationJobObject(p.job, jobObjectBasicAccountingInformation,
		uintptr(unsafe.Pointer(&acc)), uint32(unsafe.Sizeof(acc)), nil); err != nil {
		return 0, err
	}
	return int(acc.ActiveProcesses), nil
}

// Stop beendet den ganzen Prozessbaum des Jobs und gibt das Job-Objekt frei; es ist
// mehrfach und nebenläufig aufrufbar. Unter Windows gibt es keine sanfte Aufforderung
// für Prozesse ohne Konsole: grace ist die Frist, nach der Stop den Ausgang des
// Hauptprozesses abwartet, nachdem der Baum beendet wurde.
func (p *execProcess) Stop(grace time.Duration) error {
	p.mu.Lock()
	if !p.closed {
		_ = windows.TerminateJobObject(p.job, 1)
		_ = windows.CloseHandle(p.job)
		p.closed = true
	}
	p.mu.Unlock()
	t := time.NewTimer(grace)
	defer t.Stop()
	select {
	case <-p.exit:
	case <-t.C:
		_ = p.cmd.Process.Kill()
		<-p.exit
	}
	return nil
}
