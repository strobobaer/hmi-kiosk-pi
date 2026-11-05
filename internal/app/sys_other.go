//go:build !linux

package app

import "errors"

func killGroup(int, bool) error { return errors.New("nur unter Linux") }

func ownGroup() int { return 0 }

func diskUsage(string) (free, total int64, err error) {
	return 0, 0, errors.New("nur unter Linux")
}
