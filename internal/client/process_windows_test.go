//go:build windows

package client

import (
	"testing"
	"time"
)

type jobProcess interface {
	Process
	ActiveProcesses() (int, error)
}

// TestWindowsStopEndsWholeTree: cmd startet ping als Kind; Stop beendet beide.
func TestWindowsStopEndsWholeTree(t *testing.T) {
	p, err := NewExecRunner().Start(Command{Path: "cmd.exe", Args: []string{"/c", "ping -n 60 127.0.0.1 > nul"}})
	if err != nil {
		t.Fatal(err)
	}
	jp := p.(jobProcess)
	deadline := time.Now().Add(10 * time.Second)
	for {
		n, err := jp.ActiveProcesses()
		if err != nil {
			t.Fatal(err)
		}
		if n >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Job hat %d Prozesse, erwartet cmd und ping", n)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := p.Stop(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Done nach Stop nicht geschlossen")
	}
	if n, _ := jp.ActiveProcesses(); n != 0 {
		t.Errorf("nach Stop noch %d Prozesse", n)
	}
	if err := p.Stop(time.Second); err != nil { // mehrfach aufrufbar
		t.Errorf("zweites Stop: %v", err)
	}
}

// TestWindowsJobSurvivesMainExit: Endet der Hauptprozess, lebt der Job mit dem Kind weiter
// (Grundlage für Starter, die nur einen Folgeprozess starten).
func TestWindowsJobSurvivesMainExit(t *testing.T) {
	p, err := NewExecRunner().Start(Command{Path: "cmd.exe", Args: []string{"/c", "start /b ping -n 60 127.0.0.1 > nul"}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop(time.Second)
	<-p.Done()
	time.Sleep(300 * time.Millisecond)
	if n, _ := p.(jobProcess).ActiveProcesses(); n < 1 {
		t.Errorf("Job leer nach Ende des Hauptprozesses: %d", n)
	}
}

// TestWindowsProcessIDs: Die Kennungen des Jobs enthalten den Startprozess.
func TestWindowsProcessIDs(t *testing.T) {
	p, err := NewExecRunner().Start(Command{Path: "cmd.exe", Args: []string{"/c", "ping -n 60 127.0.0.1 > nul"}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop(time.Second)
	ids, err := p.(interface{ ProcessIDs() ([]uint32, error) }).ProcessIDs()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, id := range ids {
		found = found || int(id) == p.(interface{ Pid() int }).Pid()
	}
	if !found {
		t.Errorf("Startprozess fehlt in %v", ids)
	}
}
