package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestHardwareIDFromDeviceTreeTrimsNulBytes(t *testing.T) {
	proc, sys := t.TempDir(), t.TempDir()
	writeFile(t, proc, "device-tree/serial-number", "10000000ABCDEF01\x00")
	if got := HardwareID(proc, sys); got != "10000000abcdef01" {
		t.Fatalf("got %q", got)
	}
}

func TestHardwareIDFallsBackToDMIProductUUID(t *testing.T) {
	proc, sys := t.TempDir(), t.TempDir()
	writeFile(t, sys, "class/dmi/id/product_uuid", "4C4C4544-0042-3510-8047-B8C04F4D3532\n")
	if got := HardwareID(proc, sys); got != "4c4c4544-0042-3510-8047-b8c04f4d3532" {
		t.Fatalf("got %q", got)
	}
}

func TestHardwareIDDeviceTreeWinsOverDMI(t *testing.T) {
	proc, sys := t.TempDir(), t.TempDir()
	writeFile(t, proc, "device-tree/serial-number", "0000000012345678\x00")
	writeFile(t, sys, "class/dmi/id/product_uuid", "4C4C4544-0042-3510-8047-B8C04F4D3532")
	if got := HardwareID(proc, sys); got != "0000000012345678" {
		t.Fatalf("got %q", got)
	}
}

func TestHardwareIDEmptyWhenNothingReadable(t *testing.T) {
	if got := HardwareID(t.TempDir(), t.TempDir()); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestHardwareIDRejectsUselessValues(t *testing.T) {
	for name, content := range map[string]string{
		"leer":          "",
		"nur Nullen":    "\x00\x00\x00",
		"alles null":    "0000000000000000\x00",
		"Steuerzeichen": "abc\x01def",
		"zu lang":       string(make([]byte, 200)) + "x",
		"Leerzeichen":   "ab cd",
		"Schrägstrich":  "ab/cd",
	} {
		t.Run(name, func(t *testing.T) {
			proc, sys := t.TempDir(), t.TempDir()
			writeFile(t, proc, "device-tree/serial-number", content)
			if got := HardwareID(proc, sys); got != "" {
				t.Fatalf("got %q", got)
			}
		})
	}
}

func TestHardwareIDIgnoresSymlinks(t *testing.T) {
	proc, sys := t.TempDir(), t.TempDir()
	target := filepath.Join(t.TempDir(), "ziel")
	if err := os.WriteFile(target, []byte("deadbeef"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(proc, "device-tree"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(proc, "device-tree", "serial-number")); err != nil {
		t.Skip("keine Symlinks:", err)
	}
	if got := HardwareID(proc, sys); got != "" {
		t.Fatalf("Symlink wurde gelesen: %q", got)
	}
}
