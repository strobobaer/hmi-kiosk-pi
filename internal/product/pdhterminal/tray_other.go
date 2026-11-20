//go:build !windows

package pdhterminal

import "errors"

// Unter Linux läuft das Terminal im Kiosk, ein Symbol im Infobereich gibt es dort nicht.
func newTray() tray { return nil }

func openURL(string) error { return errors.New("nur unter Windows") }
