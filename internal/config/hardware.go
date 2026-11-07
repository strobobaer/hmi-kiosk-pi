package config

// HardwareChanged meldet, ob die gespeicherte Hardware-Kennung nicht zum Gerät
// passt, auf dem der Dienst läuft: Die Karte wurde auf ein anderes Gerät
// übertragen. Ohne gespeicherte oder lesbare Kennung gibt es nichts zu vergleichen.
func HardwareChanged(stored, current string) bool {
	return stored != "" && current != "" && stored != current
}

// NewTerminalID erzeugt eine frische Terminal-Kennung.
func NewTerminalID() string { return newID() }

// InMaintenance meldet, ob das Terminal im Wartungsmodus steht: Die Karte läuft auf
// einem anderen Gerät, oder der Admin hat ihn von Hand gestartet.
func InMaintenance(c Config, currentHardwareID string) bool {
	return c.Terminal.MaintenanceManual || HardwareChanged(c.Terminal.HardwareID, currentHardwareID)
}

// HasSealedValues meldet, ob die Konfiguration mit der Schlüsseldatei verschlüsselte
// Werte enthält (VNC-Passwörter, gemeinsamer Schlüssel); ein neuer Schlüssel machte
// sie unlesbar.
func (c Config) HasSealedValues() bool {
	if c.SharedKeyEnc != "" {
		return true
	}
	for _, h := range c.HMIs {
		if h.PasswordEnc != "" {
			return true
		}
	}
	return false
}
