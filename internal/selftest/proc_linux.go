//go:build linux

package selftest

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"hmikiosk/internal/client"
)

// killGroup beendet die Prozessgruppe hart (SIGKILL) wie ein Absturz des Clients.
func killGroup(p client.Process) error {
	pp, ok := p.(pider)
	if !ok {
		return fmt.Errorf("Prozess hat keine Kennung")
	}
	if err := syscall.Kill(-pp.Pid(), syscall.SIGKILL); err != nil {
		return err
	}
	return nil
}

// pgrpOf liest die Prozessgruppe aus /proc/<pid>/stat. Der Name steht in Klammern
// und darf selbst Klammern und Leerzeichen enthalten.
func pgrpOf(procRoot string, pid int) (int, error) {
	b, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0, err
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return 0, fmt.Errorf("stat von %d unlesbar", pid)
	}
	f := strings.Fields(s[i+1:]) // state ppid pgrp ...
	if len(f) < 3 {
		return 0, fmt.Errorf("stat von %d unvollständig", pid)
	}
	return strconv.Atoi(f[2])
}

func rssOf(procRoot string, pid int) int64 {
	b, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "status"))
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "VmRSS:"); ok {
			f := strings.Fields(v)
			if len(f) >= 1 {
				kb, _ := strconv.ParseInt(f[0], 10, 64)
				return kb * 1024
			}
		}
	}
	return 0
}

// GroupRSS summiert den residenten Speicher aller Prozesse in der Prozessgruppe von pid.
func GroupRSS(procRoot string, pid int) (int64, error) {
	group, err := pgrpOf(procRoot, pid)
	if err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, e := range entries {
		n, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if g, err := pgrpOf(procRoot, n); err == nil && g == group {
			total += rssOf(procRoot, n)
		}
	}
	return total, nil
}
