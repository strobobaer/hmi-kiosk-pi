//go:build !linux && !windows

package update

import "errors"

func FreeBytes(string) (uint64, error) { return 0, errors.New("Speicherplatzprüfung nur unter Linux") }
