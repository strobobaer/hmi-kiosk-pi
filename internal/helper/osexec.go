package helper

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var binaryNames = []string{"mount", "umount", "blkid", "sync", "rfkill", "systemctl", "timedatectl"}
var binaryDirs = []string{"/usr/bin", "/bin", "/usr/sbin", "/sbin"}

// NewOSExec löst die benötigten Programme einmalig in festen Verzeichnissen auf
// und startet später nur diese absoluten Pfade, ohne Shell und mit leerer Umgebung.
func NewOSExec() (Exec, error) {
	run, err := newOSRunner()
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, name string, args ...string) error {
		_, err := run(ctx, name, args...)
		return err
	}, nil
}

// NewOSExecOut ist wie NewOSExec, liefert aber die Standardausgabe (für blkid).
func NewOSExecOut() (ExecOut, error) { return newOSRunner() }

func newOSRunner() (ExecOut, error) {
	paths := map[string]string{}
	for _, n := range binaryNames {
		for _, d := range binaryDirs {
			p := filepath.Join(d, n)
			if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0 {
				paths[n] = p
				break
			}
		}
		if paths[n] == "" {
			return nil, fmt.Errorf("Programm %s nicht gefunden", n)
		}
	}
	return func(ctx context.Context, name string, args ...string) (string, error) {
		p, ok := paths[name]
		if !ok {
			return "", fmt.Errorf("Programm %q nicht erlaubt", name)
		}
		cmd := exec.CommandContext(ctx, p, args...)
		cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C"}
		cmd.WaitDelay = 2 * time.Second
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			msg := strings.TrimSpace(stderr.String() + stdout.String())
			if len(msg) > 200 {
				msg = msg[:200]
			}
			if msg != "" {
				return "", fmt.Errorf("%v: %s", err, msg)
			}
			return "", err
		}
		return stdout.String(), nil
	}, nil
}
