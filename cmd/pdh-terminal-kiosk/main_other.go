//go:build !windows

// Den Kiosk-Helfer gibt es nur unter Windows.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "pdh-terminal-kiosk gibt es nur unter Windows")
	os.Exit(2)
}
