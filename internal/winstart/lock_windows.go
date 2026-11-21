//go:build windows

package winstart

import (
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// openNoWriteShare öffnet die Datei zum Lesen und gibt sie nur zum Lesen frei: Solange das Handle offen ist,
// kann niemand sie beschreiben, löschen oder umbenennen. Hält gerade ein Virenscanner sie zum Schreiben, kurz erneut versuchen.
func openNoWriteShare(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	var h windows.Handle
	for i := 0; ; i++ {
		h, err = windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil,
			windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
		if err == nil {
			break
		}
		if err != windows.ERROR_SHARING_VIOLATION || i >= 20 {
			return nil, &os.PathError{Op: "open", Path: path, Err: err}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return os.NewFile(uintptr(h), path), nil
}
