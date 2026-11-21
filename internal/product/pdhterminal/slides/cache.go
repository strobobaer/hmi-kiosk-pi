// Package slides hält den Offline-Zwischenspeicher der Folienschau eines globalen Terminals:
// Die PDH-Seite im Modus global schickt ihre aktuelle slides.json samt Bildern an das lokale
// Terminalprogramm. Liegt ein Zwischenspeicher vor, gilt das Terminal als globales Terminal.
package slides

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Grenzen des Zwischenspeichers.
const (
	MaxBody      = 60 << 20 // größter Inhalt einer Übertragung (Bytes)
	MaxImage     = 5 << 20  // je Bild (decodiert)
	MaxImages    = 50 << 20 // alle Bilder zusammen (decodiert)
	maxSlides    = 200
	maxListItems = 100
)

var (
	// ErrTooLarge: Inhalt oder Bild über der Grenze.
	ErrTooLarge = errors.New("Zwischenspeicher zu groß")
	// ErrInvalid: kein gültiger Zwischenspeicher.
	ErrInvalid = errors.New("Zwischenspeicher ungültig")
)

const (
	pointerFile = "current"
	metaFile    = "meta.json"
	imgDir      = "img"
)

var (
	idRe  = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	genRe = regexp.MustCompile(`^v-[0-9a-f]{16,}$`)
)

// Snapshot ist ein gespeicherter Stand: Folien (image_url zeigt auf /slides/img/{id}), Version, Zeit.
type Snapshot struct {
	Version string           `json:"version"`
	SavedAt string           `json:"saved_at"`
	Slides  []map[string]any `json:"slides"`
}

// Store verwaltet den Zwischenspeicher unter Dir (<Datenordner>/slides). Ein Stand liegt in einem
// eigenen Unterordner v-…; die Datei current nennt den gültigen Stand und wird atomar ersetzt. Ein
// abgebrochener oder abgelehnter Schreibvorgang lässt den vorigen Stand unberührt.
type Store struct {
	Dir string

	mu     sync.Mutex
	rename func(oldpath, newpath string) error // von Tests ersetzbar
}

func (s *Store) ren(a, b string) error {
	if s.rename != nil {
		return s.rename(a, b)
	}
	return os.Rename(a, b)
}

type payload struct {
	Version string            `json:"version"`
	SavedAt string            `json:"saved_at"`
	Slides  []map[string]any  `json:"slides"`
	Images  map[string]string `json:"images"`
}

func invalid(what string) error {
	return fmt.Errorf("%w: %s", ErrInvalid, what)
}

// Save prüft den Inhalt (Größe, JSON, Folienarten, Bilder) und ersetzt den Zwischenspeicher atomar.
// Scheitert irgendein Schritt, bleibt der vorige Stand erhalten.
func (s *Store) Save(data []byte) error {
	if len(data) > MaxBody {
		return ErrTooLarge
	}
	var p payload
	if err := json.Unmarshal(data, &p); err != nil {
		return invalid("kein gültiges JSON")
	}
	if p.Slides == nil {
		return invalid("slides fehlt")
	}
	if len(p.Slides) > maxSlides {
		return ErrTooLarge
	}
	if p.SavedAt != "" {
		t, err := time.Parse(time.RFC3339, p.SavedAt)
		if err != nil {
			return invalid("saved_at")
		}
		p.SavedAt = t.UTC().Format(time.RFC3339)
	} else {
		p.SavedAt = time.Now().UTC().Format(time.RFC3339)
	}

	// Bilder decodieren und begrenzen.
	imgs := map[string][]byte{}
	total := 0
	for id, b64 := range p.Images {
		if !idRe.MatchString(id) {
			return invalid("Bild-ID")
		}
		if base64.StdEncoding.DecodedLen(len(b64)) > MaxImage+3 {
			return ErrTooLarge
		}
		raw, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return invalid("Bild nicht base64")
		}
		if len(raw) > MaxImage {
			return ErrTooLarge
		}
		if total += len(raw); total > MaxImages {
			return ErrTooLarge
		}
		if len(raw) == 0 || imageType(raw) == "" {
			return invalid("Bildart")
		}
		imgs[id] = raw
	}
	p.Images = nil

	// Folien prüfen; image_url zeigt auf die lokale Kopie oder entfällt.
	seen := map[string]bool{}
	used := map[string][]byte{}
	for _, sl := range p.Slides {
		id, _ := sl["id"].(string)
		kind, _ := sl["kind"].(string)
		if !idRe.MatchString(id) || seen[id] {
			return invalid("Folien-ID")
		}
		seen[id] = true
		switch kind {
		case "image", "text", "vorgaenge":
		default:
			return invalid("Folienart")
		}
		for _, k := range []string{"title", "body"} {
			if v, ok := sl[k]; ok && v != nil {
				if _, isStr := v.(string); !isStr {
					return invalid(k)
				}
			}
		}
		for _, k := range []string{"faults", "tickets"} {
			if v, ok := sl[k]; ok && v != nil {
				list, isList := v.([]any)
				if !isList || len(list) > maxListItems {
					return invalid(k)
				}
			}
		}
		delete(sl, "image_url")
		if raw, ok := imgs[id]; ok && kind == "image" {
			sl["image_url"] = "/slides/img/" + id
			used[id] = raw
		}
	}

	meta, err := json.Marshal(Snapshot{Version: p.Version, SavedAt: p.SavedAt, Slides: p.Slides})
	if err != nil {
		return invalid("nicht darstellbar")
	}
	return s.commit(meta, used)
}

