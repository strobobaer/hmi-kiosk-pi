// Package release trägt den öffentlichen Freigabeschlüssel, der beim Bau per
// -ldflags "-X hmikiosk/internal/release.ReleasePublicKeyHex=<hex>" gesetzt wird.
package release

import (
	"crypto/ed25519"
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
