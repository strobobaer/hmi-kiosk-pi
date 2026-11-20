// Package winstart ist die Logik des Starters pdh-terminal-start.exe (Windows): Er wählt die höchste
// gültig signierte Version des PDH-Terminals, startet und überwacht sie und fällt bei ausbleibender
// Gesundmeldung auf die nächstniedrigere zurück. Wahl, Prüfung und Überwachung sind plattformneutral
// und damit überall testbar; Prozessstart und Mutex stehen in run_windows.go.
package winstart

import (
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"hmikiosk/internal/release"
	"hmikiosk/internal/update"
)

const (
	// ExeName ist die Programmdatei des PDH-Terminals unter Windows.
	ExeName = "pdh-terminal.exe"
	// PackageName ist das Paket neben der Exe im Versionsordner (schreibt update.Install).
	PackageName = "package.pkg"

	// Rückgabewerte des Programms (müssen denen in internal/product/pdhterminal gleichen; ein Test dort prüft das).
	ExitQuit    = 3
	ExitRestart = 4

	maxPackageBytes = 300 << 20
)

// Candidate ist eine startbare Version: die Grundversion (Base) aus Program Files oder ein Ordner unter app\.
type Candidate struct {
	Version, Exe, Pkg string
	Base, Bad         bool
}

// CompareVersions vergleicht x.y.z numerisch (negativ: a kleiner, 0 gleich, positiv: a größer).
// Ungültige Angaben sind kleiner als jede gültige.
func CompareVersions(a, b string) int {
	if !update.ValidVersion(a) || !update.ValidVersion(b) {
		switch {
		case update.ValidVersion(a):
			return 1
		case update.ValidVersion(b):
			return -1
		}
		return 0
	}
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := range pa {
		x, y := strings.TrimLeft(pa[i], "0"), strings.TrimLeft(pb[i], "0")
		if len(x) != len(y) {
			return len(x) - len(y)
		}
		if c := strings.Compare(x, y); c != 0 {
			return c
		}
	}
	return 0
}

// Choose wählt die höchste Version, die nicht Bad ist und die verify besteht. Die Grundversion
// wird nicht geprüft: Sie liegt in Program Files und ist nur für Administratoren beschreibbar. Bei
// gleicher Version gewinnt die Grundversion. Versionsordner unter der Grundversion kommen nie an die
// Reihe, weil die Grundversion vorher gilt.
func Choose(cands []Candidate, verify func(Candidate) error) (Candidate, error) {
	sorted := slices.Clone(cands)
	slices.SortStableFunc(sorted, func(a, b Candidate) int {
		if c := CompareVersions(b.Version, a.Version); c != 0 {
			return c
		}
		switch {
		case a.Base && !b.Base:
			return -1
		case b.Base && !a.Base:
			return 1
		}
		return 0
	})
	for _, c := range sorted {
		if c.Bad {
			continue
		}
		if !c.Base {
			if err := verify(c); err != nil {
				log.Printf("Version %s übersprungen: %v", c.Version, err)
				continue
			}
		}
		return c, nil
	}
	return Candidate{}, errors.New("keine startbare Version gefunden")
}

// Discover sammelt die Kandidaten: die Grundversion (nur wenn base\pdh-terminal.exe existiert) und
// jeden Ordner app\<x.y.z>. Ordner ohne gültigen Versionsnamen und Verweise werden ignoriert.
func Discover(base, baseVersion, app string) []Candidate {
	var out []Candidate
	exe := filepath.Join(base, ExeName)
	if fi, err := os.Stat(exe); err == nil && fi.Mode().IsRegular() {
		out = append(out, Candidate{Version: baseVersion, Exe: exe, Base: true})
	}
	ents, _ := os.ReadDir(app)
	for _, e := range ents {
		if !e.IsDir() || !update.ValidVersion(e.Name()) {
			continue
		}
		dir := filepath.Join(app, e.Name())
		_, badErr := os.Lstat(filepath.Join(dir, update.BadMarker))
		out = append(out, Candidate{
			Version: e.Name(),
			Exe:     filepath.Join(dir, ExeName),
			Pkg:     filepath.Join(dir, PackageName),
			Bad:     badErr == nil,
		})
	}
	return out
}

// MarkKnownBad setzt Bad bei den Ordnerkandidaten, die isBad kennt (nil: keine).
func MarkKnownBad(cands []Candidate, isBad func(version string) bool) []Candidate {
	if isBad == nil {
		return cands
	}
	for i := range cands {
		if !cands[i].Base && isBad(cands[i].Version) {
			cands[i].Bad = true
		}
	}
	return cands
}

// Lock hält die Exe eines geprüften Kandidaten ohne Schreibfreigabe geöffnet, bis der Prozess läuft.
type Lock struct{ f *os.File }

// Close gibt die Exe frei; mehrfach und auf nil aufrufbar.
func (l *Lock) Close() error {
	if l == nil || l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}

// VerifyCandidate prüft einen Versionsordner (nicht die Grundversion) und gibt die Exe danach wieder frei.
func VerifyCandidate(c Candidate, pub ed25519.PublicKey) error {
	l, err := LockCandidate(c, pub)
	l.Close()
	return err
}

