// Package release trägt den öffentlichen Freigabeschlüssel, der beim Bau per
// -ldflags "-X hmikiosk/internal/release.ReleasePublicKeyHex=<hex>" gesetzt wird.
package release

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// ReleasePublicKeyHex ist der Ed25519-Prüfschlüssel in Hex (-ldflags kennt nur Strings).
var ReleasePublicKeyHex string

// PublicKey liefert den Prüfschlüssel oder nil, wenn keiner (gültiger) gesetzt ist.
func PublicKey() ed25519.PublicKey {
	b, err := hex.DecodeString(strings.TrimSpace(ReleasePublicKeyHex))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil
	}
	return ed25519.PublicKey(b)
}

// Fingerprint kürzt den SHA-256 des Schlüssels auf 16 Hex-Zeichen; ohne Schlüssel leer.
func Fingerprint(key ed25519.PublicKey) string {
	if len(key) == 0 {
		return ""
	}
	sum := sha256.Sum256(key)
	return hex.EncodeToString(sum[:])[:16]
}
