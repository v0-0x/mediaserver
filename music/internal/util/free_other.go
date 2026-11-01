//go:build !unix

// Package util has what the music side needs of the system.
package util

import "errors"

// Free is the room left on the drive of dir (not known here).
func Free(dir string) (uint64, error) { return 0, errors.New("not known on this system") }
