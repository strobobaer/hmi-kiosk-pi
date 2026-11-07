package web

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"time"

	"hmikiosk/internal/config"
	"hmikiosk/internal/secrets"
)

const sharedKeyLabel = "Terminal-Schlüssel"

// keyScan nennt die gespeicherten Werte, die sich mit dem Geräteschlüssel nicht öffnen lassen.
type keyScan struct {
	HMIs   []string // Namen der HMIs, deren Passwort unlesbar ist
	Shared bool     // der gemeinsame Terminal-Schlüssel ist unlesbar
}

// Names liefert die Anzeigenamen: erst die HMIs, dann der Terminal-Schlüssel.
func (k keyScan) Names() []string {
	names := slices.Clone(k.HMIs)
	if k.Shared {
		names = append(names, sharedKeyLabel)
	}
	return names
}

// scanKeys versucht jeden gespeicherten verschlüsselten Wert zu öffnen.
func scanKeys(cfg config.Config, box *secrets.Box) keyScan {
	var k keyScan
	for _, h := range cfg.HMIs {
		if h.PasswordEnc == "" {
			continue
		}
		if _, err := box.Open(h.PasswordEnc); err != nil {
			k.HMIs = append(k.HMIs, h.Name)
		}
	}
	if cfg.SharedKeyEnc != "" {
		if _, err := box.Open(cfg.SharedKeyEnc); err != nil {
			k.Shared = true
		}
	}
	return k
}

type repairResult struct {
	Cleared []string // Namen der Einträge, deren Wert verloren ging
}

// repairKeys erzeugt einen neuen Geräteschlüssel und liefert die Konfiguration, in der
// jeder lesbare Wert mit dem neuen Schlüssel versiegelt und jeder unlesbare geleert ist.
//
// Reihenfolge, damit ein Stromausfall nie still einen Zustand hinterlässt:
//  1. Die neue Konfiguration entsteht nur im Speicher, nichts ist geändert.
//  2. Der neue Schlüssel wird atomar geschrieben. Scheitert das, bleibt alles beim Alten.
//  3. save schreibt die Konfiguration atomar. Scheitert das, kommt der alte Schlüssel
//     zurück (oder die neue Datei wird entfernt, wenn es keine alte gab).
//  4. Erst danach übernimmt die Box im Speicher den neuen Schlüssel.
//
// Restrisiko: Fällt der Strom genau zwischen 2 und 3, liegt der neue Schlüssel zur alten
// Konfiguration auf der Platte. Alle Werte sind dann unlesbar, aber der nächste Start
// erkennt das (scanKeys), und die Reparatur lässt sich wiederholen. Verloren sind dabei
// nur die Passwörter, die vorher noch lesbar waren; sie müssen neu eingegeben werden.
func repairKeys(keyPath string, box *secrets.Box, cfg config.Config, save func(config.Config) error) (config.Config, repairResult, error) {
	var res repairResult
	newKey, err := secrets.GenerateKey()
	if err != nil {
		return cfg, res, err
	}
	next, err := secrets.NewBox(newKey)
	if err != nil {
		return cfg, res, err
	}

	out := cfg
	out.HMIs = slices.Clone(cfg.HMIs)
	reseal := func(enc string) (string, bool) {
		plain, err := box.Open(enc)
		if err != nil {
			return "", false
		}
		fresh, err := next.Seal(plain)
		return fresh, err == nil
	}
	for i := range out.HMIs {
		h := &out.HMIs[i]
		if h.PasswordEnc == "" {
			continue
		}
		if fresh, ok := reseal(h.PasswordEnc); ok {
			h.PasswordEnc = fresh
			continue
		}
		h.PasswordEnc, h.PasswordLost = "", true
		res.Cleared = append(res.Cleared, h.Name)
	}
	if out.SharedKeyEnc != "" {
		if fresh, ok := reseal(out.SharedKeyEnc); ok {
			out.SharedKeyEnc = fresh
		} else {
			out.SharedKeyEnc = ""
			res.Cleared = append(res.Cleared, sharedKeyLabel)
		}
	}

	oldRaw, readErr := os.ReadFile(keyPath)
	if err := secrets.WriteKeyFile(keyPath, newKey); err != nil {
		return cfg, repairResult{}, fmt.Errorf("Der neue Schlüssel konnte nicht gespeichert werden, es bleibt alles beim Alten: %w", err)
	}
	if err := save(out); err != nil {
		if rerr := restoreKey(keyPath, oldRaw, readErr); rerr != nil {
			// Auf der Platte gilt nun der neue Schlüssel zur alten Konfiguration; die Box
			// folgt der Platte, damit die Seite die unlesbaren Werte zeigt.
			_ = box.Rekey(newKey)
			return cfg, repairResult{}, fmt.Errorf("Die Konfiguration konnte nicht gespeichert werden (%v) und der alte Schlüssel nicht wiederhergestellt werden (%v). Die Passwörter sind jetzt unlesbar; die Reparatur bitte wiederholen.", err, rerr)
		}
		return cfg, repairResult{}, fmt.Errorf("Die Konfiguration konnte nicht gespeichert werden, es bleibt alles beim Alten: %w", err)
	}
	if err := box.Rekey(newKey); err != nil {
		return out, res, err
	}
	return out, res, nil
}

// restoreKey stellt die Schlüsseldatei vor der Reparatur wieder her.
func restoreKey(path string, oldRaw []byte, readErr error) error {
	switch {
	case readErr == nil:
		return secrets.WriteKeyFile(path, oldRaw)
	case errors.Is(readErr, os.ErrNotExist):
		return os.Remove(path)
	}
	return readErr
}

// peerCheckLimit begrenzt die Schlüsselprüfung je Terminal auf der System-Seite.
var peerCheckLimit = 3 * time.Second
