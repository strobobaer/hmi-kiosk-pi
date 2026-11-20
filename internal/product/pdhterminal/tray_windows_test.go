//go:build windows

package pdhterminal

import (
	"testing"
	"time"
)

// Das echte Symbol: Menübefehle (WM_COMMAND, wie ihn die Prüfung schickt) lösen die Rückrufe aus, ein
// Neustart des Explorers (TaskbarCreated) und neue Zustände überstehen die Nachrichtenschleife, Stop beendet
// den Thread, und danach lässt sich das Symbol erneut anlegen (eine Klasse je Prozess).
func TestWinTrayCommandsAndRestart(t *testing.T) {
	for round := 0; round < 2; round++ {
		open, admin, selftest := make(chan struct{}, 1), make(chan struct{}, 1), make(chan struct{}, 1)
		tr := newTray().(*winTray)
		err := tr.Start(trayActions{
			Open:     func() { open <- struct{}{} },
			Admin:    func() { admin <- struct{}{} },
			Selftest: func() { selftest <- struct{}{} },
			Quit:     func() {},
		})
		if err != nil {
			t.Fatalf("Runde %d: %v", round, err)
		}
		h := tr.hwnd.Load()
		for cmd, ch := range map[uintptr]chan struct{}{cmdOpen: open, cmdAdmin: admin, cmdSelftest: selftest} {
			pTrPostMessage.Call(h, wmCommand, cmd, 0)
			select {
			case <-ch:
			case <-time.After(3 * time.Second):
				t.Fatalf("Runde %d: Befehl %d kam nicht an", round, cmd)
			}
		}
		tr.SetState("Edge fehlt")
		pTrPostMessage.Call(h, uintptr(trTaskbarMsg.Load()), 0, 0)
		tr.SetState("")
		tr.Stop()
		select {
		case <-tr.done:
		default:
			t.Fatal("Thread läuft nach Stop")
		}
		tr.Stop() // mehrfach harmlos
	}
}
