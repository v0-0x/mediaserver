package winsetup

import (
	"bytes"
	"regexp"
	"runtime"
	"testing"

	"github.com/v0-0x/mediaserver/movies/internal/config"
)

// Windows PowerShell reads a script without a byte order mark in the
// computer's code page: anything but ASCII comes out garbled.
func TestScriptIsASCII(t *testing.T) {
	for i, b := range script {
		if b >= 0x80 {
			line := bytes.Count(script[:i], []byte("\n")) + 1
			t.Fatalf("install-tools.ps1 line %d has a non-ASCII byte", line)
		}
	}
}

// The script installs what KumoBox uses, and nothing it no longer does.
func TestScriptInstalls(t *testing.T) {
	for _, want := range []string{"scoop install", "bucket add extras", `Install "ffmpeg"`, `Install "mpv"`, `Install "yt-dlp"`, "gh auth login"} {
		if !bytes.Contains(script, []byte(want)) {
			t.Errorf("install-tools.ps1 lacks %q", want)
		}
	}
	for _, gone := range []string{"ani-cli", "fzf", "aria2"} {
		if bytes.Contains(script, []byte(gone)) {
			t.Errorf("install-tools.ps1 still mentions %q, which KumoBox doesn't use", gone)
		}
	}
}

// The script checks the repository KumoBox updates from.
func TestScriptRepo(t *testing.T) {
	m := regexp.MustCompile(`\[string\]\$Repo = "([^"]+)"`).FindSubmatch(script)
	if m == nil {
		t.Fatal("install-tools.ps1 has no $Repo parameter with a default")
	}
	if string(m[1]) != config.UpdateRepo {
		t.Errorf("install-tools.ps1's $Repo is %q, KumoBox updates from %q", m[1], config.UpdateRepo)
	}
}

func TestStartIsForWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("it would open a PowerShell window")
	}
	if Start() == nil {
		t.Error("Start worked outside Windows")
	}
	if Running() {
		t.Error("running outside Windows")
	}
}
