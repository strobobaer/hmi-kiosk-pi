package client

import (
	"crypto/des"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
)

// Fester Schlüssel des VNC-Passwortdateiformats (für Standard-DES).
const tigervncKeyHex = "e84ad660c4721ae0"

type tigervnc struct{}

func (tigervnc) Name() string { return "tigervnc" }

func tigervncObfuscate(password string) ([]byte, error) {
	key, _ := hex.DecodeString(tigervncKeyHex)
	block, err := des.NewCipher(key)
	if err != nil {
		return nil, err
	}
	pt := make([]byte, 8)
	copy(pt, password)
	ct := make([]byte, 8)
	block.Encrypt(ct, pt)
	return ct, nil
}

func (tigervnc) Prepare(dir string, s Spec) (Command, error) {
	if err := validateSpec(s); err != nil {
		return Command{}, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Command{}, err
	}
	ct, err := tigervncObfuscate(s.Password)
	if err != nil {
		return Command{}, err
	}
	pf := filepath.Join(dir, "passwd")
	if err := os.WriteFile(pf, ct, 0o600); err != nil {
		return Command{}, err
	}
	return Command{
		Path: "vncviewer",
		Args: []string{
			"-FullScreen",
			"-PasswordFile=" + pf,
			"-ViewOnly=" + boolStr(s.ViewOnly),
			s.Host + "::" + strconv.Itoa(s.Port),
		},
		Env: []string{"HOME=" + dir},
	}, nil
}
