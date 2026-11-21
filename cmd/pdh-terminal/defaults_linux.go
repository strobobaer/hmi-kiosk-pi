//go:build linux

package main

import "hmikiosk/internal/product/pdhterminal"

// Vorgaben unter Linux: Datenpartition, RAM-Ordner und Socket des root-Helfers.
const (
	defaultData         = "/data"
	defaultRun          = "/run/hmi-kiosk"
	defaultHelperSocket = pdhterminal.DefaultHelperSocket
)
