//go:build windows

package util

import "golang.org/x/sys/windows"

// DiskSpace is the size of the drive a path is on, and how much of it is
// free (for this user).
func DiskSpace(path string) (total, free uint64, err error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, err
	}
	var avail, size, all uint64
	if err := windows.GetDiskFreeSpaceEx(p, &avail, &size, &all); err != nil {
		return 0, 0, err
	}
	return size, avail, nil
}
