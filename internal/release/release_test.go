package release

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestPublicKeyAccessor(t *testing.T) {
	old := ReleasePublicKeyHex
	t.Cleanup(func() { ReleasePublicKeyHex = old })
	ReleasePublicKeyHex = ""
	if PublicKey() != nil {
		t.Fatal("leer muss nil liefern")
	}
	ReleasePublicKeyHex = "zz"
	if PublicKey() != nil {
		t.Fatal("kein Hex muss nil liefern")
	}
	ReleasePublicKeyHex = hex.EncodeToString(make([]byte, 31))
	if PublicKey() != nil {
		t.Fatal("falsche Länge muss nil liefern")
	}
	raw := make([]byte, 32)
	raw[0] = 7
	ReleasePublicKeyHex = strings.ToUpper(hex.EncodeToString(raw)) + "\n"
	k := PublicKey()
	if len(k) != 32 || k[0] != 7 {
		t.Fatalf("Schlüssel %v", k)
	}
}
