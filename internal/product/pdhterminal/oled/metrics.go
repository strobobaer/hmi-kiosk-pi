package oled

import (
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"strconv"
	"strings"
)

type cpuTimes struct{ idle, total uint64 }

// readCPU liest die Summenzeile „cpu …“ aus dem Inhalt von /proc/stat.
func readCPU(procStat string) (cpuTimes, error) {
	for _, line := range strings.Split(procStat, "\n") {
		f := strings.Fields(line)
		if len(f) < 5 || f[0] != "cpu" {
			continue
		}
		var t cpuTimes
		for i, s := range f[1:] {
			v, err := strconv.ParseUint(s, 10, 64)
			if err != nil {
				return cpuTimes{}, fmt.Errorf("cpu-Zeile: %w", err)
			}
			t.total += v
			if i == 3 || i == 4 { // idle + iowait
				t.idle += v
			}
		}
		return t, nil
	}
	return cpuTimes{}, errors.New("keine cpu-Zeile in /proc/stat")
}

// cpuPercent ist die Auslastung zwischen zwei Messungen in Prozent.
func cpuPercent(prev, cur cpuTimes) float64 {
	if cur.total <= prev.total {
		return 0
	}
	total := float64(cur.total - prev.total)
	var idle float64
	if cur.idle > prev.idle { // iowait kann rückwärts laufen
		idle = float64(cur.idle - prev.idle)
	}
	return clampPercent((total - idle) / total * 100)
}

// readRAM berechnet (MemTotal-MemAvailable)/MemTotal in Prozent aus /proc/meminfo.
func readRAM(meminfo string) (float64, error) {
	var total, avail float64
	var haveTotal, haveAvail bool
	for _, line := range strings.Split(meminfo, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		v, err := strconv.ParseFloat(f[1], 64)
		if err != nil {
			continue
		}
		switch f[0] {
		case "MemTotal:":
			total, haveTotal = v, true
		case "MemAvailable:":
			avail, haveAvail = v, true
		}
	}
	if !haveTotal || !haveAvail || total <= 0 {
		return 0, errors.New("MemTotal/MemAvailable fehlen in /proc/meminfo")
	}
	return (total - avail) / total * 100, nil
}

// readTemp liest die Temperatur in Milligrad aus path und gibt °C zurück.
func readTemp(path string) (float64, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
	if err != nil {
		return 0, false
	}
	return v / 1000, true
}

// ipAddress ermittelt die ausgehende IPv4-Adresse (UDP-Connect, es wird nichts
// gesendet); ohne Route „Offline“.
func ipAddress() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "Offline"
	}
	defer conn.Close()
	if a, ok := conn.LocalAddr().(*net.UDPAddr); ok && a.IP != nil && !a.IP.IsUnspecified() {
		return a.IP.String()
	}
	return "Offline"
}

// clampPercent begrenzt v auf 0 bis 100.
func clampPercent(v float64) float64 {
	return math.Min(100, math.Max(0, v))
}
