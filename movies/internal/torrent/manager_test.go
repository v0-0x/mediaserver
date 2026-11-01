package torrent

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSplitCommand(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "My Apps")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "qbittorrent")
	shell, shellArgs := "sh", []string{"-c", "true"}
	if runtime.GOOS == "windows" {
		exe += ".exe"
		shell, shellArgs = "cmd", []string{"/c", "exit"}
	}
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		in   string
		prog string
		args []string
	}{
		{"  " + exe + "  ", exe, nil},
		{exe + " --profile=kumobox  -x", exe, []string{"--profile=kumobox", "-x"}},
		{shell + " " + strings.Join(shellArgs, " "), shell, shellArgs},
	} {
		prog, args, err := splitCommand(tc.in)
		if err != nil || prog != tc.prog || strings.Join(args, "|") != strings.Join(tc.args, "|") {
			t.Errorf("%q: %q %q %v", tc.in, prog, args, err)
		}
	}
	for _, in := range []string{"", "   ", "\t\n", filepath.Join(dir, "missing") + " --x", "kumobox-no-such-program-xyz"} {
		if _, _, err := splitCommand(in); err == nil {
			t.Errorf("%q: no error", in)
		}
	}
}
