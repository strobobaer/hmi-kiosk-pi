package tailnet

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ExecOut startet ein Programm ohne Shell und liefert seine Standardausgabe.
type ExecOut func(ctx context.Context, name string, args ...string) (string, error)

// DefaultBin ist der Installationsort von tailscale.exe (Windows).
func DefaultBin() string {
	pf := os.Getenv("ProgramFiles")
	if pf == "" {
		pf = `C:\Program Files`
	}
	return filepath.Join(pf, "Tailscale", "tailscale.exe")
}

type cli struct {
	bin string
	run ExecOut
}

// NewCLI steuert tailscale.exe (Windows). bin leer: %ProgramFiles%\Tailscale\tailscale.exe. Fehlt die Datei,
// meldet Status "nicht installiert".
func NewCLI(bin string, run ExecOut) Ctl {
	if bin == "" {
		bin = DefaultBin()
	}
	return &cli{bin: bin, run: run}
}

func (c *cli) installed() bool {
	fi, err := os.Stat(c.bin)
	return err == nil && fi.Mode().IsRegular()
}

var errNotInstalled = errors.New("Tailscale ist auf diesem Gerät nicht installiert.")

func (c *cli) Status(ctx context.Context) (Status, error) {
	if !c.installed() {
		return Status{}, nil
	}
	out, err := c.run(ctx, c.bin, "status", "--json")
	if st, perr := ParseStatus([]byte(out)); perr == nil {
		return st, nil // auch ein beendeter Dienst liefert den Stand
	}
	if err != nil {
		return Status{}, errors.New("Tailscale-Status nicht abrufbar: " + err.Error())
	}
	return Status{}, errors.New("Tailscale-Status nicht lesbar")
}

// Up meldet mit dem Auth-Key an. Der Key liegt nur in einer Datei, die nur der angemeldete Benutzer anlegen
// und lesen soll: os.CreateTemp legt sie im Temp-Ordner des Benutzers (%TMP%, per Profil nur für ihn und
// Administratoren lesbar) mit Modus 0600 an. Unter Windows bedeutet 0600 wenig (Go setzt nur das
// Schreibschutz-Attribut); der Schutz kommt aus der ACL des Profils. Die Datei ist nur so lange da, wie
// tailscale läuft, und wird auf jedem Weg gelöscht. Der Key steht nie in einem Argument.
func (c *cli) Up(ctx context.Context, authKey, hostname string) error {
	if err := ValidateKey(authKey); err != nil {
		return err
	}
	if !ValidHostname(hostname) {
		return errors.New("Der Hostname ist ungültig.")
	}
	if !c.installed() {
		return errNotInstalled
	}
	f, err := os.CreateTemp("", "tailnet-key-*")
	if err != nil {
		return errors.New("Schlüsseldatei nicht anlegbar: " + err.Error())
	}
	path := f.Name()
	defer os.Remove(path)
	_ = f.Chmod(0o600) // CreateTemp legt schon mit 0600 an; unter Windows ohne Wirkung
	_, werr := f.WriteString(authKey)
	cerr := f.Close()
	if werr != nil || cerr != nil {
		return errors.New("Schlüsseldatei nicht schreibbar")
	}
	_, err = c.run(ctx, c.bin, "up", "--auth-key=file:"+path, "--hostname="+hostname, "--unattended")
	if err != nil {
		err = redact(err, authKey)
		low := strings.ToLower(err.Error())
		if strings.Contains(low, "access is denied") || strings.Contains(low, "access denied") || strings.Contains(low, "zugriff verweigert") {
			return errors.New("Tailscale hat den Zugriff verweigert. Zum Anmelden braucht das Terminal Administratorrechte oder unbeaufsichtigten Modus (Setup mit TS_UNATTENDEDMODE=always).")
		}
		return errors.New("Anmelden fehlgeschlagen: " + err.Error())
	}
	return nil
}

func (c *cli) Down(ctx context.Context) error {
	if !c.installed() {
		return errNotInstalled
	}
	if _, err := c.run(ctx, c.bin, "logout"); err != nil {
		low := strings.ToLower(err.Error())
		if strings.Contains(low, "access is denied") || strings.Contains(low, "access denied") {
			return errors.New("Tailscale hat den Zugriff verweigert. Zum Abmelden braucht das Terminal Administratorrechte oder unbeaufsichtigten Modus (Setup mit TS_UNATTENDEDMODE=always).")
		}
		return errors.New("Abmelden fehlgeschlagen: " + err.Error())
	}
	return nil
}
