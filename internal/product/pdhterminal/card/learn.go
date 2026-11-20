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
// (Zahl als Bytefolge, höchstwertiges Byte zuerst, wie normalizeUID), sonst muss es eine gerade
// Zahl Hexzeichen sein. dec ist bei Dezimaleingabe die getippte Zahl.
func UIDForms(typed string) (hexForm, hexRev, dec string, ok bool) {
	typed = strings.TrimSpace(typed)
	var raw []byte
	if SuggestFormat(typed) == "dec" {
		n, _ := new(big.Int).SetString(typed, 10)
		b := n.Bytes()
		for len(b) < 4 {
			b = append([]byte{0}, b...)
		}
		raw = b // das Terminal liest Dezimalzahlen als Bytefolge, höchstwertiges Byte zuerst (normalizeUID)
	} else {
		if typed == "" || len(typed)%2 != 0 {
			return "", "", "", false
		}
		var err error
		if raw, err = hex.DecodeString(typed); err != nil {
			return "", "", "", false
		}
	}
	dec = FormatUID(raw, "dec")
	if SuggestFormat(typed) == "dec" {
		dec = typed
	}
	return FormatUID(raw, "hex"), FormatUID(raw, "hex-rev"), dec, true
}

// normalizeUID bringt die Eingabe eines USB-Lesers in die Form, die der PN532 liefert (Hex in
// Großbuchstaben). format sagt, was der Leser tippt: "" lässt den Text unverändert (nur Großbuchstaben),
// "hex" ist Hex wie es ist, "hex-rev" Hex mit umgekehrter Bytefolge, "dec" eine Dezimalzahl (4 Byte
// bis 10 Ziffern, sonst 7 Byte, höchstwertiges Byte zuerst). Passt der Text nicht zum Format,
// bleibt er unverändert.
func normalizeUID(typed, format string) string {
	up := strings.ToUpper(strings.TrimSpace(typed))
	switch format {
	case "hex":
		return up
	case "hex-rev":
		b, err := hex.DecodeString(up)
		if err != nil || len(b) == 0 {
			return up
		}
		return strings.ToUpper(hex.EncodeToString(reverse(b)))
	case "dec":
		n, ok := new(big.Int).SetString(up, 10)
		if !ok || n.Sign() < 0 || strings.Trim(up, "0123456789") != "" {
			return up
		}
		size := 7
		if len(up) <= 10 {
			size = 4
		}
		if len(n.Bytes()) > size {
			return up
		}
		return strings.ToUpper(hex.EncodeToString(n.FillBytes(make([]byte, size))))
	}
	return up
}
