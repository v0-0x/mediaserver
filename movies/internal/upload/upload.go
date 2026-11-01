// Package upload takes files sent from another computer into a library
// folder: a device's KumoBox, or a browser signed in to the server, sends
// them a piece at a time (Chunk at most), each piece after the last, so a
// large one never sits in memory and an upload cut short carries on where
// it stopped. A file is written next to its place as <name>.part (the
// library's scanner skips those) and takes its name once it's all there. A
// file is never written over, nor outside its folder.
package upload

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"unicode"
)

// Chunk is the most a piece may hold.
const Chunk = 8 << 20

// partSuffix marks a file still being sent.
const partSuffix = ".part"

// spare is the room kept free on the drive beyond the file.
const spare = 512 << 20

// Result is where a file stands: Have bytes are there (the next piece
// starts there); Done once it's all there, as Path (in its folder).
type Result struct {
	Path string `json:"path"`
	Have int64  `json:"have"`
	Done bool   `json:"done"`
}

// Error is a refusal, with the HTTP status that says it.
type Error struct {
	Status int
	Msg    string
	// Have: where the file stands, for a piece that doesn't follow on.
	Have int64
}

func (e *Error) Error() string { return e.Msg }

func refuse(status int, format string, args ...any) error {
	return &Error{Status: status, Msg: fmt.Sprintf(format, args...)}
}

// Receiver takes the pieces into folders. Pieces of one file are taken one
// at a time.
type Receiver struct {
	// Free reports the room left on the drive of a folder (nil: not
	// checked).
	Free func(dir string) (uint64, error)
	// Done runs when a file is all there (its full path).
	Done func(path string)

	mu   sync.Mutex
	busy map[string]bool
}

// Clean checks a file's path in its folder, as sent ("Show/Season 1/x.mkv",
// slashes either way) and returns it with this system's separators: no
// parent folders (..), hidden names or odd characters.
func Clean(rel string) (string, error) {
	rel = strings.ReplaceAll(rel, "\\", "/")
	if rel == "" || len(rel) > 1024 || strings.HasPrefix(rel, "/") {
		return "", refuse(400, "not a file's path: %q", rel)
	}
	parts := strings.Split(rel, "/")
	if len(parts) > 16 {
		return "", refuse(400, "too many folders in %q", rel)
	}
	for _, p := range parts {
		switch {
		case p == "", p == ".", p == "..", strings.HasPrefix(p, "."):
			return "", refuse(400, "not a file's path: %q", rel)
		case strings.TrimRight(p, " .") != p:
			return "", refuse(400, "a name can't end with a space or a dot: %q", p)
		case len(p) > 255:
			return "", refuse(400, "a name is too long: %q", p)
		}
		for _, r := range p {
			if unicode.IsControl(r) || strings.ContainsRune(`:*?"<>|`, r) {
				return "", refuse(400, "%q has a character a file name can't", p)
			}
		}
	}
	if strings.HasSuffix(strings.ToLower(rel), partSuffix) {
		return "", refuse(400, "%s files can't be sent", partSuffix)
	}
	return filepath.FromSlash(path.Clean(rel)), nil
}

// Status is where the file rel in dir stands (what an upload carries on
// from).
func (u *Receiver) Status(dir, rel string, total int64) (Result, error) {
	name, err := Clean(rel)
	if err != nil {
		return Result{}, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return Result{}, refuse(404, "the folder isn't there: %v", err)
	}
	defer root.Close()
	return status(root, name, rel, total)
}

func status(root *os.Root, name, rel string, total int64) (Result, error) {
	if st, err := root.Stat(name); err == nil {
		if st.Mode().IsRegular() && st.Size() == total {
			return Result{Path: rel, Have: total, Done: true}, nil
		}
		return Result{}, refuse(409, "%s is already there, and isn't the same file", rel)
	}
	if st, err := root.Stat(name + partSuffix); err == nil && st.Mode().IsRegular() && st.Size() <= total {
		return Result{Path: rel, Have: st.Size()}, nil
	}
	return Result{Path: rel}, nil
}

// Receive writes a piece of the file rel (total bytes in all) at offset, in
// dir. A piece that doesn't follow on from what's there is refused with
// the status 409 and where the file stands (Error.Have).
func (u *Receiver) Receive(dir, rel string, offset, total int64, piece io.Reader) (Result, error) {
	name, err := Clean(rel)
	if err != nil {
		return Result{}, err
	}
	if total < 0 || offset < 0 || offset > total {
		return Result{}, refuse(400, "a piece out of the file")
	}
	key := filepath.Join(dir, name)
	u.mu.Lock()
	if u.busy == nil {
		u.busy = map[string]bool{}
	}
	if u.busy[key] {
		u.mu.Unlock()
		return Result{}, refuse(409, "%s is being sent already", rel)
	}
	u.busy[key] = true
	u.mu.Unlock()
	defer func() {
		u.mu.Lock()
		delete(u.busy, key)
		u.mu.Unlock()
	}()

	root, err := os.OpenRoot(dir)
	if err != nil {
		return Result{}, refuse(404, "the folder isn't there: %v", err)
	}
	defer root.Close()
	st, err := status(root, name, rel, total)
	if err != nil || st.Done {
		return st, err
	}
	if offset != st.Have {
		return st, &Error{Status: 409, Msg: "the piece doesn't follow on from what's there", Have: st.Have}
	}
	if offset == 0 && u.Free != nil {
		if free, err := u.Free(dir); err == nil && free < uint64(total)+spare {
			return st, refuse(507, "there isn't room for %s on the server's drive (%d MB free)", rel, free>>20)
		}
	}
	if dirName := filepath.Dir(name); dirName != "." {
		if err := root.MkdirAll(dirName, 0o755); err != nil {
			return st, refuse(500, "couldn't make the folder: %v", err)
		}
	}
	f, err := root.OpenFile(name+partSuffix, os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return st, refuse(500, "couldn't write %s: %v", rel, err)
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		f.Close()
		return st, refuse(500, "couldn't write %s: %v", rel, err)
	}
	// No more than the file has left, nor than a piece holds.
	n, err := io.Copy(f, io.LimitReader(piece, min(total-offset, Chunk)+1))
	if err == nil && (n > Chunk || offset+n > total) {
		err = refuse(400, "the piece is larger than the file")
		n = 0
	}
	if err != nil {
		// What came before stays: the piece is sent again from there.
		_ = f.Truncate(offset)
		f.Close()
		var e *Error
		if errors.As(err, &e) {
			e.Have = offset
			return Result{Path: rel, Have: offset}, e
		}
		return Result{Path: rel, Have: offset}, refuse(500, "couldn't write %s: %v", rel, err)
	}
	st.Have = offset + n
	if st.Have < total {
		return st, f.Close()
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return st, refuse(500, "couldn't write %s: %v", rel, err)
	}
	if err := f.Close(); err != nil {
		return st, refuse(500, "couldn't write %s: %v", rel, err)
	}
	// Taken by another file meanwhile: this one stays a .part.
	if _, err := root.Stat(name); !errors.Is(err, fs.ErrNotExist) {
		return st, refuse(409, "%s is already there, and isn't the same file", rel)
	}
	if err := root.Rename(name+partSuffix, name); err != nil {
		return st, refuse(500, "couldn't finish %s: %v", rel, err)
	}
	st.Done = true
	if u.Done != nil {
		u.Done(filepath.Join(dir, name))
	}
	return st, nil
}
