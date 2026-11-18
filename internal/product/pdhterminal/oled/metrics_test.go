package oled

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestCPUPercent(t *testing.T) {
	a, err := readCPU("cpu 100 0 100 800 0 0 0 0 0 0\ncpu0 1 2 3 4\n")
	if err != nil {
		t.Fatal(err)
	}
	b, err := readCPU("cpu 150 0 150 900 0 0 0 0 0 0\n")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := cpuPercent(a, b); !ok || math.Abs(got-50) > 1e-9 {
		t.Errorf("cpuPercent = %v, %v, want 50, true", got, ok)
	}
	if _, ok := cpuPercent(a, a); ok {
		t.Error("ohne Zeitablauf muss die Messung verworfen werden")
	}
	if _, err := readCPU("nichts"); err == nil {
		t.Error("Fehler erwartet")
	}
}

func TestReadRAM(t *testing.T) {
	got, err := readRAM("MemTotal:        1000 kB\nMemFree: 5 kB\nMemAvailable:    250 kB\n")
	if err != nil || math.Abs(got-75) > 1e-9 {
		t.Errorf("readRAM = %v, %v, want 75", got, err)
	}
	if _, err := readRAM("MemTotal: 1000 kB\n"); err == nil {
		t.Error("Fehler erwartet")
	}
}

func TestReadTemp(t *testing.T) {
	p := filepath.Join(t.TempDir(), "temp")
	if err := os.WriteFile(p, []byte("52123\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if v, ok := readTemp(p); !ok || math.Abs(v-52.123) > 1e-9 {
		t.Errorf("readTemp = %v, %v", v, ok)
	}
	if _, ok := readTemp(filepath.Join(t.TempDir(), "fehlt")); ok {
		t.Error("fehlende Datei muss false liefern")
	}
}

func TestCPUPercentIdleRegression(t *testing.T) {
	prev := cpuTimes{idle: 900, total: 1000}
	cur := cpuTimes{idle: 880, total: 1100} // iowait lief rückwärts
	if got, ok := cpuPercent(prev, cur); ok {
		t.Errorf("Idle rückläufig muss verworfen werden, bekam %v", got)
	}
}
