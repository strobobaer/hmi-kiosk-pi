package helper

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// openStateDir öffnet den Ordner der dauerhaften Helfer-Daten (/data/system) ohne
// Verweise zu folgen. /data gehört dem Dienstbenutzer, der den Ordner sonst gegen
// einen Verweis oder einen eigenen Ordner tauschen könnte. Deshalb muss er ein echter
// Ordner sein, dem Helfer (root) gehören und für niemanden sonst schreibbar sein.
// Alle Zugriffe laufen danach relativ zum geöffneten Ordner.
func openStateDir(dir string, create bool) (*os.File, error) {
	if create {
		if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
			return nil, err
		}
		if err := os.Mkdir(dir, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, err
		}
	}
	fd, err := syscall.Open(dir, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) || errors.Is(err, syscall.ENOTDIR) {
			return nil, fmt.Errorf("%s ist kein echter Ordner", dir)
		}
		return nil, &os.PathError{Op: "open", Path: dir, Err: err}
	}
	d := os.NewFile(uintptr(fd), dir)
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		d.Close()
		return nil, err
	}
	if int(st.Uid) != os.Geteuid() {
		d.Close()
		return nil, fmt.Errorf("%s gehört nicht dem Helfer", dir)
	}
	if st.Mode&0o022 != 0 {
		d.Close()
		return nil, fmt.Errorf("%s ist für andere schreibbar", dir)
	}
	return d, nil
}

// writeStateFile ersetzt path atomar innerhalb des geprüften Ordners.
func writeStateFile(path string, data []byte) error {
	d, err := openStateDir(filepath.Dir(path), true)
	if err != nil {
		return err
	}
	defer d.Close()
	dirfd := int(d.Fd())
	name := filepath.Base(path)
	rnd := make([]byte, 6)
	if _, err := rand.Read(rnd); err != nil {
		return err
	}
	tmp := "." + name + ".tmp" + hex.EncodeToString(rnd)
	fd, err := syscall.Openat(dirfd, tmp, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0o644)
	if err != nil {
		return &os.PathError{Op: "open", Path: tmp, Err: err}
	}
	f := os.NewFile(uintptr(fd), tmp)
	done := false
	defer func() {
		if !done {
			f.Close()
			_ = syscall.Unlinkat(dirfd, tmp)
		}
	}()
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Chmod(0o644); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := syscall.Renameat(dirfd, tmp, dirfd, name); err != nil {
		return &os.LinkError{Op: "rename", Old: tmp, New: name, Err: err}
	}
	done = true
	return d.Sync()
}

// readStateFile liest höchstens max Bytes aus einer regulären Datei im geprüften Ordner.
// Fehlt der Ordner oder die Datei, ist der Fehler os.ErrNotExist.
func readStateFile(path string, max int) ([]byte, error) {
	d, err := openStateDir(filepath.Dir(path), false)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	fd, err := syscall.Openat(int(d.Fd()), filepath.Base(path), syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
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
