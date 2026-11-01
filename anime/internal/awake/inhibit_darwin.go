//go:build darwin

package awake

import (
	"os"
	"strconv"
)

// caffeinate -i: no idle sleep while it runs (the screen may still turn
// off); -w: only while Aniko runs (it ends with Aniko, also when Aniko crashes).
func newInhibitor() inhibitor {
	return &cmdInhibitor{args: []string{"caffeinate", "-i", "-w", strconv.Itoa(os.Getpid())}}
}
