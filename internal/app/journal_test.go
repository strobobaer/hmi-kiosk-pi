package app

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openTestJournal(t *testing.T, dir string, maxBytes int64, stderr io.Writer) *Journal {
	t.Helper()
	j, err := newJournal(dir, maxBytes, stderr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(j.Close)
	return j
}

func dirSize(t *testing.T, dir string) int64 {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var sum int64
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		sum += info.Size()
	}
	return sum
}

func TestJournalRotatesWithin50MB(t *testing.T) {
	if MaxLogBytes != 50<<20 {
		t.Fatalf("Grenze %d", MaxLogBytes)
	}
	dir := t.TempDir()
	j := openTestJournal(t, dir, MaxLogBytes, io.Discard)
	line := append(bytes.Repeat([]byte("x"), maxLineBytes-1), '\n')
	for written := 0; written < 80<<20; written += len(line) {
		j.writeFile(line)
	}
	last := "letzte Zeile\n"
	j.writeFile([]byte(last))
	j.Close()

	if got := dirSize(t, dir); got > MaxLogBytes {
		t.Fatalf("Protokolle belegen %d Byte", got)
	}
	newest, err := os.ReadFile(filepath.Join(dir, "kiosk.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(newest), last) {
		t.Fatal("kiosk.log ist nicht die neueste Datei")
	}
	if _, err := os.Stat(filepath.Join(dir, "kiosk.log.1")); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if n := e.Name(); n != "kiosk.log" && !strings.HasPrefix(n, "kiosk.log.") {
			t.Fatalf("unerwartete Datei %s", n)
		}
	}
	if j.Warning() != "" {
		t.Fatalf("Warnung %q", j.Warning())
	}
}

func TestJournalOverlongLineStaysWithinLimit(t *testing.T) {
	dir := t.TempDir()
	j := openTestJournal(t, dir, 5000, io.Discard)
	for range 20 {
		j.writeFile(bytes.Repeat([]byte("y"), 100_000))
	}
	j.Close()
	if got := dirSize(t, dir); got > 5000 {
		t.Fatalf("Protokolle belegen %d Byte", got)
	}
}

func TestJournalSurvivesFullDisk(t *testing.T) {
	if _, err := os.Stat("/dev/full"); err != nil {
		t.Skip("kein /dev/full")
	}
	dir := t.TempDir()
	if err := os.Symlink("/dev/full", filepath.Join(dir, "kiosk.log")); err != nil {
		t.Fatal(err)
	}
	j := openTestJournal(t, dir, MaxLogBytes, io.Discard)
	j.retryEvery = 0
	j.writeFile([]byte("erste Zeile\n"))
	if !strings.Contains(j.Warning(), "nicht schreibbar") {
		t.Fatalf("keine Warnung nach Schreibfehler: %q", j.Warning())
	}

	// Ist wieder Platz, geht es ohne Neustart weiter.
	if err := os.Remove(filepath.Join(dir, "kiosk.log")); err != nil {
		t.Fatal(err)
	}
	j.writeFile([]byte("zweite Zeile\n"))
	b, err := os.ReadFile(filepath.Join(dir, "kiosk.log"))
	if err != nil || string(b) != "zweite Zeile\n" {
		t.Fatalf("nach Erholung: %q, %v", b, err)
	}
	w := j.Warning()
	if strings.Contains(w, "nicht schreibbar") || !strings.Contains(w, "1 Zeilen verworfen") {
		t.Fatalf("Warnung nach Erholung: %q", w)
	}
}

func TestJournalUnusableDirStillAcceptsLines(t *testing.T) {
	file := filepath.Join(t.TempDir(), "kein-ordner")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr safeBuffer
	j, err := newJournal(filepath.Join(file, "log"), MaxLogBytes, &stderr)
	if err == nil {
		t.Fatal("kein Fehler bei unbrauchbarem Ordner")
	}
	if n, err := j.Write([]byte("Zeile\n")); n != 6 || err != nil {
		t.Fatalf("Write %d, %v", n, err)
	}
	j.Close()
	if !strings.Contains(stderr.String(), "Zeile") {
		t.Fatal("Zeile fehlt auf stderr")
	}
	if j.Warning() == "" {
		t.Fatal("keine Warnung")
	}
}

type blockedWriter struct{ release chan struct{} }

func (b blockedWriter) Write(p []byte) (int, error) {
	<-b.release
	return len(p), nil
}

func TestJournalWriteNeverBlocks(t *testing.T) {
	w := blockedWriter{release: make(chan struct{})}
	j, err := newJournal(t.TempDir(), MaxLogBytes, w)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 3 * journalBuffer {
			j.Write([]byte("Zeile\n"))
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Write blockiert bei hängendem Ziel")
	}
	if !strings.Contains(j.Warning(), "verworfen") {
		t.Fatalf("Warnung %q", j.Warning())
	}
	close(w.release)
	j.Close()
}

func TestJournalCloseDoesNotHangOnBlockedTarget(t *testing.T) {
	w := blockedWriter{release: make(chan struct{})}
	defer close(w.release)
	j, err := newJournal(t.TempDir(), MaxLogBytes, w)
	if err != nil {
		t.Fatal(err)
	}
	j.closeWait = 10 * time.Millisecond
	j.Write([]byte("Zeile\n"))
	done := make(chan struct{})
	go func() { j.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Close hängt")
	}
}

func TestJournalSessionRecordLine(t *testing.T) {
	dir := t.TempDir()
	j := openTestJournal(t, dir, MaxLogBytes, io.Discard)
	j.Session(SessionRecord{
		Time:     time.Date(2026, 11, 5, 8, 0, 0, 0, time.UTC),
		Terminal: "Linie\n1", HMI: "Presse", Mode: "Bedienen",
		Duration: 90 * time.Second, Reason: "Übernommen von B",
	})
	j.Close()
	b, err := os.ReadFile(filepath.Join(dir, "kiosk.log"))
	if err != nil {
		t.Fatal(err)
	}
	line := string(b)
	for _, want := range []string{"2026-11-05T08:00:00Z", `"Linie\n1"`, `"Presse"`, `"Bedienen"`, "1m30s", "Übernommen von B"} {
		if !strings.Contains(line, want) {
			t.Errorf("%q fehlt in %q", want, line)
		}
	}
	if strings.Count(line, "\n") != 1 {
		t.Errorf("mehr als eine Zeile: %q", line)
	}
}
