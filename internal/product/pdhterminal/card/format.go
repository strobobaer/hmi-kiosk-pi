package card

import (
	"encoding/hex"
	"math/big"
	"strings"
)

// FormatUID formatiert die rohen UID-Bytes eines Lesers.
//   - ""/"hex":  Bytes in Lesereihenfolge, Hex in Grossbuchstaben (DEADBEEF)
//   - "hex-rev": Bytes in umgekehrter Reihenfolge (EFBEADDE)
//   - "dec":     umgekehrte Reihenfolge als Dezimalzahl (4022250974);
//     math/big, damit auch UIDs mit mehr als 8 Bytes nicht ueberlaufen
//
// Unbekannte Formate verhalten sich wie "hex". raw wird nicht veraendert.
func FormatUID(raw []byte, format string) string {
	switch format {
	case "hex-rev":
		return strings.ToUpper(hex.EncodeToString(reverse(raw)))
	case "dec":
		return new(big.Int).SetBytes(reverse(raw)).String()
	default:
		return strings.ToUpper(hex.EncodeToString(raw))
	}
}

func reverse(b []byte) []byte {
	out := make([]byte, len(b))
	for i, v := range b {
		out[len(b)-1-i] = v
	}
	return out
}
