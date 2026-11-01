//go:build linux && !android

package awake

import (
	"testing"
	"time"
)

// The program reading from Aniko ends once Aniko's end of the pipe closes (as
// when Aniko crashes), not only when it's stopped.
func TestInhibitorEndsWithAniko(t *testing.T) {
	c := &cmdInhibitor{args: []string{"cat"}, stdin: true}
	if err := c.start(); err != nil {
		t.Skip("no cat:", err)
	}
	c.mu.Lock()
	pipe := c.pipe
	c.mu.Unlock()
	time.Sleep(100 * time.Millisecond)
	c.mu.Lock()
	running := c.cmd != nil
	c.mu.Unlock()
	if !running {
		t.Fatal("ended at once")
	}
	// Aniko gone: its end of the pipe closed by the system.
	_ = pipe.Close()
	for i := 0; i < 50; i++ {
		c.mu.Lock()
		gone := c.cmd == nil
		c.mu.Unlock()
		if gone {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("it went on without Aniko")
}
