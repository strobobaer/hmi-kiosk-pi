package update

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// readSmall liest höchstens max Bytes aus einer regulären Datei (kein Symlink).
func readSmall(path string, max int) ([]byte, error) {
	li, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !li.Mode().IsRegular() {
		return nil, errors.New("keine reguläre Datei")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() || !os.SameFile(li, fi) {
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
