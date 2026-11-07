//go:build linux

package helper

import (
	"errors"
	"os"
	"syscall"
)

func freeBytes(path string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}

// dirAttr sind Besitzer und Rechte eines Ordners.
type dirAttr struct {
	uid, gid uint32
	perm     uint32 // Unix-Rechtebits, auch setgid/sticky
}

func dirAttrOf(path string) (dirAttr, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return dirAttr{}, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || !fi.IsDir() {
		return dirAttr{}, errors.New("kein Ordner")
	}
	return dirAttr{uid: st.Uid, gid: st.Gid, perm: uint32(st.Mode) & 0o7777}, nil
}
