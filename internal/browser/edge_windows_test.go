//go:build windows

package browser

import "testing"

func TestEdgeWindowWithoutJob(t *testing.T) {
	w := NewEdgeWindow(func() ([]uint32, error) { return nil, nil })
	if w.Foreground() || w.Exists() {
		t.Error("ohne Prozess gibt es kein Fenster")
	}
	if err := w.MinimizeNoActivate(); err == nil {
		t.Error("Minimieren ohne Fenster muss scheitern")
	}
	if err := w.Raise(); err == nil {
		t.Error("Raise ohne Fenster muss scheitern")
	}
}

func TestReadRegistryAppPaths(t *testing.T) {
	if _, err := ReadRegistry(`HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\msedge.exe`); err != nil {
		t.Skipf("Edge nicht installiert: %v", err)
	}
	p, err := FindEdge(ReadRegistry)
	if err != nil || p == "" {
		t.Errorf("%q %v", p, err)
	}
}
