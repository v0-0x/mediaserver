//go:build windows

package awake

import (
	"runtime"
	"syscall"
)

const (
	esContinuous     = 0x80000000
	esSystemRequired = 0x00000001
)

var setThreadExecutionState = syscall.NewLazyDLL("kernel32.dll").NewProc("SetThreadExecutionState")

// SetThreadExecutionState asks Windows not to sleep for as long as the thread
// that called it lives (or calls it again): one thread of its own, kept.
type winInhibitor struct {
	done chan struct{}
}

func newInhibitor() inhibitor { return &winInhibitor{} }

func (w *winInhibitor) start() error {
	if w.done != nil {
		return nil
	}
	if err := setThreadExecutionState.Find(); err != nil {
		return err
	}
	done := make(chan struct{})
	ok := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		r, _, err := setThreadExecutionState.Call(esContinuous | esSystemRequired)
		if r == 0 {
			ok <- err
			return
		}
		ok <- nil
		<-done
		setThreadExecutionState.Call(esContinuous)
	}()
	if err := <-ok; err != nil {
		return err
	}
	w.done = done
	return nil
}

func (w *winInhibitor) stop() {
	if w.done != nil {
		close(w.done)
		w.done = nil
	}
}
