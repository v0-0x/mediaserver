//go:build unix

// Package util has what the music side needs of the system.
package util

import "golang.org/x/sys/unix"

// Free is the room left on the drive of dir, in bytes.
func Free(dir string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}
