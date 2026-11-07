//go:build !linux

package helper

import "errors"

func freeBytes(string) (int64, error) { return 0, errors.New("nur unter Linux") }

// dirAttr sind Besitzer und Rechte eines Ordners.
type dirAttr struct {
	uid, gid uint32
	perm     uint32
}

func fsUsage(string) (int64, int64, error) { return 0, 0, errors.New("nur unter Linux") }

func dirAttrOf(string) (dirAttr, error) { return dirAttr{}, errors.New("nur unter Linux") }
