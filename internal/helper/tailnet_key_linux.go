package helper

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"syscall"
)

// withTailnetKeyFile legt den Auth-Key in einer neuen Datei (0600, O_EXCL, kein Verweis) im geprüften Ordner an
// (root-eigen, für andere nicht schreibbar, kein Verweis), ruft fn und löscht die Datei danach auf jedem Weg.
// Ein Rest eines früheren Abbruchs wird vorher entfernt. Der Ordner wird nie angelegt: Er ist der
// Laufzeitordner des Helfers.
func withTailnetKeyFile(path, key string, fn func() error) error {
	d, err := openStateDir(filepath.Dir(path), false)
	if err != nil {
		return errors.New("Ordner für die Schlüsseldatei nicht nutzbar: " + err.Error())
	}
	defer d.Close()
	dirfd := int(d.Fd())
	name := filepath.Base(path)
	if err := syscall.Unlinkat(dirfd, name); err != nil && !errors.Is(err, syscall.ENOENT) {
		return errors.New("alte Schlüsseldatei nicht entfernbar")
	}
	fd, err := syscall.Openat(dirfd, name, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0o600)
	if err != nil {
		return errors.New("Schlüsseldatei nicht anlegbar")
	}
	f := os.NewFile(uintptr(fd), name)
	defer func() { _ = syscall.Unlinkat(dirfd, name) }()
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return errors.New("Schlüsseldatei nicht schützbar")
	}
	_, werr := f.WriteString(key)
	cerr := f.Close()
	if werr != nil || cerr != nil {
		return errors.New("Schlüsseldatei nicht schreibbar")
	}
	return fn()
}

// removeStaleTailnetKey entfernt beim Start des Helfers eine Schlüsseldatei, die ein abgebrochener Aufruf
// (SIGKILL, Absturz) zurückgelassen hat. Fehlt der Ordner oder die Datei, ist nichts zu tun.
func (s *Server) removeStaleTailnetKey() {
	path := s.TailnetKeyPath
	if path == "" {
		path = defaultTailnetKeyPath
	}
	d, err := openStateDir(filepath.Dir(path), false)
	if err != nil {
		return
	}
	defer d.Close()
	if err := syscall.Unlinkat(int(d.Fd()), filepath.Base(path)); err != nil && !errors.Is(err, syscall.ENOENT) {
		log.Printf("Alte Tailscale-Schlüsseldatei nicht entfernbar: %v", err)
	}
}
