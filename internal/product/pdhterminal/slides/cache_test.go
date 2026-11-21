package slides

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func pngBytes(t testing.TB) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// padPNG liefert ein gültiges PNG-Präfix, aufgefüllt auf n Bytes (die Art wird am Anfang erkannt).
func padPNG(t testing.TB, n int) []byte {
	t.Helper()
	b := pngBytes(t)
	return append(b, make([]byte, n-len(b))...)
}

func payloadJSON(t testing.TB, version string, images map[string][]byte) []byte {
	t.Helper()
	imgs := map[string]string{}
	for k, v := range images {
		imgs[k] = base64.StdEncoding.EncodeToString(v)
	}
	b, err := json.Marshal(map[string]any{
		"version":  version,
		"saved_at": "2026-11-21T10:30:00Z",
		"slides": []any{
			map[string]any{"id": "s-img", "kind": "image", "title": "Bild", "duration": 10, "image_url": "/terminal/slides/s-img/image?v=1"},
			map[string]any{"id": "s-txt", "kind": "text", "title": "<b>Hallo</b>", "body": "Text", "duration": 15},
			map[string]any{"id": "s-vor", "kind": "vorgaenge", "duration": 20, "faults_total": 1,
				"faults": []any{map[string]any{"id": "f1", "title": "Pumpe", "status": "open", "statusLabel": "Offen", "path": "", "age": "2 h"}}},
		},
		"images": imgs,
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newStore(t *testing.T) *Store { return &Store{Dir: filepath.Join(t.TempDir(), "slides")} }

func mustVersion(t *testing.T, s *Store, want string) {
	t.Helper()
	snap, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if snap.Version != want {
		t.Fatalf("Version %q, erwartet %q", snap.Version, want)
	}
}

func TestSaveLoadRoundtrip(t *testing.T) {
	s := newStore(t)
	if s.Present() {
		t.Fatal("leer: Present")
	}
	if _, err := s.Load(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Load ohne Stand: %v", err)
	}
	png := pngBytes(t)
	if err := s.Save(payloadJSON(t, "v1", map[string][]byte{"s-img": png})); err != nil {
		t.Fatal(err)
	}
	if !s.Present() {
		t.Fatal("Present fehlt")
	}
	snap, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if snap.Version != "v1" || snap.SavedAt != "2026-11-21T10:30:00Z" || len(snap.Slides) != 3 {
		t.Fatalf("%+v", snap)
	}
	if got := snap.Slides[0]["image_url"]; got != "/slides/img/s-img" {
		t.Errorf("image_url = %v", got)
	}
	if _, ok := snap.Slides[1]["image_url"]; ok {
		t.Error("Textfolie mit image_url")
	}
	b, ct, err := s.Image("s-img")
	if err != nil || ct != "image/png" || !bytes.Equal(b, png) {
		t.Errorf("Bild: %v %q", err, ct)
	}
	if _, _, err := s.Image("../meta.json"); err == nil {
		t.Error("Pfad in Bild-ID")
	}
	if _, _, err := s.Image("fehlt"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("fehlendes Bild: %v", err)
	}
}

func TestSaveWithoutImageDropsURL(t *testing.T) {
	s := newStore(t)
	if err := s.Save(payloadJSON(t, "v1", nil)); err != nil {
		t.Fatal(err)
	}
	snap, _ := s.Load()
	if _, ok := snap.Slides[0]["image_url"]; ok {
		t.Error("image_url ohne Bild muss entfallen")
	}
}

func TestSaveReplacesAndPrunesOldVersion(t *testing.T) {
	s := newStore(t)
	for _, v := range []string{"v1", "v2", "v3"} {
		if err := s.Save(payloadJSON(t, v, map[string][]byte{"s-img": pngBytes(t)})); err != nil {
			t.Fatal(err)
		}
	}
	mustVersion(t, s, "v3")
	ents, _ := os.ReadDir(s.Dir)
	if len(ents) != 2 { // current + ein Stand
		var names []string
		for _, e := range ents {
			names = append(names, e.Name())
		}
		t.Errorf("Reste: %v", names)
	}
}

// Review Focus 3: abgelehnte Zwischenspeicher lassen den vorigen Stand unberührt.
func TestRejectedKeepsOld(t *testing.T) {
	big := padPNG(t, MaxImage+1)
	var over bytes.Buffer // > 72 MB Inhalt
	over.WriteString(`{"version":"x","slides":[],"pad":"`)
	over.Write(bytes.Repeat([]byte("a"), MaxBody))
	over.WriteString(`"}`)
	cases := []struct {
		name    string
		data    []byte
		wantErr error
	}{
		{"72 MB", over.Bytes(), ErrTooLarge},
		{"Bild über 5 MB", payloadJSON(t, "neu", map[string][]byte{"s-img": big}), ErrTooLarge},
		{"kaputtes JSON", []byte(`{"version":"neu","slides":[`), ErrInvalid},
		{"halbe Übertragung", payloadJSON(t, "neu", nil)[:40], ErrInvalid},
		{"keine Folien", []byte(`{"version":"neu"}`), ErrInvalid},
		{"falsche Folienart", []byte(`{"version":"neu","slides":[{"id":"a","kind":"video"}]}`), ErrInvalid},
		{"Bild kein Bild", payloadJSON(t, "neu", map[string][]byte{"s-img": []byte("<html>nein</html>")}), ErrInvalid},
		{"Bild kein base64", []byte(`{"version":"neu","slides":[],"images":{"a":"!!!"}}`), ErrInvalid},
		{"Bild-ID mit Pfad", []byte(`{"version":"neu","slides":[],"images":{"../x":"AAAA"}}`), ErrInvalid},
		{"doppelte Folien-ID", []byte(`{"version":"neu","slides":[{"id":"a","kind":"text"},{"id":"a","kind":"text"}]}`), ErrInvalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newStore(t)
			if err := s.Save(payloadJSON(t, "alt", map[string][]byte{"s-img": pngBytes(t)})); err != nil {
				t.Fatal(err)
			}
			if err := s.Save(c.data); !errors.Is(err, c.wantErr) {
				t.Fatalf("Fehler %v, erwartet %v", err, c.wantErr)
			}
			mustVersion(t, s, "alt")
			if _, _, err := s.Image("s-img"); err != nil {
				t.Errorf("Bild des alten Standes: %v", err)
			}
			ents, _ := os.ReadDir(s.Dir)
			if len(ents) != 2 {
				t.Errorf("Reste im Ordner: %d Einträge", len(ents))
			}
		})
	}
}

func TestImageUpTo5MBAccepted(t *testing.T) {
	s := newStore(t)
	if err := s.Save(payloadJSON(t, "v1", map[string][]byte{"s-img": padPNG(t, MaxImage)})); err != nil {
		t.Fatalf("5 MB genau: %v", err)
	}
}

// Ein abgebrochener Schreibvorgang (Umbenennen scheitert) lässt den alten Stand stehen.
func TestRenameFailureKeepsOld(t *testing.T) {
	s := newStore(t)
	if err := s.Save(payloadJSON(t, "alt", map[string][]byte{"s-img": pngBytes(t)})); err != nil {
		t.Fatal(err)
	}
	s.rename = func(string, string) error { return errors.New("Platte voll") }
	if err := s.Save(payloadJSON(t, "neu", map[string][]byte{"s-img": pngBytes(t)})); err == nil {
		t.Fatal("Save muss scheitern")
	}
	s.rename = nil
	mustVersion(t, s, "alt")
	ents, _ := os.ReadDir(s.Dir)
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".current.tmp-") {
			t.Errorf("temporäre Datei übrig: %s", e.Name())
		}
	}
	if len(ents) != 2 {
		t.Errorf("Reste: %d Einträge", len(ents))
	}
	// danach geht Speichern wieder
	if err := s.Save(payloadJSON(t, "neu", nil)); err != nil {
		t.Fatal(err)
	}
	mustVersion(t, s, "neu")
}

