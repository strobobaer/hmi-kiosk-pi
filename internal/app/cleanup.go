package app

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"hmikiosk/internal/client"
)

const (
	pidFileName      = "children.pid"
	maxTrackedGroups = 8
	leftoverGrace    = 3 * time.Second
)

// Nur Prozesse mit einem dieser Namen werden nach einem Absturz beendet: Die
// Nummer in der PID-Datei kann längst einem fremden Prozess gehören.
var leftoverNames = [][]byte{[]byte("chromium"), []byte("remmina"), []byte("vncviewer")}

// pidRecorder führt die letzten Prozessgruppen von Client und Browser. Beendete
// Gruppen bleiben stehen: Chromium-Kinder überleben ihren Anführer, und der Start
// prüft jede Gruppe ohnehin, bevor er ein Signal schickt.
type pidRecorder struct {
	path string
	mu   sync.Mutex
	pids []int
}

func (r *pidRecorder) add(pid int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pids = append(r.pids, pid)
	if len(r.pids) > maxTrackedGroups {
		r.pids = r.pids[len(r.pids)-maxTrackedGroups:]
	}
	var b strings.Builder
	for _, p := range r.pids {
		fmt.Fprintln(&b, p)
	}
	if err := writeAtomic(r.path, []byte(b.String()), 0o644); err != nil {
		log.Printf("Prozessliste nicht geschrieben: %v", err)
	}
}

// trackingRunner notiert jede gestartete Prozessgruppe.
type trackingRunner struct {
	inner client.Runner
	rec   *pidRecorder
}

func (t trackingRunner) Start(c client.Command) (client.Process, error) {
	p, err := t.inner.Start(c)
	if err != nil {
		return nil, err
	}
	if withPid, ok := p.(interface{ Pid() int }); ok {
		t.rec.add(withPid.Pid())
	}
	return p, nil
}

// killLeftovers beendet die Gruppen aus der PID-Datei eines abgestürzten Vorgängers.
func killLeftovers(procRoot, pidFile string, grace time.Duration) {
	data, err := os.ReadFile(pidFile)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("Prozessliste nicht lesbar: %v", err)
		}
		return
	}
	for _, field := range strings.Fields(string(data)) {
		pgid, err := strconv.Atoi(field)
		// Kill(-1) träfe alle Prozesse, die eigene Gruppe den Dienst selbst.
		if err != nil || pgid <= 1 || pgid == ownGroup() {
			continue
		}
		if verified, _ := inspectGroup(procRoot, pgid); !verified {
			continue
		}
		log.Printf("Beende übrig gebliebene Prozessgruppe %d", pgid)
		stopGroup(procRoot, pgid, grace)
	}
	if err := os.Remove(pidFile); err != nil {
		log.Printf("Prozessliste nicht entfernt: %v", err)
	}
}

func stopGroup(procRoot string, pgid int, grace time.Duration) {
	if err := killGroup(pgid, false); err != nil {
		log.Printf("Prozessgruppe %d: %v", pgid, err)
		return
	}
	if waitGroupGone(procRoot, pgid, grace) {
		return
	}
	if err := killGroup(pgid, true); err != nil {
		log.Printf("Prozessgruppe %d: %v", pgid, err)
		return
	}
	waitGroupGone(procRoot, pgid, grace)
}

func waitGroupGone(procRoot string, pgid int, limit time.Duration) bool {
	deadline := time.Now().Add(limit)
	for {
		if _, alive := inspectGroup(procRoot, pgid); !alive {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// inspectGroup sucht die lebenden Mitglieder der Gruppe. verified: Mindestens
// eines trägt einen erwarteten Namen. Der Anführer allein reicht nicht, er kann
// vor seinen Kindern geendet haben.
func inspectGroup(procRoot string, pgid int) (verified, alive bool) {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return false, false
	}
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		group, zombie, ok := readProcStat(filepath.Join(procRoot, e.Name(), "stat"))
		if !ok || group != pgid || zombie {
			continue
		}
		alive = true
		cmdline, err := os.ReadFile(filepath.Join(procRoot, e.Name(), "cmdline"))
		if err != nil {
			continue
		}
		for _, name := range leftoverNames {
			if bytes.Contains(cmdline, name) {
				verified = true
			}
		}
	}
	return verified, alive
}

// readProcStat liest Prozessgruppe und Zustand. Der Programmname in Klammern darf
// Leerzeichen enthalten, deshalb zählen die Felder erst ab der letzten Klammer.
func readProcStat(path string) (group int, zombie, ok bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false, false
	}
	i := bytes.LastIndexByte(data, ')')
	if i < 0 {
		return 0, false, false
	}
	fields := strings.Fields(string(data[i+1:]))
	if len(fields) < 3 {
		return 0, false, false
	}
	group, err = strconv.Atoi(fields[2])
	if err != nil {
		return 0, false, false
	}
	return group, fields[0] == "Z", true
}

// writeAtomic schreibt über eine temporäre Datei, damit Leser nie einen halben
// Inhalt sehen und ein vorhandener Verweis ersetzt statt verfolgt wird.
func writeAtomic(path string, data []byte, perm os.FileMode) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Chmod(perm); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
