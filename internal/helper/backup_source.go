package helper

import (
	"context"
	"io"
	"os"
)

// OpenSource liefert den rohen Abbild-Strom und seine Gesamtgröße. Es ist die Nahtstelle der
// SD-Sicherung: Streaming, Sperre, USB und Status kennen nur diese Funktion. Das Öffnen darf
// dauern (die Datenpartition wird gebaut); ctx bricht es ab. Schließen räumt auf.
type OpenSource func(ctx context.Context) (io.ReadCloser, int64, error)

// sourceKind ist der Weg der Sicherung, den der Helfer wählt.
type sourceKind int

const (
	kindCustom  sourceKind = iota // Backup.OpenSource ersetzt alles (Tests)
	kindCompact                   // SD-Abbild: Rohbytes bis Partition 2 plus kleine Datenpartition
	kindFiles                     // System ohne Schreibschutz: dateiweise gebautes Abbild
)

func (s *Server) imageSystem() bool {
	_, err := os.Stat(s.Backup.imageRelease())
	return err == nil
}

func (s *Server) sourceKind() sourceKind {
	switch {
	case s.Backup.OpenSource != nil:
		return kindCustom
	case s.imageSystem():
		return kindCompact
	}
	return kindFiles
}

// openCard öffnet die Quelle der Sicherung. target sagt, wohin die dateiweise Arbeitsdatei kommt
// (Download: Ordner auf der Karte, USB: der Stick).
func (s *Server) openCard(ctx context.Context, run *backupRun, target filesTarget) (io.ReadCloser, int64, error) {
	switch s.sourceKind() {
	case kindCustom:
		return s.Backup.OpenSource(ctx)
	case kindCompact:
		return s.openCompact(ctx)
	}
	return s.openFiles(ctx, run, target)
}

// precheckSource lehnt eine Sicherung sofort ab, die mit der Voreinstellung nie ginge.
func (s *Server) precheckSource(ctx context.Context) error {
	if s.sourceKind() != kindFiles {
		return nil
	}
	_, err := s.planFiles(ctx)
	return err
}