// Ein beschädigter Zeiger gilt nicht als Zwischenspeicher (und führt nie aus dem Ordner hinaus).
func TestCorruptPointerNotPresent(t *testing.T) {
	s := newStore(t)
	if err := s.Save(payloadJSON(t, "v1", nil)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir, pointerFile), []byte("../../etc"), 0o600); err != nil {
		t.Fatal(err)
	}
	if s.Present() {
		t.Error("beschädigter Zeiger gilt als Zwischenspeicher")
	}
	if _, err := s.Load(); err == nil {
		t.Error("Load mit beschädigtem Zeiger")
	}
}

func TestClear(t *testing.T) {
	s := newStore(t)
	if err := s.Clear(); err != nil { // ohne Stand: wirkungslos
		t.Fatal(err)
	}
	if err := s.Save(payloadJSON(t, "v1", map[string][]byte{"s-img": pngBytes(t)})); err != nil {
		t.Fatal(err)
	}
	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	if s.Present() {
		t.Error("Present nach Clear")
	}
	if ents, _ := os.ReadDir(s.Dir); len(ents) != 0 {
		t.Errorf("Reste nach Clear: %d", len(ents))
	}
	if _, _, err := s.Image("s-img"); err == nil {
		t.Error("Bild nach Clear")
	}
}

func TestNilStoreNotPresent(t *testing.T) {
	var s *Store
	if s.Present() {
		t.Fatal("nil-Store")
	}
}

// 49 MB Bilder (base64 ≈ 65 MB) liegen unter der Lesegrenze und werden gespeichert.
func TestSaveAccepts49MBOfImages(t *testing.T) {
	s := newStore(t)
	imgs := map[string][]byte{}
	for i := 0; i < 10; i++ {
		imgs[string(rune('a'+i))+"img"] = padPNG(t, 49*(1<<20)/10)
	}
	data := payloadJSON(t, "gross", imgs)
	if len(data) <= 60<<20 || len(data) > MaxBody {
		t.Fatalf("Testgröße %d", len(data))
	}
	if err := s.Save(data); err != nil {
		t.Fatalf("49 MB Bilder: %v", err)
	}
	mustVersion(t, s, "gross")
}
