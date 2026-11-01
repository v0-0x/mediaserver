//go:build !windows

package util

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// BindChildren is for Windows; here a killed KumoBox's children end with the
// desktop app's process group, or keep running like any orphan.
func BindChildren() error { return nil }

func detachAttrs(cmd *exec.Cmd) {
	// A session of its own, so Ctrl+C on KumoBox's terminal doesn't close it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// startDetached starts a detached command, and returns the one started.
func startDetached(cmd *exec.Cmd) (*exec.Cmd, error) { return cmd, cmd.Start() }

// OwnProcessGroup makes cmd the leader of a new process group, so that
// KillGroup stops it and everything it started.
func OwnProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// KillGroup kills a process started with OwnProcessGroup and the processes
// it started.
func KillGroup(p *os.Process) error {
	err := syscall.Kill(-p.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}

// Suspend pauses a process until Resume.
func Suspend(p *os.Process) error { return p.Signal(syscall.SIGSTOP) }

// Resume continues a process paused with Suspend.
func Resume(p *os.Process) error { return p.Signal(syscall.SIGCONT) }

// openCommand opens a folder or a web address with the desktop's app for it.
func openCommand(target string) *exec.Cmd {
	name, args := opener(runtime.GOOS, target)
	return exec.Command(name, args...)
}

// opener is the program, and its arguments, that opens target on goos.
// macOS's open starts an app or installs a plugin it's given (a folder
// named Something.app, which a torrent can have) rather than showing it:
// those are shown in Finder, selected, instead.
func opener(goos, target string) (string, []string) {
	if goos != "darwin" {
		return "xdg-open", []string{target}
	}
	if isBundle(target) {
		return "open", []string{"-R", target}
	}
	return "open", []string{target}
}

// macBundles are the folder extensions macOS opens as an app or a plugin.
var macBundles = map[string]bool{
	".app": true, ".appex": true, ".bundle": true, ".plugin": true, ".framework": true, ".kext": true,
	".prefpane": true, ".saver": true, ".qlgenerator": true, ".mdimporter": true, ".workflow": true, ".action": true,
}

// isBundle reports whether target is a folder macOS treats as a bundle, by
// its own name or, for a symlink, the name of the folder it points at.
func isBundle(target string) bool {
	if st, err := os.Stat(target); err != nil || !st.IsDir() {
		return false
	}
	names := []string{filepath.Clean(target)}
	if real, err := filepath.EvalSymlinks(target); err == nil {
		names = append(names, real)
	}
	for _, n := range names {
		if macBundles[strings.ToLower(filepath.Ext(n))] {
			return true
		}
	}
	return false
}

// DialIPC connects to a local IPC endpoint: a Unix socket here, a named pipe
// on Windows.
func DialIPC(addr string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout("unix", addr, timeout)
}

// lookAbs checks a program given by its path.
func lookAbs(p string) (string, bool) {
	st, err := os.Stat(p)
	if err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
		return p, true
	}
	return "", false
}
