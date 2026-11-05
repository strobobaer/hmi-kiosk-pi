// Package helper ist der kleine root-Helfer: eine feste Befehlsliste hinter einem
// Unix-Socket. Er nimmt nie Befehlszeilen entgegen, nur Namen aus der Liste und
// höchstens einen streng geprüften Wert.
package helper

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"hmikiosk/internal/config"
)

const (
	CmdWifiOff        = "wifi-off"
	CmdWifiOn         = "wifi-on"
	CmdBtOff          = "bt-off"
	CmdBtOn           = "bt-on"
	CmdReboot         = "reboot"
	CmdRestartService = "restart-service"
	CmdSetTime        = "set-time"
	CmdSetNTP         = "set-ntp"
	CmdUpdateActivate = "update-activate"
	CmdUpdateRollback = "update-rollback"
)

const (
	overlayWifi = "disable-wifi"
	overlayBT   = "disable-bt"

	bootMount = "/boot/firmware"
	roTimeout = 10 * time.Second

	maxNTPStateBytes = 300
	maxVersionLen    = 32
	timeLayout       = "2006-01-02 15:04:05"
)

// Exec startet ein Programm ohne Shell. name ist der Programmname, nicht ein Pfad.
type Exec func(ctx context.Context, name string, args ...string) error

// UpdateOps sind die Aktionen der Aktualisierung (Aufgabe 14).
type UpdateOps interface {
	Activate(version string) error
	Rollback() error
}

var versionRe = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

type command struct {
	takesArg bool
	run      func(s *Server, ctx context.Context, arg string) error
}

var commands = map[string]command{
	CmdWifiOff: {run: func(s *Server, ctx context.Context, _ string) error {
		return s.setRadio(ctx, overlayWifi, "wifi", true)
	}},
	CmdWifiOn: {run: func(s *Server, ctx context.Context, _ string) error {
		return s.setRadio(ctx, overlayWifi, "wifi", false)
	}},
	CmdBtOff: {run: func(s *Server, ctx context.Context, _ string) error {
		return s.setRadio(ctx, overlayBT, "bluetooth", true)
	}},
	CmdBtOn: {run: func(s *Server, ctx context.Context, _ string) error {
		return s.setRadio(ctx, overlayBT, "bluetooth", false)
	}},
	CmdReboot: {run: func(s *Server, ctx context.Context, _ string) error { return s.run(ctx, "systemctl", "reboot") }},
	CmdRestartService: {run: func(s *Server, ctx context.Context, _ string) error {
		return s.run(ctx, "systemctl", "restart", "hmi-kiosk.service")
	}},
	CmdSetTime:        {takesArg: true, run: (*Server).setTime},
	CmdSetNTP:         {takesArg: true, run: (*Server).setNTP},
	CmdUpdateActivate: {takesArg: true, run: (*Server).updateActivate},
	CmdUpdateRollback: {run: (*Server).updateRollback},
}

// Dispatch führt einen Befehl aus. Fehlertexte sind für die Admin-Seite bestimmt.
func (s *Server) Dispatch(ctx context.Context, cmd, arg string) error {
	c, ok := commands[cmd]
	if !ok {
		return errors.New("Unbekannter Befehl")
	}
	if !c.takesArg && arg != "" {
		return errors.New("Dieser Befehl nimmt keinen Wert an")
	}
	if c.takesArg && arg == "" {
		return errors.New("Der Wert fehlt")
	}
	return c.run(s, ctx, arg)
}

func (s *Server) run(ctx context.Context, name string, args ...string) error {
	if err := s.exec(ctx, name, args...); err != nil {
		return fmt.Errorf("%s fehlgeschlagen: %v", name, err)
	}
	return nil
}

// setRadio ändert das Overlay in config.txt (dauerhaft) und sperrt das Funkmodul
// sofort per rfkill. Eingeschaltet wird nach einem Neustart wirksam.
func (s *Server) setRadio(ctx context.Context, overlay, radio string, off bool) error {
	writeErr, roErr := s.updateBootConfig(ctx, overlay, off)
	if writeErr != nil {
		if roErr != nil {
			return fmt.Errorf("%v; %v", writeErr, roErr)
		}
		return writeErr
	}
	action := "unblock"
	if off {
		action = "block"
	}
	rfErr := s.run(ctx, "rfkill", action, radio)
	switch {
	case roErr != nil && rfErr != nil:
		return fmt.Errorf("%v; %v", roErr, rfErr)
	case roErr != nil:
		return roErr
	}
	return rfErr
}

