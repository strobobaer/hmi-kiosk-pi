package client

import (
	"crypto/cipher"
	"crypto/des"
	"encoding/base64"
	"fmt"
)

// remminaEncrypt folgt Remminas Dateiformat ohne Schlüsselbund: 3DES-CBC, Schlüssel = erste 24 Byte
// des Secrets, IV = die nächsten 8, Nullauffüllung um 8 - len%8 Byte (ein voller Block bei Vielfachen von 8).
func remminaEncrypt(secretB64, password string) (string, error) {
	if password == "" {
		return "", nil
	}
	secret, err := base64.StdEncoding.DecodeString(secretB64)
	if err != nil || len(secret) < 32 {
		return "", fmt.Errorf("remmina-secret ungültig")
	}
	block, err := des.NewTripleDESCipher(secret[:24])
	if err != nil {
		return "", err
	}
	pt := []byte(password)
	pad := 8 - len(pt)%8
	pt = append(pt, make([]byte, pad)...)
	ct := make([]byte, len(pt))
	cipher.NewCBCEncrypter(block, secret[24:32]).CryptBlocks(ct, pt)
	return base64.StdEncoding.EncodeToString(ct), nil
}
