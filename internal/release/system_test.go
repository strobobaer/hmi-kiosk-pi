package release

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

// Abbild und install.sh schreiben den Stand aus packaging/rootfs; er muss dem des Programms gleichen.
func TestSystemLevelFileMatchesConstant(t *testing.T) {
	b, err := os.ReadFile("../../packaging/rootfs/etc/hmi-kiosk/system-level")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(b)); got != strconv.Itoa(SystemLevel) {
		t.Fatalf("packaging/rootfs/etc/hmi-kiosk/system-level = %q, SystemLevel = %d", got, SystemLevel)
	}
}
