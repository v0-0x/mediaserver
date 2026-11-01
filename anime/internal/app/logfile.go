package app

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// LogFile is the server's log on disk (a hub's, for its dashboard's audit
// log): every line, in files of logFileMax, the newest logFiles kept
// (aniko.log, then aniko.log.1, the one before, …).
type LogFile struct {
	mu   sync.Mutex
	dir  string
	f    *os.File
	size int64
}

const (
	logFileMax = 2 << 20
	logFiles   = 10
	logName    = "aniko.log"
)

// OpenLogFile opens (or starts) the log in dir.
func OpenLogFile(dir string) (*LogFile, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	l := &LogFile{dir: dir}
	return l, l.open()
}

func (l *LogFile) open() error {
	f, err := os.OpenFile(filepath.Join(l.dir, logName), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	l.f, l.size = f, st.Size()
	return nil
}

func (l *LogFile) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return len(p), nil
	}
	if l.size > 0 && l.size+int64(len(p)) > logFileMax {
		l.rotate()
	}
	n, err := l.f.Write(p)
	l.size += int64(n)
	return n, err
}

// rotate starts a new file: aniko.log becomes aniko.log.1, and so on; the
// oldest goes.
func (l *LogFile) rotate() {
	_ = l.f.Close()
	l.f = nil
	_ = os.Remove(l.name(logFiles - 1))
	for i := logFiles - 2; i >= 0; i-- {
		_ = os.Rename(l.name(i), l.name(i+1))
	}
	_ = l.open()
}

func (l *LogFile) name(i int) string {
	if i == 0 {
		return filepath.Join(l.dir, logName)
	}
	return filepath.Join(l.dir, fmt.Sprintf("%s.%d", logName, i))
}

// Lines are the log's lines, the newest first, that match a search (every
// word of it) or, with errors, that say something went wrong; skip and
// limit page through them. more: there are older ones.
func (l *LogFile) Lines(search string, errors bool, skip, limit int) (lines []string, more bool, err error) {
	words := strings.Fields(strings.ToLower(search))
	var all []string
	err = l.each(func(line string) {
		low := strings.ToLower(line)
		if errors && !looksWrong(low) {
			return
		}
		for _, w := range words {
			if !strings.Contains(low, w) {
				return
			}
		}
		all = append(all, line)
	})
	for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
		all[i], all[j] = all[j], all[i]
	}
	if skip >= len(all) {
		return []string{}, false, err
	}
	all = all[skip:]
	if limit > 0 && len(all) > limit {
		return all[:limit], true, err
	}
	return all, false, err
}

// looksWrong: a log line that says something went wrong.
func looksWrong(low string) bool {
	for _, w := range []string{"error", "fail", "couldn't", "can't", "cannot", "panic", "refused", "denied", "forbidden", "timeout", "timed out", "kept out", "wrong password"} {
		if strings.Contains(low, w) {
			return true
		}
	}
	return false
}

// WriteTo writes the whole log, oldest first (to download it).
func (l *LogFile) WriteTo(w io.Writer) (int64, error) {
	var n int64
	err := l.each(func(line string) {
		m, _ := io.WriteString(w, line+"\n")
		n += int64(m)
	})
	return n, err
}

// each runs fn on every line, the oldest first. The files are opened
// together (a rotation can't come between them), then read without
// holding up the server's logging.
func (l *LogFile) each(fn func(string)) error {
	var files []*os.File
	defer func() {
		for _, f := range files {
			f.Close()
		}
	}()
	l.mu.Lock()
	for i := logFiles - 1; i >= 0; i-- {
		f, err := os.Open(l.name(i))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			l.mu.Unlock()
			return err
		}
		files = append(files, f)
	}
	l.mu.Unlock()
	for _, f := range files {
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			fn(sc.Text())
		}
	}
	return nil
}

// Close closes the file.
func (l *LogFile) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}
