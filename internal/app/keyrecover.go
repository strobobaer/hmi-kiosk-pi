package app

import (
	"log"
	"path/filepath"

	"hmikiosk/internal/web"
)

// recoverInterruptedRepair nimmt eine Schlüsselreparatur zurück, die der Strom
// zwischen neuem Schlüssel und neuer Konfiguration unterbrochen hat, und räumt
// die Kopie des alten Schlüssels nach einer fertigen Reparatur weg.
func (s *service) recoverInterruptedRepair() {
	restored, err := web.RecoverInterruptedRepair(filepath.Join(s.opts.DataDir, secretsFile), s.box, s.config())
	if err != nil {
		log.Printf("Unterbrochene Schlüsselreparatur nicht geprüft: %v", err)
		return
	}
	if !restored {
		return
	}
	s.mu.Lock()
	s.boxWarn = ""
	s.mu.Unlock()
	s.warn("Eine unterbrochene Schlüsselreparatur wurde zurückgenommen: Der alte Geräteschlüssel gilt wieder. Die Reparatur kann auf der System-Seite wiederholt werden.")
}
