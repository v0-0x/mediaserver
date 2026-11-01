//go:build (linux && !android) || darwin

package awake

import (
	"errors"
	"os"
	"os/exec"
	"sync"
)

// A program that keeps the system awake while it runs: systemd-inhibit on
// Linux, caffeinate on macOS.
type cmdInhibitor struct {
	mu   sync.Mutex
	args []string
	cmd  *exec.Cmd
	// stdin: its input is a pipe from Aniko, open while it runs (closed when
	// Aniko ends, however it ends).
	stdin bool
	pipe  *os.File
}

func (c *cmdInhibitor) start() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cmd != nil {
		return nil
	}
	path, err := exec.LookPath(c.args[0])
	if err != nil {
		return errors.New(c.args[0] + " isn't installed")
	}
	cmd := exec.Command(path, c.args[1:]...)
	var r, w *os.File
	if c.stdin {
		if r, w, err = os.Pipe(); err != nil {
			return err
		}
		cmd.Stdin = r
	}
	err = cmd.Start()
	if r != nil {
		_ = r.Close() // the program's now
	}
	if err != nil {
		if w != nil {
			_ = w.Close()
		}
		return err
	}
	c.cmd, c.pipe = cmd, w
	// Reaped when it ends (stopped, or the system ended it).
	go func() {
		_ = cmd.Wait()
		c.mu.Lock()
		if c.cmd == cmd {
			c.cmd = nil
			if c.pipe != nil {
				_ = c.pipe.Close()
				c.pipe = nil
			}
		}
		c.mu.Unlock()
	}()
	return nil
}

func (c *cmdInhibitor) stop() {
	c.mu.Lock()
	cmd, pipe := c.cmd, c.pipe
	c.cmd, c.pipe = nil, nil
	c.mu.Unlock()
	if pipe != nil {
		_ = pipe.Close()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
