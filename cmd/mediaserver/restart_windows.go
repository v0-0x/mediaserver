//go:build windows

package main

import "errors"

// canReexec: Windows can't replace a running program with a new one: the
// service manager starts mediaserver again (it exits with codeRestart).
const canReexec = false

func reexec() error { return errors.ErrUnsupported }
