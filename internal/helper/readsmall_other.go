//go:build !linux

package helper

import "errors"

func readSmallFile(string, int) ([]byte, error) {
	return nil, errors.New("nur unter Linux")
}
