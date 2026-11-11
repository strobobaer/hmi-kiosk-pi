//go:build !linux

package pdhterminal

import "errors"

func diskUsage(string) (free, total int64, err error) {
	return 0, 0, errors.New("nur unter Linux")
}
