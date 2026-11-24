package pdhterminal

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"

	"hmikiosk/internal/tailnet"
)

// tailnetCtl wählt die Steuerung von Tailscale: Mit Helfer-Socket (Pi) über den root-Helfer, sonst unter
// Windows über tailscale.exe, sonst keine (kein Abschnitt „Tailnet“).
func (s *service) tailnetCtl() tailnet.Ctl {
	switch {
	case s.helper != nil:
		return tailnet.NewHelper(s.helper)
	case onWindows:
		return tailnet.NewCLI("", runTailscale)
	}
	return nil
}

// runTailscale startet tailscale.exe ohne Shell und ohne Fenster. Die Befehlszeile enthält nie den Auth-Key
// (er geht über eine Datei); die Fehlermeldung bekommt nur ein kurzes Stück der Programmausgabe.
func runTailscale(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = 2 * time.Second
	hideWindow(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String() + stdout.String())
		if len(msg) > 200 {
			msg = msg[:200]
		}
		if msg != "" {
			return stdout.String(), errors.New(err.Error() + ": " + msg)
		}
		return stdout.String(), err
	}
	return stdout.String(), nil
}
