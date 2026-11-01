//go:build !(linux || darwin || windows)

package util

import "errors"

// DiskSpace isn't known here.
func DiskSpace(path string) (total, free uint64, err error) {
	return 0, 0, errors.New("not supported")
}
