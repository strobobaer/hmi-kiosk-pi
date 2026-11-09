package update

import (
	"archive/tar"
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Install entpackt ein geprüftes Paket nach <root>/app/<version>. Es schaltet
// nichts um. Der Entpackvorgang läuft in einem temporären Ordner und wird erst
// am Ende umbenannt; bei einem Fehler bleibt nichts zurück.
func Install(root string, p *Package) (version string, err error) {
	if p == nil || !ValidVersion(p.Version) {
		return "", errors.New("Paket ungültig")
	}
	app := appPath(root)
	if err := os.MkdirAll(app, 0o755); err != nil {
		return "", fmt.Errorf("Programmordner nicht anlegbar: %v", err)
	}
	tmp := filepath.Join(app, ".tmp-"+p.Version)
	os.RemoveAll(tmp)
	if err := os.Mkdir(tmp, 0o755); err != nil {
		return "", fmt.Errorf("Arbeitsordner nicht anlegbar: %v", err)
	}
	defer func() {
		if err != nil {
			os.RemoveAll(tmp)
		}
	}()
	err = walk(p.targz, func(h *tar.Header, r io.Reader) error {
		name := h.Name
		if name == versionName {
			v, err := readVersion(r)
			if err != nil {
				return err
			}
			if v != p.Version {
				return errors.New("VERSION im Paket weicht ab")
			}
			return writeNew(filepath.Join(tmp, name), strings.NewReader(v+"\n"), 0o644)
		}
		return writeNew(filepath.Join(tmp, name), r, 0o755)
	})
	if err != nil {
		return "", err
	}
	if err := syncDir(tmp); err != nil {
		return "", err
	}
	dest := filepath.Join(app, p.Version)
	if _, statErr := os.Lstat(dest); statErr == nil {
		cur, err := readLink(root, currentLink)
		if err != nil {
			return "", err
		}
		prev, err := readLink(root, previousLink)
		if err != nil {
			return "", err
		}
		if p.Version == cur || p.Version == prev {
			return "", fmt.Errorf("Version %s ist bereits installiert", p.Version)
		}
		if err := os.RemoveAll(dest); err != nil {
			return "", err
		}
	}
	if err := os.Rename(tmp, dest); err != nil {
		return "", fmt.Errorf("Version nicht übernehmbar: %v", err)
	}
	if err := syncDir(app); err != nil {
		return "", err
	}
	return p.Version, nil
}

// writeNew legt die Datei neu an (nie über einen vorhandenen Eintrag hinweg).
// Weil Verify den Inhalt schon geprüft hat, hängt der Dateiname nur von der Positivliste ab.
func writeNew(path string, r io.Reader, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Prune löscht alle Versionsordner außer current und previous sowie Reste von
// abgebrochenen Entpackvorgängen.
func Prune(root string) error {
	cur, err := readLink(root, currentLink)
	if err != nil {
		return err
	}
	prev, err := readLink(root, previousLink)
	if err != nil {
		return err
	}
	ents, err := os.ReadDir(appPath(root))
	if err != nil {
		return err
	}
	var errs []string
	for _, e := range ents {
		n := e.Name()
		stale := strings.HasPrefix(n, ".tmp-") && e.IsDir()
		old := e.IsDir() && ValidVersion(n) && n != cur && n != prev
		if !stale && !old {
			continue
		}
		if err := os.RemoveAll(filepath.Join(appPath(root), n)); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// Installer prüft und installiert hochgeladene Pakete (erfüllt web.UpdateAPI).
type Installer struct {
	Root      string
	PublicKey ed25519.PublicKey
	// Activate schaltet über den Helfer um (Produktion: Aufruf update-activate).
	Activate func(ctx context.Context, version string) error
	// FreeBytes liefert den freien Platz; nil bedeutet das echte Dateisystem.
	FreeBytes func(path string) (uint64, error)
	// SystemLevel liefert den Stand der Systemteile des Geräts; nil prüft nicht.
	SystemLevel func() int

	mu sync.Mutex
}

func (in *Installer) Install(ctx context.Context, blob []byte) (string, error) {
	return in.install(ctx, blob, "")
}

// InstallExpect installiert nur, wenn das Paket genau die Version want enthält. Beim Update
// per Klick verhindert das, dass ein älteres, gültig signiertes Paket unter dem Namen der
// angebotenen Version still zurückstuft.
func (in *Installer) InstallExpect(ctx context.Context, blob []byte, want string) (string, error) {
	if !ValidVersion(want) {
		return "", errors.New("Erwartete Version ungültig")
	}
	return in.install(ctx, blob, want)
}

// install: want leer heißt jede Version (Hochladen).
func (in *Installer) install(ctx context.Context, blob []byte, want string) (string, error) {
	in.mu.Lock()
	defer in.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	p, err := Verify(blob, in.PublicKey)
	if err != nil {
		return "", err
	}
	if want != "" && p.Version != want {
		return "", fmt.Errorf("Das Paket enthält Version %s statt der angebotenen Version %s; es wurde nicht installiert.", p.Version, want)
	}
	if err := in.checkSystemLevel(p); err != nil {
		return "", err
	}
	cur, err := readLink(in.Root, currentLink)
	if err != nil {
		return "", err
	}
	if p.Version == cur {
		return "", fmt.Errorf("Version %s ist bereits aktiv", p.Version)
	}
	app := appPath(in.Root)
	if err := os.MkdirAll(app, 0o755); err != nil {
		return "", fmt.Errorf("Programmordner nicht anlegbar: %v", err)
	}
	free := in.FreeBytes
	if free == nil {
		free = FreeBytes
	}
	if err := Prune(in.Root); err != nil {
		log.Printf("Aufräumen alter Versionen: %v", err)
	}
	avail, err := free(app)
	if err != nil {
		return "", fmt.Errorf("Speicherplatz nicht prüfbar: %v", err)
	}
	if avail < 2*uint64(len(blob)) {
		return "", errors.New("Zu wenig Speicherplatz für das Update")
	}
	if in.Activate == nil {
		return "", errors.New("Aktivierung nicht eingerichtet")
	}
	version, err := Install(in.Root, p)
	if err != nil {
		return "", err
	}
	if err := in.Activate(ctx, version); err != nil {
		// Hat die Umschaltung schon stattgefunden (z. B. Neustart des Dienstes
		// scheiterte), darf die Version nicht verschwinden.
		if now, _ := readLink(in.Root, currentLink); now != version {
			os.RemoveAll(filepath.Join(app, version))
		}
		return "", err
	}
	return version, nil
}

// checkSystemLevel lehnt ein Paket ab, das neuere Systemteile braucht, als das Gerät hat:
// Ein Programm-Update tauscht nur hmi-kiosk, Helfer und /etc kommen mit Abbild oder install.sh.
func (in *Installer) checkSystemLevel(p *Package) error {
	if in.SystemLevel == nil {
		return nil
	}
	if have := in.SystemLevel(); p.SystemLevel > have {
		return fmt.Errorf("Version %s braucht neuere Systemteile (Stand %d, dieses Gerät hat Stand %d). "+
			"Ein Programm-Update reicht dafür nicht: Bitte die Konfiguration sichern und das SD-Abbild %s neu schreiben "+
			"oder install.sh aus dem Bundle %s ausführen.", p.Version, p.SystemLevel, have, p.Version, p.Version)
	}
	return nil
}
