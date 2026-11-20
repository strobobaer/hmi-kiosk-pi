//go:build !windows

// Der Starter gibt es nur unter Windows; unter Linux startet systemd das Programm.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "pdh-terminal-start gibt es nur unter Windows")
	os.Exit(2)
}
