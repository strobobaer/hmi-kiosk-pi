package release

import (
	"crypto/sha256"
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

func TestFingerprintIsFirst16HexOfSHA256(t *testing.T) {
	if Fingerprint(nil) != "" {
		t.Fatal("ohne Schlüssel muss der Fingerabdruck leer sein")
	}
	key := make([]byte, 32)
	sum := sha256.Sum256(key)
	want := hex.EncodeToString(sum[:])[:16]
	if got := Fingerprint(key); got != want || len(got) != 16 {
		t.Fatalf("got %q, want %q", got, want)
	}
}
