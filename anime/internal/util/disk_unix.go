//go:build linux || darwin

package util

import "syscall"

// DiskSpace is the size of the drive a path is on, and how much of it is
// free (for this user).
func DiskSpace(path string) (total, free uint64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	return uint64(st.Blocks) * uint64(st.Bsize), uint64(st.Bavail) * uint64(st.Bsize), nil
}
