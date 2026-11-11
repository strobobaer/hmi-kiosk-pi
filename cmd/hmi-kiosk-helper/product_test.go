package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hmikiosk/internal/update"
)

func TestProductBinary(t *testing.T) {
	write := func(root, content string) {
		p := filepath.Join(root, productFile)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		name, content, want, errPart string
		missing                      bool
	}{
		{name: "fehlt", missing: true, want: "hmi-kiosk"},
		{name: "leer", content: "\n", want: "hmi-kiosk"},
		{name: "hmi-kiosk", content: "hmi-kiosk\n", want: "hmi-kiosk"},
		{name: "pdh-terminal", content: "pdh-terminal\n", want: "pdh-terminal"},
		{name: "unbekannt", content: "evil\n", errPart: "Unbekanntes Produkt"},
	} {
		root := t.TempDir()
		if !c.missing {
			write(root, c.content)
		}
		got, err := productBinary(root)
		if c.errPart != "" {
			if err == nil || !strings.Contains(err.Error(), c.errPart) {
				t.Errorf("%s: err = %v", c.name, err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%s: %q, %v", c.name, got, err)
		}
	}
}

func TestUpdateGuardRollbackUsesProductBinary(t *testing.T) {
	root := t.TempDir()
	for _, v := range []string{"1.0.0", "1.1.0"} {
		d := filepath.Join(root, "app", v)
		os.MkdirAll(d, 0o755)
		os.WriteFile(filepath.Join(d, "pdh-terminal"), []byte("x"), 0o755)
		if err := update.ActivateBinary(root, "pdh-terminal", v); err != nil {
			t.Fatal(err)
		}
	}
	var calls []string
	err := runGuard(context.Background(), root, "pdh-terminal", filepath.Join(t.TempDir(), "healthy"), 100*time.Millisecond, recorder(&calls))
	if err != nil {
		t.Fatal(err)
	}
	if cur, _ := os.Readlink(filepath.Join(root, "app", "current")); cur != "1.0.0" {
		t.Fatalf("current=%s", cur)
	}
	// Mit dem Standard-Programmnamen wäre der Rückfall gescheitert.
	if len(calls) != 2 {
		t.Fatalf("calls %v", calls)
	}
}
