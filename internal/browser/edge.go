package browser

import (
	"fmt"
	"strings"
)

// edgeAppPathsKey ist der Registry-Ort des Edge-Programms (Wert „(Standard)“).
const edgeAppPathsKey = `HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\msedge.exe`

// FindEdge liest den Pfad von msedge.exe aus der Registry; readReg liefert den
// Standardwert des Schlüssels (unter Windows ReadRegistry).
func FindEdge(readReg func(key string) (string, error)) (string, error) {
	if readReg != nil {
		if v, err := readReg(edgeAppPathsKey); err == nil {
			if v = strings.Trim(strings.TrimSpace(v), `"`); v != "" {
				return v, nil
			}
		}
	}
	return "", fmt.Errorf("Edge nicht gefunden (msedge.exe)")
}

// edgeBackgroundArgs halten die Seite im Hintergrund wach (Karte lesen, Zeitgeber) und erlauben der
// HTTPS-Seite von PDH den Zugriff auf die lokale Startseite (lokaler Netzzugriff).
var edgeBackgroundArgs = []string{
	"--disable-features=msEdgeSleepingTabs,LocalNetworkAccessChecks,PrivateNetworkAccessRespectPreflightResults",
	"--disable-background-timer-throttling", "--disable-renderer-backgrounding",
	"--disable-backgrounding-occluded-windows",
}

// EdgeArgs: Edge als App-Fenster mit eigenem Profil. Die Schalter halten die Seite im
// Hintergrund wach (Karte lesen, Zeitgeber) und erlauben der HTTPS-Seite von PDH den
// Zugriff auf die lokale Startseite (lokaler Netzzugriff).
func EdgeArgs(startURL, profileDir string) []string {
	return []string{
		"--app=" + startURL,
		"--user-data-dir=" + profileDir,
		"--no-first-run", "--no-default-browser-check",
		"--disable-features=msEdgeSleepingTabs,LocalNetworkAccessChecks,PrivateNetworkAccessRespectPreflightResults",
		"--disable-background-timer-throttling", "--disable-renderer-backgrounding",
		"--disable-backgrounding-occluded-windows",
	}
}

// EdgeKioskArgs: Edge im Vollbild-Kiosk (Windows-Kiosk-Benutzer) statt als App-Fenster, mit eigenem
// Profil und denselben Hintergrund-Schaltern wie EdgeArgs.
func EdgeKioskArgs(startURL, profileDir string) []string {
	a := []string{
		"--kiosk", startURL, "--edge-kiosk-type=fullscreen",
		"--no-first-run", "--user-data-dir=" + profileDir,
		"--no-default-browser-check",
	}
	return append(a, edgeBackgroundArgs...)
}
