//go:build windows

package update

import "golang.org/x/sys/windows"

// FreeBytes ist der für den Benutzer verfügbare Platz auf dem Laufwerk von path (Kontingente eingerechnet).
func FreeBytes(path string) (uint64, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var avail uint64
	if err := windows.GetDiskFreeSpaceEx(p, &avail, nil, nil); err != nil {
		return 0, err
	}
	return avail, nil
}
