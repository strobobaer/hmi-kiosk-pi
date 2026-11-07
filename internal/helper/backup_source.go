package helper

import (
	"errors"
	"io"
	"os"
)

// OpenSource liefert den rohen Abbild-Strom und seine Größe. Es ist die Nahtstelle der
// SD-Sicherung: Streaming, Sperre, USB und Status kennen nur diese Funktion. Die
// Voreinstellung (openDevice) liest das ganze Blockgerät und ist ein Platzhalter für
// das endgültige Abbildformat.
type OpenSource func() (io.ReadCloser, int64, error)

// openCard öffnet die Quelle der Sicherung.
func (s *Server) openCard() (io.ReadCloser, int64, error) {
	if s.Backup.OpenSource != nil {
		return s.Backup.OpenSource()
	}
	return s.openDevice()
}

// openDevice öffnet Source (Test) oder das Gerät mit /boot/firmware zum Lesen.
func (s *Server) openDevice() (io.ReadCloser, int64, error) {
	path := s.Backup.Source
	checkDev := false
	if path == "" {
		disk, err := s.bootDisk()
		if err != nil {
			return nil, 0, err
		}
		path = s.Backup.devDir() + "/" + disk
		checkDev = true
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, errors.New("Die SD-Karte ließ sich nicht öffnen: " + err.Error())
	}
	fi, err := f.Stat()
	if err == nil && checkDev && fi.Mode()&os.ModeDevice == 0 {
		err = errors.New("kein Blockgerät")
	}
	var size int64
	if err == nil {
		size, err = f.Seek(0, io.SeekEnd)
	}
	if err == nil {
		_, err = f.Seek(0, io.SeekStart)
	}
	if err != nil {
		f.Close()
		return nil, 0, errors.New("Die SD-Karte ließ sich nicht öffnen: " + err.Error())
	}
	return f, size, nil
}
