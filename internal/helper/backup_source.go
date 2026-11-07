package helper

import (
	"context"
	"io"
)

// OpenSource liefert den rohen Abbild-Strom und seine Gesamtgröße. Es ist die Nahtstelle der
// SD-Sicherung: Streaming, Sperre, USB und Status kennen nur diese Funktion. Das Öffnen darf
// dauern (die Datenpartition wird gebaut); ctx bricht es ab. Schließen räumt auf.
type OpenSource func(ctx context.Context) (io.ReadCloser, int64, error)

// openCard öffnet die Quelle der Sicherung: Voreinstellung ist das kompakte Abbild.
func (s *Server) openCard(ctx context.Context) (io.ReadCloser, int64, error) {
	if s.Backup.OpenSource != nil {
		return s.Backup.OpenSource(ctx)
	}
	return s.openCompact(ctx)
}

// precheckSource lehnt eine Sicherung sofort ab, die mit der Voreinstellung nie ginge.
func (s *Server) precheckSource() error {
	if s.Backup.OpenSource != nil {
		return nil
	}
	return s.checkImageSystem()
}
