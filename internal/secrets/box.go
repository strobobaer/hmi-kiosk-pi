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
	"path/filepath"
)

const keySize = 32

// Box verschlüsselt einzelne Werte mit einem lokal gespeicherten AES-256-Schlüssel.
type Box struct {
	aead cipher.AEAD
}

// OpenBox lädt den Schlüssel aus path oder erzeugt ihn mit Modus 0600. Eine leere
// oder zu kurze Datei stammt von einem abgebrochenen ersten Schreiben; sie gilt nur
// als fehlend, solange sealedInUse falsch ist (die Konfiguration enthält noch keine
// verschlüsselten Werte). Sonst wären diese Werte mit einem neuen Schlüssel verloren.
func OpenBox(path string, sealedInUse bool) (*Box, error) {
	key, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		key, err = createKey(path)
	case err != nil:
		return nil, fmt.Errorf("Schlüsseldatei %s nicht lesbar: %w", path, err)
	case len(key) < keySize && !sealedInUse:
		key, err = createKey(path)
	case len(key) != keySize:
		return nil, fmt.Errorf("Schlüsseldatei %s ist beschädigt (%d statt %d Byte); gespeicherte VNC-Passwörter "+
			"und der gemeinsame Schlüssel sind damit nicht lesbar", path, len(key), keySize)
	}
	if err != nil {
		return nil, err
	}
	aead, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// createKey schreibt einen neuen Schlüssel atomar: Ein Stromausfall hinterlässt
// entweder keine oder eine vollständige Datei.
func createKey(path string) ([]byte, error) {
	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("Schlüssel erzeugen: %w", err)
	}
	if err := writeFileAtomic(path, key, 0o600); err != nil {
		return nil, fmt.Errorf("Schlüssel speichern: %w", err)
	}
	return key, nil
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) (err error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	if err = tmp.Chmod(mode); err != nil {
		return err
	}
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
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
