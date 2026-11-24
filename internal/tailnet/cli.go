package tailnet

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MaxStatusBytes begrenzt, wie viel der Statusausgabe gelesen wird.
const MaxStatusBytes = 256 << 10

// upTimeout beendet "tailscale up" selbst mit einer lesbaren Meldung, bevor die Admin-Seite (60 s) aufgibt.
const upTimeout = "50s"

// Das Löschen der Schlüsseldatei wird wiederholt (Virenscanner und Indexdienst halten Dateien kurz offen).
var (
	removeFile  = os.Remove
	removeTries = 5
	removeDelay = 200 * time.Millisecond
)

// keyFilePrefix: fester Namensanfang der Schlüsseldatei im Temp-Ordner des Benutzers, damit eine nach einem
// Abbruch (Abmelden der Sitzung, Task-Manager, Stromausfall) liegengebliebene Datei wiedergefunden wird.
const keyFilePrefix = "pdh-tailnet-key-"

// staleKeyAge: Ältere Schlüsseldateien gehören zu keinem laufenden "tailscale up" mehr (Frist upTimeout: 50 s).
const staleKeyAge = time.Minute

// keyDir ist der Ordner der Schlüsseldatei (Temp-Ordner des Benutzers); im Test ersetzbar.
var keyDir = os.TempDir

// sweepStaleKeyFiles löscht liegengebliebene Schlüsseldateien (älter als staleKeyAge). Im Protokoll stehen nur
// Pfade, nie Inhalte; Verknüpfungen und Ordner bleiben unberührt.
func sweepStaleKeyFiles(now time.Time) {
	dir := keyDir()
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range ents {
		if !strings.HasPrefix(e.Name(), keyFilePrefix) || !e.Type().IsRegular() {
			continue
		}
		fi, err := e.Info()
		if err != nil || now.Sub(fi.ModTime()) < staleKeyAge {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			log.Printf("tailnet: alte Schlüsseldatei nicht löschbar: %s", p)
			continue
		}
		log.Printf("tailnet: alte Schlüsseldatei gelöscht: %s", p)
	}
}

// removeKeyFile löscht die Schlüsseldatei; misslingt es, steht nur die Tatsache und der Pfad im Protokoll.
func removeKeyFile(path string) {
	for i := 0; i < removeTries; i++ {
		err := removeFile(path)
		if err == nil || os.IsNotExist(err) {
			return
		}
		if i+1 < removeTries {
			time.Sleep(removeDelay)
		}
	}
	log.Printf("tailnet: Schlüsseldatei konnte nicht gelöscht werden: %s", path)
}

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
// meldet Status "nicht installiert". Beim Anlegen (Start des Terminals) werden liegengebliebene Schlüsseldateien
// gelöscht.
func NewCLI(bin string, run ExecOut) Ctl {
	if bin == "" {
		bin = DefaultBin()
	}
	sweepStaleKeyFiles(time.Now())
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
	out, err := c.run(ctx, c.bin, "status", "--json", "--peers=false")
	if len(out) > MaxStatusBytes {
		out = out[:MaxStatusBytes]
	}
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
// tailscale läuft, und wird auf jedem Weg gelöscht; bleibt sie nach einem Abbruch liegen, löscht sie der nächste
// Start oder das nächste Anmelden (sweepStaleKeyFiles). Der Key steht nie in einem Argument.
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
	sweepStaleKeyFiles(time.Now())
	f, err := os.CreateTemp(keyDir(), keyFilePrefix+"*")
	if err != nil {
		return errors.New("Schlüsseldatei nicht anlegbar: " + err.Error())
	}
	path := f.Name()
	defer removeKeyFile(path)
	_ = f.Chmod(0o600) // CreateTemp legt schon mit 0600 an; unter Windows ohne Wirkung
	_, werr := f.WriteString(authKey)
	cerr := f.Close()
	if werr != nil || cerr != nil {
		return errors.New("Schlüsseldatei nicht schreibbar")
	}
	// Der Key wird auch aus der Ausgabe von tailscale entfernt, bevor diese gekürzt wird (Scrub im Runner).
	_, err = c.run(WithSecret(ctx, authKey), c.bin, "up", "--reset", "--auth-key=file:"+path, "--hostname="+hostname, "--unattended", "--timeout="+upTimeout)
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
