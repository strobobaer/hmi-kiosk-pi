//go:build windows

// pdh-terminal-start ist der Starter des PDH-Terminals unter Windows: Der Autostart ruft ihn auf. Er wählt die
// höchste gültig signierte Version (Grundversion in Program Files oder Update unter %ProgramData%), startet und
// überwacht sie und fällt bei ausbleibender Gesundmeldung zurück. Alle Argumente außer -base gehen unverändert
// an pdh-terminal.exe.
package main

import (
	"os"
	"path/filepath"

	"hmikiosk/internal/winstart"
)

func main() {
	base := "."
	if exe, err := os.Executable(); err == nil {
		base = filepath.Dir(exe)
	}
	data := filepath.Join(envOr("ProgramData", `C:\ProgramData`), "PDH-Terminal")
	// Dieselben Vorgaben wie cmd/pdh-terminal/defaults_windows.go: Starter und Programm teilen den Laufzeitordner.
	run := filepath.Join(envOr("LOCALAPPDATA", filepath.Join(os.TempDir(), "PDH-Terminal-local")), "PDH-Terminal", "run")
	os.Exit(winstart.Main(os.Args[1:], winstart.Paths{Base: base, Data: data, Run: run}))
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
