//go:build windows

// pdh-terminal-kiosk ist der Kiosk-Helfer des Windows-Setups. "-setup -app <Ordner>" macht den PC zum Kiosk
// (Benutzer PDH-Kiosk, Autologon, Shell, Richtlinien, Rechte am Datenordner); "-undo" nimmt alles zurück und ist
// wiederholbar; "-acl" setzt nur die Rechte des Datenordners für den Kiosk neu (nach einem Kiosk-Update, wenn das
// Setup den Ordner auf "Benutzer: Ändern" gesetzt hat). Protokoll: logkiosk-setup.log. Rückgabewert: 0 Erfolg, 2 Edition nicht unterstützt, 1 sonst. Es läuft nur als Administrator auf
// dem Zielgerät und nie auf einem Entwicklungsrechner.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"hmikiosk/internal/winkiosk"
)

const (
	exitOK          = 0
	exitFail        = 1
	exitUnsupported = 2
)

func main() { os.Exit(run(os.Args[1:], os.Stderr)) }

func run(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("pdh-terminal-kiosk", flag.ContinueOnError)
	fs.SetOutput(stderr)
	setup := fs.Bool("setup", false, "Kiosk einrichten")
	undo := fs.Bool("undo", false, "Kiosk zurücknehmen")
	acl := fs.Bool("acl", false, "nur die Rechte des Datenordners für den Kiosk neu setzen")
	app := fs.String("app", "", "Programmordner (für -setup)")
	if err := fs.Parse(args); err != nil {
		return exitFail
	}
	n := 0
	for _, b := range []bool{*setup, *undo, *acl} {
		if b {
			n++
		}
	}
	if n != 1 || (*setup && *app == "") {
		fmt.Fprintln(stderr, "Aufruf: pdh-terminal-kiosk -setup -app <Ordner> | -undo | -acl")
		return exitFail
	}

	logPath := winkiosk.LogPath()
	var lw io.Writer
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err == nil {
		if f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			defer f.Close()
			lw = f
		}
	}
	sys := winkiosk.NewSystem(lw)
	steps := winkiosk.Plan(*app)

	if *acl {
		sys.Logf("-acl")
		if err := winkiosk.ReapplyACL(sys); err != nil {
			sys.Logf("Rechte des Datenordners gescheitert: %v", err)
			fmt.Fprintln(stderr, err)
			return exitFail
		}
		sys.Logf("Rechte des Datenordners gesetzt")
		return exitOK
	}
	if *undo {
		sys.Logf("-undo")
		if err := winkiosk.Undo(steps, sys); err != nil {
			sys.Logf("Rückbau gescheitert: %v", err)
			fmt.Fprintln(stderr, err)
			return exitFail
		}
		sys.Logf("Rückbau fertig")
		return exitOK
	}

	sys.Logf("-setup -app %s", *app)
	edition, err := winkiosk.Edition()
	if err != nil {
		sys.Logf("Edition nicht lesbar: %v", err)
		fmt.Fprintln(stderr, "Edition nicht lesbar:", err)
		return exitFail
	}
	if !winkiosk.EditionSupported(edition) {
		sys.Logf("Edition %q wird nicht unterstützt", edition)
		fmt.Fprintln(stderr, "Der Kioskbetrieb braucht Windows 11 Pro, Enterprise (auch IoT Enterprise) oder Education.")
		return exitUnsupported
	}
	if _, err := os.Stat(filepath.Join(*app, "pdh-terminal-start.exe")); err != nil {
		sys.Logf("Starter fehlt: %v", err)
		fmt.Fprintln(stderr, "pdh-terminal-start.exe fehlt im Programmordner:", err)
		return exitFail
	}

	// Ein fremdes Konto PDH-Kiosk (keine gemerkte SID) darf der Rückbau unten nicht löschen: abbrechen.
	if err := winkiosk.PreflightSetup(sys); err != nil {
		sys.Logf("Einrichtung abgelehnt: %v", err)
		fmt.Fprintln(stderr, err)
		return exitFail
	}
	// Wiederholbar: Reste einer früheren Einrichtung zuerst wegräumen (frisches Passwort, frischer Benutzer).
	if err := winkiosk.Undo(steps, sys); err != nil {
		sys.Logf("Aufräumen vor der Einrichtung: %v", err)
	}
	if err := winkiosk.Run(steps, sys); err != nil {
		sys.Logf("Einrichtung gescheitert: %v", err)
		fmt.Fprintln(stderr, err)
		return exitFail
	}
	sys.Logf("Kiosk eingerichtet")
	return exitOK
}
