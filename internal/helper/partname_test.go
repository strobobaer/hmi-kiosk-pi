package helper

import "testing"

func TestPartName(t *testing.T) {
	for _, c := range []struct {
		disk string
		n    int
		want string
	}{
		{"mmcblk0", 1, "mmcblk0p1"},
		{"mmcblk0", 3, "mmcblk0p3"},
		{"nvme0n1", 2, "nvme0n1p2"},
		{"sda", 1, "sda1"},
		{"sdb", 3, "sdb3"},
		{"", 1, "1"},
	} {
		if got := partName(c.disk, c.n); got != c.want {
			t.Errorf("partName(%q, %d) = %q, erwartet %q", c.disk, c.n, got, c.want)
		}
	}
}
