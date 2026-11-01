package share

import "syscall"

// reuseAddr does nothing on Windows, where SO_REUSEADDR would let another
// program take the port over: a second Aniko on the same computer isn't
// found.
func reuseAddr(_, _ string, _ syscall.RawConn) error { return nil }
