// Package secrets kapselt Verschlüsselung für VNC-Passwörter, den Konfigurationsexport und Admin-Passwort-Hashes.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
)

const keySize = 32

// Box verschlüsselt einzelne Werte mit einem lokal gespeicherten AES-256-Schlüssel.
type Box struct {
	aead cipher.AEAD
}

// OpenBox lädt den Schlüssel aus path oder erzeugt ihn mit Modus 0600.
func OpenBox(path string) (*Box, error) {
	key, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		key = make([]byte, keySize)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("Schlüssel erzeugen: %w", err)
		}
		if err := os.WriteFile(path, key, 0o600); err != nil {
			return nil, fmt.Errorf("Schlüssel speichern: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("Schlüssel lesen: %w", err)
	}
	if len(key) != keySize {
		return nil, fmt.Errorf("Schlüsseldatei %s hat ungültige Länge %d", path, len(key))
	}
	aead, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Seal liefert nonce||ciphertext als Base64.
func (b *Box) Seal(plain string) (string, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b.aead.Seal(nonce, nonce, []byte(plain), nil)), nil
}

// Open kehrt Seal um.
func (b *Box) Open(sealed string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		return "", fmt.Errorf("ungültiges Format: %w", err)
	}
	n := b.aead.NonceSize()
	if len(raw) < n+b.aead.Overhead() {
		return "", errors.New("verschlüsselter Wert zu kurz")
	}
	plain, err := b.aead.Open(nil, raw[:n], raw[n:], nil)
	if err != nil {
		return "", errors.New("Entschlüsselung fehlgeschlagen")
	}
	return string(plain), nil
}
