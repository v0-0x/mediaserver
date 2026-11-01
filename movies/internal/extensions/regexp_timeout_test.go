package extensions

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A catastrophic-backtracking regex that forces goja onto the regexp2
// backtracking engine (the lookahead can't be lowered to RE2) must not run
// uninterruptibly: the match timeout turns it into a bounded no-match so
// control returns to the VM. Without it this pins a CPU core and the call
// only returns when its 90s hard timeout fires, with the match goroutine
// leaking (running, effectively, forever).
func TestEvilRegexDoesNotRunUninterruptibly(t *testing.T) {
	src := "class Provider { go() { return /^(a+)+(?=b)/.test('" + strings.Repeat("a", 100) + "!'); } }"
	rt := testRuntime(t, src, RuntimeOptions{})
	start := time.Now()
	done := make(chan error, 1)
	go func() {
		_, err := rt.CallProvider(context.Background(), "go")
		done <- err
	}()
	budget := regexpMatchTimeout + 15*time.Second
	select {
	case <-done:
		if d := time.Since(start); d > budget {
			t.Fatalf("regex match took %s, want it bounded near %s", d, regexpMatchTimeout)
		}
	case <-time.After(budget):
		t.Fatalf("regex match did not return within %s: the match timeout is not enforced", budget)
	}
}
