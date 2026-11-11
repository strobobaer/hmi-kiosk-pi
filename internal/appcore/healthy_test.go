package appcore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteHealthyContentAndNoLeftovers(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, HealthyFile), []byte("alt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteHealthy(dir, "1.2.3"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "healthy"))
	if string(b) != "1.2.3" {
		t.Fatalf("Marke %q", b)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), "tmp") {
			t.Fatalf("Rest: %s", e.Name())
		}
	}
}