// updateBootConfig liest config.txt im Normalzustand (schreibgeschützt), schreibt
// nur bei einer Änderung und stellt den Schreibschutz auch nach einem Fehler her.
func (s *Server) updateBootConfig(ctx context.Context, overlay string, enabled bool) (writeErr, roErr error) {
	orig, err := os.ReadFile(s.bootCfg)
	if err != nil {
		return fmt.Errorf("config.txt nicht lesbar: %v", err), nil
	}
	updated := SetOverlay(string(orig), overlay, enabled)
	changed := updated != string(orig)
	if changed {
		if err := s.exec(ctx, "mount", "-o", "remount,rw", bootMount); err != nil {
			return fmt.Errorf("Boot-Partition ließ sich nicht beschreibbar machen: %v", err), nil
		}
		if err := writeWithBackup(s.bootCfg, orig, []byte(updated)); err != nil {
			writeErr = fmt.Errorf("config.txt konnte nicht geschrieben werden: %v", err)
		}
	}
	// Auch ohne Änderung: ein früheres, fehlgeschlagenes ro soll nachgeholt werden.
	roCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), roTimeout)
	defer cancel()
	if err := s.exec(roCtx, "mount", "-o", "remount,ro", bootMount); err != nil {
		what := "Die Einstellung wurde geschrieben, "
		if writeErr != nil || !changed {
			what = ""
		}
		roErr = fmt.Errorf("%sdie Boot-Partition ließ sich nicht wieder schreibschützen: %v", what, err)
	}
	return writeErr, roErr
}

func (s *Server) setTime(ctx context.Context, arg string) error {
	t, err := time.Parse(time.RFC3339, arg)
	if err != nil {
		return errors.New("Uhrzeit ungültig")
	}
	if t.Year() < 2020 || t.Year() > 2100 {
		return errors.New("Jahr außerhalb des gültigen Bereichs")
	}
	// set-time scheitert bei aktiver NTP-Synchronisation.
	if err := s.run(ctx, "timedatectl", "set-ntp", "false"); err != nil {
		return err
	}
	return s.run(ctx, "timedatectl", "set-time", t.Local().Format(timeLayout))
}

func (s *Server) setNTP(ctx context.Context, arg string) error {
	if !config.ValidNTPServer(arg) {
		return errors.New("NTP-Server ungültig")
	}
	// Zuerst der dauerhafte Wert: scheitert er, bleibt /etc unberührt. Der Ordner liegt
	// auf /data, das dem Dienstbenutzer gehört; writeStateFile prüft ihn.
	if err := writeStateFile(s.NTPStatePath, []byte(arg+"\n")); err != nil {
		return fmt.Errorf("NTP-Wert nicht speicherbar: %v", err)
	}
	return s.applyNTP(ctx, arg)
}

// applyNTP schreibt die timesyncd-Datei (das Wurzeldateisystem ist ein Overlay,
// der Wert steht zusätzlich auf der Datenpartition) und startet die Zeitsynchronisation neu.
func (s *Server) applyNTP(ctx context.Context, server string) error {
	if err := os.MkdirAll(filepath.Dir(s.NTPConfPath), 0o755); err != nil {
		return fmt.Errorf("NTP-Einstellung nicht schreibbar: %v", err)
	}
	if err := writeAtomic(s.NTPConfPath, []byte("[Time]\nNTP="+server+"\n"), 0o644); err != nil {
		return fmt.Errorf("NTP-Einstellung nicht schreibbar: %v", err)
	}
	if err := s.run(ctx, "timedatectl", "set-ntp", "true"); err != nil {
		return err
	}
	return s.run(ctx, "systemctl", "restart", "systemd-timesyncd")
}

// ReapplyNTP stellt nach dem Start die gespeicherte NTP-Einstellung wieder her.
// Ohne gespeicherten Wert tut es nichts. Die Datei wird begrenzt und ohne
// Symlinks gelesen, der Ablauf hat eine Frist.
func (s *Server) ReapplyNTP(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	raw, err := readStateFile(s.NTPStatePath, maxNTPStateBytes)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("NTP-Wert nicht lesbar: %v", err)
	}
	server := strings.TrimSpace(string(raw))
	if !config.ValidNTPServer(server) {
		return errors.New("Gespeicherter NTP-Wert ungültig")
	}
	return s.applyNTP(ctx, server)
}

const (
	serviceUnit = "hmi-kiosk.service"
	guardUnit   = "hmi-kiosk-update-guard.service"
)

// updateActivate entfernt zuerst die alte Gesundmarke, damit sie die neue Version
// nicht fälschlich bestätigt, schaltet um, startet den Wächter und danach den
// Dienst neu (beides ohne zu warten). Scheitert der Wächterstart, gibt es keinen
// Neustart: Ohne Wächter gäbe es keinen Rückfall.
func (s *Server) updateActivate(ctx context.Context, arg string) error {
	if len(arg) > maxVersionLen || !versionRe.MatchString(arg) {
		return errors.New("Versionsangabe ungültig")
	}
	if err := os.Remove(s.HealthyMarker); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("Gesundmarke nicht entfernbar: %v", err)
	}
	if err := s.upd.Activate(arg); err != nil {
		return err
	}
	// Beide Aufrufe blockieren nicht: ein hängender Neustart darf den Wächter nie verhindern.
	if err := s.run(ctx, "systemctl", "start", "--no-block", guardUnit); err != nil {
		return err
	}
	return s.run(ctx, "systemctl", "restart", "--no-block", serviceUnit)
}

func (s *Server) updateRollback(ctx context.Context, _ string) error {
	if err := s.upd.Rollback(); err != nil {
		return err
	}
	return s.run(ctx, "systemctl", "restart", serviceUnit)
}
