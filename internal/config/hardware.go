package config

// HardwareChanged meldet, ob die gespeicherte Hardware-Kennung nicht zum Gerät
// passt, auf dem der Dienst läuft: Die Karte wurde auf ein anderes Gerät
// übertragen. Ohne gespeicherte oder lesbare Kennung gibt es nichts zu vergleichen.
func HardwareChanged(stored, current string) bool {
	return stored != "" && current != "" && stored != current
}

// NewTerminalID erzeugt eine frische Terminal-Kennung.
func NewTerminalID() string { return newID() }
