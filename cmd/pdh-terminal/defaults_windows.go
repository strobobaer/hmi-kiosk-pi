//go:build windows

package main

import (
	"os"
	"path/filepath"
)

// Vorgaben unter Windows. Es gibt keinen root-Helfer: Der leere Socket heißt „kein Helfer“.
var (
	defaultData         = filepath.Join(envOr("ProgramData", `C:\ProgramData`), "PDH-Terminal")
	defaultRun          = filepath.Join(envOr("LOCALAPPDATA", filepath.Join(os.TempDir(), "PDH-Terminal-local")), "PDH-Terminal", "run")
	defaultHelperSocket = ""
)

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
