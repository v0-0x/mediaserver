//go:build !windows && !darwin

package config

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// Without $XDG_RUNTIME_DIR the runtime folder is in the shared temporary
// folder, where another user may have made it first: the shell token must
// never be written into a folder someone else owns or can read.
func TestRuntimeDirInSharedTmp(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("TMPDIR", tmp)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	shared := filepath.Join(tmp, "kumobox")

	t.Run("own folder, open to others", func(t *testing.T) {
		if err := os.Mkdir(shared, 0o777); err != nil {
			t.Fatal(err)
		}
		_ = os.Chmod(shared, 0o777) // past the umask
		defer os.RemoveAll(shared)
		got := RuntimeDir()
		if got != shared {
			t.Fatalf("RuntimeDir = %s, want %s", got, shared)
		}
		if st, _ := os.Stat(got); st.Mode().Perm() != 0o700 {
			t.Fatalf("mode = %v, want 0700", st.Mode().Perm())
		}
	})

	t.Run("another user's folder", func(t *testing.T) {
		if os.Getuid() != 0 {
			t.Skip("needs root to give the folder to another user")
		}
		if err := os.Mkdir(shared, 0o777); err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(shared)
		if err := os.Chown(shared, 12345, 12345); err != nil {
			t.Fatal(err)
		}
		got := RuntimeDir()
		if got == shared {
			t.Fatalf("RuntimeDir used %s, owned by another user", got)
		}
		st, err := os.Lstat(got)
		if err != nil || !st.IsDir() || st.Mode().Perm() != 0o700 || st.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
			t.Fatalf("fallback %s: %v %v", got, st, err)
		}
	})

	t.Run("a symlink", func(t *testing.T) {
		elsewhere := t.TempDir()
		if err := os.Symlink(elsewhere, shared); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(shared)
		if got := RuntimeDir(); got == shared {
			t.Fatalf("RuntimeDir followed the symlink %s", got)
		}
	})
}
