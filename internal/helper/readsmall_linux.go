package helper

import (
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
)

// readSmallFile liest höchstens max Bytes aus einer regulären Datei. Symlinks und
// Sonderdateien (FIFO, Geräte) werden abgelehnt, das Öffnen blockiert nie.
func readSmallFile(path string, max int) ([]byte, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, errors.New("keine reguläre Datei")
	}
	b, err := io.ReadAll(io.LimitReader(f, int64(max)+1))
	if err != nil {
		return nil, err
	}
	if len(b) > max {
		return nil, fmt.Errorf("Datei länger als %d Bytes", max)
	}
	return b, nil
}
