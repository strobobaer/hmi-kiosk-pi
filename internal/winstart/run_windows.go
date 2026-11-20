//go:build windows

package winstart

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/windows"

	"hmikiosk/internal/client"
	"hmikiosk/internal/release"
	"hmikiosk/internal/update"
)

// MutexName ist der systemweite Mutex: Es läuft höchstens ein Starter je PC.
const MutexName = `Global\PDH-Terminal`

// Main ist der Starter. Es liefert den Rückgabewert des Prozesses.
func Main(args []string, def Paths) int {
	paths, pass := ParseArgs(args, def)
	if closeLog := setupLog(paths.Data); closeLog != nil {
		defer closeLog()
	}

	name, _ := windows.UTF16PtrFromString(MutexName)
	mu, err := windows.CreateMutex(nil, false, name)
	switch {
	case errors.Is(err, windows.ERROR_ALREADY_EXISTS), errors.Is(err, windows.ERROR_ACCESS_DENIED):
		// Ein anderer Starter läuft (ERROR_ACCESS_DENIED: in einer anderen Sitzung unter anderem Konto).
		log.Printf("Ein Starter läuft schon, dieser endet.")
		if mu != 0 {
			windows.CloseHandle(mu)
		}
		return 0
	case err != nil:
		log.Printf("Mutex nicht anlegbar: %v", err)
		return 1
	}
	defer windows.CloseHandle(mu)

	if err := os.MkdirAll(paths.Run, 0o755); err != nil {
		log.Printf("Laufzeitordner nicht anlegbar: %v", err)
		return 1
	}
	pub := release.PublicKey()
	if pub == nil {
		log.Printf("Kein Prüfschlüssel eingebaut: Es startet nur die Grundversion.")
	}
	baseVersion := release.Version
	if !update.ValidVersion(baseVersion) {
		baseVersion = "0.0.0"
	}
	app := filepath.Join(paths.Data, "app")
	locks := map[string]*Lock{}
	closeLocks := func(except string) {
		for v, l := range locks {
			if v != except {
				l.Close()
				delete(locks, v)
			}
		}
	}
	defer closeLocks("")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err = Supervise(ctx, Config{
		Choose: func() (Candidate, error) {
			closeLocks("")
			// Die Prüfung hält die Exe jedes geprüften Kandidaten geöffnet, bis der Prozess läuft.
			c, err := Choose(Discover(paths.Base, baseVersion, app), func(c Candidate) error {
				l, err := LockCandidate(c, pub)
				if err != nil {
					return err
				}
				locks[c.Version] = l
				return nil
			})
			closeLocks(c.Version)
			return c, err
		},
		Start: func(c Candidate) (Proc, error) {
			defer func() { closeLocks("") }() // läuft der Prozess, hält das Abbild die Datei fest
			return startProc(c, pass)
		},
		RunDir: paths.Run,
		OnHealthy: func(c Candidate) {
			if err := Cleanup(app, baseVersion, c.Version); err != nil {
				log.Printf("Aufräumen alter Versionen: %v", err)
			}
		},
	})
	if err != nil {
		log.Printf("Starter beendet: %v", err)
		return 1
	}
	return 0
}

// setupLog schreibt das Protokoll zusätzlich nach <data>\log\starter.log (höchstens 1 MiB, dann von vorn).
func setupLog(data string) func() {
	dir := filepath.Join(data, "log")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil
	}
	path := filepath.Join(dir, "starter.log")
	if fi, err := os.Stat(path); err == nil && fi.Size() > 1<<20 {
		_ = os.Remove(path)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil
	}
	log.SetOutput(io.MultiWriter(os.Stderr, f))
	return func() { f.Close() }
}

// startProc startet die Exe im Job-Objekt: Beendet der Starter den Prozess, endet der ganze Baum (Edge eingeschlossen).
func startProc(c Candidate, args []string) (Proc, error) {
	p, err := client.NewExecRunner().Start(client.Command{Path: c.Exe, Args: args})
	if err != nil {
		return nil, err
	}
	ep := &execProc{p: p, done: make(chan int, 1)}
	go func() {
		err := <-p.Done()
		code := 0
		var ee *exec.ExitError
		switch {
		case errors.As(err, &ee):
			code = ee.ExitCode()
		case err != nil:
			code = -1
		}
		ep.done <- code
	}()
	return ep, nil
}

type execProc struct {
	p    client.Process
	done chan int
}

func (e *execProc) Done() <-chan int { return e.done }

func (e *execProc) Kill() {
	if err := e.p.Stop(5 * time.Second); err != nil {
		log.Print(fmt.Errorf("Prozess nicht beendet: %w", err))
	}
}
