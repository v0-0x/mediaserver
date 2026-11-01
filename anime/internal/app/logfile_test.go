package app

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every line kept on disk, in files that start over at logFileMax (the
// oldest going past logFiles); read back the newest first, a page at a
// time, searched, or only what went wrong, and whole to download.
func TestLogFile(t *testing.T) {
	dir := t.TempDir()
	l, err := OpenLogFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() }) // Windows deletes no open file
	pad := strings.Repeat("x", 1000)
	n := 3 * logFileMax / 1000
	for i := 0; i < n; i++ {
		msg := "scan done"
		if i%100 == 0 {
			msg = "download failed"
		}
		fmt.Fprintf(l, "2026/10/30 10:00:00 line %d %s %s\n", i, msg, pad)
	}
	if _, err := os.Stat(filepath.Join(dir, "aniko.log.2")); err != nil {
		t.Fatalf("not rotated: %v", err)
	}
	for _, name := range []string{"aniko.log", "aniko.log.1"} {
		if st, _ := os.Stat(filepath.Join(dir, name)); st.Size() > logFileMax {
			t.Errorf("%s is %d bytes, over %d", name, st.Size(), logFileMax)
		}
	}

	lines, more, err := l.Lines("", false, 0, 10)
	if err != nil || len(lines) != 10 || !more || !strings.Contains(lines[0], fmt.Sprintf("line %d ", n-1)) {
		t.Fatalf("newest page: %d lines, more %v, %v, first %.40q", len(lines), more, err, lines[0])
	}
	next, _, _ := l.Lines("", false, 10, 1)
	if !strings.Contains(next[0], fmt.Sprintf("line %d ", n-11)) {
		t.Errorf("second page starts with %.40q", next[0])
	}
	all, more, _ := l.Lines("", false, 0, 0)
	if len(all) != n || more {
		t.Errorf("all: %d lines, want %d", len(all), n)
	}
	wrong, _, _ := l.Lines("", true, 0, 0)
	if len(wrong) != (n+99)/100 {
		t.Errorf("errors only: %d lines", len(wrong))
	}
	found, _, _ := l.Lines("LINE 1234 scan", false, 0, 0)
	if len(found) != 1 {
		t.Errorf("search: %d lines", len(found))
	}

	var b bytes.Buffer
	if _, err := l.WriteTo(&b); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(b.String(), "2026/10/30 10:00:00 line 0 ") || strings.Count(b.String(), "\n") != n {
		t.Errorf("download: starts %.40q, %d lines", b.String(), strings.Count(b.String(), "\n"))
	}

	// Reopened (the hub restarting): carries on in the same file.
	l.Close()
	l2, err := OpenLogFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l2.Close() })
	fmt.Fprintln(l2, "started again")
	if lines, _, _ := l2.Lines("", false, 0, 2); lines[0] != "started again" || !strings.Contains(lines[1], fmt.Sprintf("line %d ", n-1)) {
		t.Errorf("after reopening: %.40q", lines)
	}
}

// Past logFiles files, the oldest goes.
func TestLogFileKeepsTen(t *testing.T) {
	dir := t.TempDir()
	l, err := OpenLogFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	big := strings.Repeat("y", logFileMax-10) + "\n"
	for i := 0; i < logFiles+3; i++ {
		l.Write([]byte(big))
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != logFiles {
		t.Errorf("%d files, want %d", len(entries), logFiles)
	}
}
