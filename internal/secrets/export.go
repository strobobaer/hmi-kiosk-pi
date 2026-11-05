package secrets

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // KiB = 64 MB
	argonThreads = 2
	saltSize     = 16
	hashSize     = 32

	exportMagic   = 0x01
	exportVersion = 0x01
)

func deriveKey(password string, salt []byte) []byte {
	return argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, keySize)
}

// SealExport verschlüsselt data mit einem aus password abgeleiteten Schlüssel.
// Format: magic || version || salt(16) || nonce(12) || ciphertext.
func SealExport(data []byte, password string) ([]byte, error) {
	salt := make([]byte, saltSize)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	aead, err := newGCM(deriveKey(password, salt))
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := append([]byte{exportMagic, exportVersion}, salt...)
	out = append(out, nonce...)
	return aead.Seal(out, nonce, data, nil), nil
}

// OpenExport entschlüsselt einen mit SealExport erzeugten Blob.
func OpenExport(blob []byte, password string) ([]byte, error) {
	const nonceSize = 12
	const gcmOverhead = 16
	const header = 2 + saltSize + nonceSize
	if len(blob) < header+gcmOverhead {
		return nil, errors.New("Exportdatei zu kurz")
	}
	if blob[0] != exportMagic || blob[1] != exportVersion {
		return nil, errors.New("unbekanntes Exportformat")
	}
	salt := blob[2 : 2+saltSize]
	nonce := blob[2+saltSize : header]
	aead, err := newGCM(deriveKey(password, salt))
	if err != nil {
		return nil, err
	}
	plain, err := aead.Open(nil, nonce, blob[header:], nil)
	if err != nil {
		return nil, errors.New("Passwort falsch oder Exportdatei beschädigt")
	}
	return plain, nil
}

// HashPassword liefert einen Argon2id-Hash im Format argon2id$salt$hash (Base64).
func HashPassword(pw string) (string, error) {
	salt := make([]byte, saltSize)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	h := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, hashSize)
	enc := base64.RawStdEncoding
	return fmt.Sprintf("argon2id$%s$%s", enc.EncodeToString(salt), enc.EncodeToString(h)), nil
}

// CheckPassword prüft pw gegen einen mit HashPassword erzeugten Hash.
func CheckPassword(hash, pw string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 3 || parts[0] != "argon2id" {
		return false
	}
	enc := base64.RawStdEncoding
	salt, err1 := enc.DecodeString(parts[1])
	want, err2 := enc.DecodeString(parts[2])
	if err1 != nil || err2 != nil || len(want) == 0 {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}
