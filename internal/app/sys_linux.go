//go:build linux

package app

import "syscall"

func killGroup(pgid int, force bool) error {
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	err := syscall.Kill(-pgid, sig)
	if err == syscall.ESRCH {
		return nil
	}
	return err
}

func ownGroup() int { return syscall.Getpgrp() }

func diskUsage(path string) (free, total int64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	bsize := int64(st.Bsize)
	return int64(st.Bavail) * bsize, int64(st.Blocks) * bsize, nil
}
