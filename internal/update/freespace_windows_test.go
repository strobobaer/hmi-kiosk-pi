//go:build windows

package update

import "testing"

func TestFreeBytesWindows(t *testing.T) {
	n, err := FreeBytes(t.TempDir())
	if err != nil || n == 0 {
		t.Fatalf("FreeBytes = %d, %v", n, err)
	}
}
