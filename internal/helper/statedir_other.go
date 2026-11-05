//go:build !linux

package helper

import "errors"

func writeStateFile(string, []byte) error {
	return errors.New("nur unter Linux")
}

func readStateFile(string, int) ([]byte, error) {
	return nil, errors.New("nur unter Linux")
}