// commit schreibt einen neuen Stand und schaltet erst danach auf ihn um.
func (s *Store) commit(meta []byte, imgs map[string][]byte) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err = os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	var rnd [8]byte
	if _, err = rand.Read(rnd[:]); err != nil {
		return err
	}
	gen := fmt.Sprintf("v-%016x%s", time.Now().UnixNano(), hex.EncodeToString(rnd[:]))
	dir := filepath.Join(s.Dir, gen)
	if err = os.Mkdir(dir, 0o700); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.RemoveAll(dir)
		}
	}()
	if len(imgs) > 0 {
		if err = os.Mkdir(filepath.Join(dir, imgDir), 0o700); err != nil {
			return err
		}
	}
	for id, raw := range imgs {
		if err = writeSynced(filepath.Join(dir, imgDir, id), raw); err != nil {
			return err
		}
	}
	if err = writeSynced(filepath.Join(dir, metaFile), meta); err != nil {
		return err
	}
	if err = s.writePointer(gen); err != nil {
		return err
	}
	s.prune(gen)
	return nil
}

func writeSynced(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// writePointer ersetzt die Datei current atomar (temporäre Datei, Sync, Rename, Ordner-Sync).
func (s *Store) writePointer(gen string) (err error) {
	tmp, err := os.CreateTemp(s.Dir, ".current.tmp-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.WriteString(gen); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = s.ren(tmp.Name(), filepath.Join(s.Dir, pointerFile)); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return nil // Windows kennt kein fsync auf einem Ordner (Open+Sync: Zugriff verweigert); Rename ist dort dauerhaft
	}
	d, err := os.Open(s.Dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// prune löscht alle Stände außer keep und liegengebliebene temporäre Dateien.
func (s *Store) prune(keep string) {
	ents, err := os.ReadDir(s.Dir)
	if err != nil {
		return
	}
	for _, e := range ents {
		n := e.Name()
		switch {
		case n == keep || n == pointerFile:
		case genRe.MatchString(n):
			os.RemoveAll(filepath.Join(s.Dir, n))
		case strings.HasPrefix(n, ".current.tmp-"):
			os.Remove(filepath.Join(s.Dir, n))
		}
	}
}

// current liefert den Ordner des gültigen Standes.
func (s *Store) current() (string, error) {
	b, err := os.ReadFile(filepath.Join(s.Dir, pointerFile))
	if err != nil {
		return "", err
	}
	gen := strings.TrimSpace(string(b))
	if !genRe.MatchString(gen) {
		return "", ErrInvalid
	}
	return filepath.Join(s.Dir, gen), nil
}

// Load liest den gespeicherten Stand; os.ErrNotExist, wenn keiner da ist.
func (s *Store) Load() (*Snapshot, error) {
	var lastErr error
	for i := 0; i < 2; i++ { // ein gleichzeitiges Save kann den Ordner zwischen Lesen und Öffnen ersetzen
		dir, err := s.current()
		if err != nil {
			return nil, err
		}
		b, err := os.ReadFile(filepath.Join(dir, metaFile))
		if err != nil {
			lastErr = err
			continue
		}
		var snap Snapshot
		if err := json.Unmarshal(b, &snap); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		return &snap, nil
	}
	return nil, lastErr
}

// Image liefert das Bild einer Folie und seine Art (image/jpeg, image/png, image/webp).
func (s *Store) Image(id string) ([]byte, string, error) {
	if !idRe.MatchString(id) {
		return nil, "", os.ErrNotExist
	}
	var lastErr error
	for i := 0; i < 2; i++ {
		dir, err := s.current()
		if err != nil {
			return nil, "", err
		}
		b, err := os.ReadFile(filepath.Join(dir, imgDir, id))
		if err != nil {
			lastErr = err
			continue
		}
		t := imageType(b)
		if t == "" {
			return nil, "", os.ErrNotExist
		}
		return b, t, nil
	}
	return nil, "", lastErr
}

// Clear löscht den Zwischenspeicher; ohne Zwischenspeicher ist es wirkungslos.
func (s *Store) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(filepath.Join(s.Dir, pointerFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	s.prune("")
	return nil
}

// Present sagt, ob ein gültiger Zwischenspeicher vorliegt.
func (s *Store) Present() bool {
	if s == nil {
		return false
	}
	dir, err := s.current()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(dir, metaFile))
	return err == nil
}

// imageType erkennt JPEG, PNG und WebP am Inhalt, sonst "".
func imageType(b []byte) string {
	if len(b) > 512 {
		b = b[:512]
	}
	switch t := http.DetectContentType(b); t {
	case "image/jpeg", "image/png", "image/webp":
		return t
	}
	return ""
}
