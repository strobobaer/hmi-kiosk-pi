package card

import (
	"context"
	"encoding/hex"
	"errors"
	"math/big"
	"strings"
	"time"
)

// errNotSupported: Das Lernen eines Tastaturlesers gibt es nur unter Windows; die Seite blendet den Knopf dort aus, wo es fehlt.
var errNotSupported = errors.New("Leser anlernen wird auf dieser Plattform nicht unterstützt")

// Learn wartet bis zu d auf die erste Tastenfolge, die mit Enter endet, und liefert das Gerät
// und den getippten Text (Großbuchstaben). Während des Wartens werden Kartentasten aller Tastaturen
// nicht an andere Programme weitergegeben.
func Learn(ctx context.Context, d time.Duration) (InputDevice, string, error) {
	return learnPlatform(ctx, d)
}

// SuggestFormat schlägt das Format zu einer getippten UID vor: genau 10 Ziffern sind dezimal
// (dec), alles andere, auch 8 oder 14 Hexzeichen, hex.
func SuggestFormat(typed string) string {
	if len(typed) == 10 && strings.Trim(typed, "0123456789") == "" {
		return "dec"
	}
	return "hex"
}

// UIDForms zeigt die getippte UID in den drei Schreibweisen. Zehn Ziffern gelten als dezimal
// (umgekehrte Bytefolge als Zahl, wie FormatUID), sonst muss es eine gerade Zahl Hexzeichen sein.
func UIDForms(typed string) (hexForm, hexRev, dec string, ok bool) {
	typed = strings.TrimSpace(typed)
	var raw []byte
	if SuggestFormat(typed) == "dec" {
		n, _ := new(big.Int).SetString(typed, 10)
		b := n.Bytes()
		for len(b) < 4 {
			b = append([]byte{0}, b...)
		}
		raw = reverse(b)
	} else {
		if typed == "" || len(typed)%2 != 0 {
			return "", "", "", false
		}
		var err error
		if raw, err = hex.DecodeString(typed); err != nil {
			return "", "", "", false
		}
	}
	return FormatUID(raw, "hex"), FormatUID(raw, "hex-rev"), FormatUID(raw, "dec"), true
}
