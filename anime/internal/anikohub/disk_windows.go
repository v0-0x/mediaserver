package anikohub

// diskOf: a hub runs on Linux; on Windows (a test) disks aren't told.
func diskOf(path string) (Disk, uint64, bool) { return Disk{}, 0, false }