// LockCandidate prüft:
//   - die Signatur von package.pkg mit pub, Produkt pdh-terminal, Plattform windows-amd64,
//   - dass die Version im Paket dem Ordnernamen und c.Version entspricht,
//   - dass der SHA-256 der Exe im Paket dem der Datei c.Exe gleicht.
//
// Die Exe wird dazu ohne Schreib-, Lösch- und Umbenennfreigabe geöffnet; der Lock hält sie bis
// zum Start des Prozesses, damit sie zwischen Prüfung und Start nicht ersetzt werden kann.
func LockCandidate(c Candidate, pub ed25519.PublicKey) (*Lock, error) {
	dir := filepath.Dir(c.Exe)
	if !update.ValidVersion(c.Version) || filepath.Base(dir) != c.Version {
		return nil, fmt.Errorf("Ordnername %q passt nicht zur Version %s", filepath.Base(dir), c.Version)
	}
	if err := regularNoLink(dir, true); err != nil {
		return nil, err
	}
	blob, err := readLimited(c.Pkg)
	if err != nil {
		return nil, fmt.Errorf("Paket %s nicht lesbar: %v", PackageName, err)
	}
	p, err := update.Verify(blob, pub)
	if err != nil {
		return nil, err
	}
	switch {
	case p.Platform != release.PlatformWindowsAMD64:
		return nil, errors.New("Paket ist für ein anderes Betriebssystem")
	case p.Product != release.PDHTerminal.ID:
		return nil, errors.New("Paket gehört nicht zum PDH-Terminal")
	case p.Version != c.Version:
		return nil, fmt.Errorf("Version im Paket (%s) passt nicht zum Ordner (%s)", p.Version, c.Version)
	}
	f, err := openNoWriteShare(c.Exe)
	if err != nil {
		return nil, fmt.Errorf("Programmdatei nicht prüfbar: %v", err)
	}
	l := &Lock{f: f}
	ok := false
	defer func() {
		if !ok {
			l.Close()
		}
	}()
	// Nach dem Öffnen ist die Datei festgehalten; jetzt darf sie kein Verweis sein und muss die Datei sein,
	// die der Pfad bezeichnet (ein Verweis ließe sich nach der Prüfung umbiegen).
	if err := regularNoLink(dir, true); err != nil {
		return nil, err
	}
	if err := regularNoLink(c.Exe, false); err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if pi, err := os.Stat(c.Exe); err != nil || !os.SameFile(fi, pi) {
		return nil, errors.New("Programmdatei ist nicht die Datei des Pfads")
	}
	h := sha256.New()
	if _, err := io.Copy(h, io.NewSectionReader(f, 0, fi.Size())); err != nil {
		return nil, fmt.Errorf("Programmdatei nicht lesbar: %v", err)
	}
	var sum [sha256.Size]byte
	h.Sum(sum[:0])
	if sum != p.BinarySum {
		return nil, errors.New("Prüfsumme der Programmdatei stimmt nicht mit dem Paket überein")
	}
	ok = true
	return l, nil
}

// regularNoLink lehnt Verweise und Verbindungspunkte ab; dir sagt, ob ein Ordner erwartet wird.
func regularNoLink(path string, dir bool) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if fi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 || fi.IsDir() != dir {
		return fmt.Errorf("%s ist keine gewöhnliche Datei bzw. kein gewöhnlicher Ordner", filepath.Base(path))
	}
	return nil
}

func readLimited(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxPackageBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxPackageBytes {
		return nil, errors.New("Paket zu groß")
	}
	return b, nil
}

// MarkBad kennzeichnet den Versionsordner als schlecht (nie für die Grundversion).
func MarkBad(c Candidate) error {
	if c.Base {
		return nil
	}
	return os.WriteFile(filepath.Join(filepath.Dir(c.Exe), update.BadMarker), []byte("keine Gesundmeldung\n"), 0o644)
}

// Cleanup räumt nach einem gesunden Start von running (Version) in app auf: Es bleiben die laufende Version
// und die nächstniedrigere gültige (nicht als schlecht markierte) Version ab der Grundversion; ältere Ordner,
// Ordner unter der Grundversion, schlechte Ordner unterhalb und Reste von .tmp- werden gelöscht.
// Höhere Ordner bleiben unberührt (neu installiert, noch nicht gestartet, oder schlecht markiert). Fehler beim
// Löschen werden gesammelt zurückgegeben; die übrigen Ordner werden trotzdem bearbeitet.
func Cleanup(app, baseVersion, running string) error {
	ents, err := os.ReadDir(app)
	if errors.Is(err, os.ErrNotExist) {
		return nil // noch kein Update installiert
	}
	if err != nil {
		return err
	}
	keep := ""
	for _, e := range ents {
		n := e.Name()
		if !e.IsDir() || !update.ValidVersion(n) || CompareVersions(n, running) >= 0 || CompareVersions(n, baseVersion) < 0 {
			continue
		}
		if _, err := os.Lstat(filepath.Join(app, n, update.BadMarker)); err == nil {
			continue
		}
		if keep == "" || CompareVersions(n, keep) > 0 {
			keep = n
		}
	}
	var errs []string
	for _, e := range ents {
		n := e.Name()
		if !e.IsDir() {
			continue
		}
		del := strings.HasPrefix(n, ".tmp-")
		if update.ValidVersion(n) && CompareVersions(n, running) < 0 && n != keep {
			del = true
		}
		if !del {
			continue
		}
		if err := os.RemoveAll(filepath.Join(app, n)); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}
