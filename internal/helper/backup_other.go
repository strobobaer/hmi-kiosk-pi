//go:build !linux

package helper

import "errors"

func freeBytes(string) (int64, error) { return 0, errors.New("nur unter Linux") }
