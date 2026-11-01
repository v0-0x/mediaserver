//go:build !windows

package macsetup

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSetupRunsInTerminal(t *testing.T) {
	tmp := t.TempDir()
	var opens []string
	dir, open, goos = func() string { return tmp }, func(p string) error { opens = append(opens, p); return nil }, "darwin"
	toolsDir = func() string { return "/Users/me/Library/Application Support/KumoBox's/bin" }
	t.Cleanup(func() { opened = time.Time{} })

	if Running() {
		t.Fatal("running before it was opened")
	}
	if err := Start(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(tmp, name)
	if len(opens) != 1 || opens[0] != path {
		t.Fatalf("opened %q, want %q", opens, path)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("script %v (%v): want it executable by its owner only", st.Mode(), err)
	}
	b, _ := os.ReadFile(path)
	want := bytes.Replace(script, []byte("'@KUMOBOX_BIN@'"), []byte(`'/Users/me/Library/Application Support/KumoBox'\''s/bin'`), 1)
	if bytes.Equal(want, script) || !bytes.Equal(b, want) {
		t.Error("the script isn't install-tools.command with KumoBox's programs folder in it")
	}
	// The shell reads the folder back as it is.
	var line string
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "KUMOBOX_BIN=") {
			line = l
		}
	}
	out, err := exec.Command("bash", "-c", line+`; printf %s "$KUMOBOX_BIN"`).Output()
	if err != nil || string(out) != "/Users/me/Library/Application Support/KumoBox's/bin" {
		t.Errorf("%s: KUMOBOX_BIN = %q (%v)", line, out, err)
	}
	// Until the script wrote its pid file, it counts as running.
	if !Running() || Start() != ErrRunning {
		t.Fatal("not running right after it was opened")
	}

	// The script runs (a stand-in process writes its pid file).
	sh := exec.Command("sleep", "30")
	if err := sh.Start(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".pid", []byte(strconv.Itoa(sh.Process.Pid)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	opened = time.Now().Add(-time.Hour)
	if !Running() {
		t.Fatal("not running while its process is")
	}
	// The Terminal window was closed: the process is gone.
	_ = sh.Process.Kill()
	_ = sh.Wait()
	if Running() {
		t.Fatal("still running after its process ended")
	}
	if err := Start(); err != nil {
		t.Fatalf("can't open it again: %v", err)
	}
	if len(opens) != 2 {
		t.Errorf("opened %d times, want 2", len(opens))
	}
}

// The script gets what KumoBox uses, nothing it no longer does, and parses
// in bash (macOS has bash 3.2: the release workflow checks with that one).
func TestScriptPrograms(t *testing.T) {
	for _, want := range []string{"ffmpeg_tool ffmpeg", "ffmpeg_tool ffprobe", "yt-dlp_macos", "install mpv"} {
		if !bytes.Contains(script, []byte(want)) {
			t.Errorf("install-tools.command lacks %q", want)
		}
	}
	for _, gone := range []string{"ani-cli", "fzf"} {
		if bytes.Contains(script, []byte(gone)) {
			t.Errorf("install-tools.command still mentions %q, which KumoBox doesn't use", gone)
		}
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	cmd := exec.Command("bash", "-n")
	cmd.Stdin = bytes.NewReader(script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("bash -n install-tools.command: %v\n%s", err, out)
	}
}

func TestSetupIsForMacOS(t *testing.T) {
	tmp := t.TempDir()
	dir, open, goos = func() string { return tmp }, func(string) error { t.Fatal("opened"); return nil }, "linux"
	if err := Start(); err == nil {
		t.Fatal("Start opened the setup on Linux")
	}
}
