//go:build !windows

package anikohub

import "syscall"

// diskOf is the disk a folder is on, and its device number (to tell each
// disk once).
func diskOf(path string) (Disk, uint64, bool) {
	var fs syscall.Statfs_t
	var st syscall.Stat_t
	if syscall.Statfs(path, &fs) != nil || syscall.Stat(path, &st) != nil {
		return Disk{}, 0, false
	}
	bs := int64(fs.Bsize)
	return Disk{Path: path, Total: int64(fs.Blocks) * bs, Free: int64(fs.Bavail) * bs}, uint64(st.Dev), true
}
