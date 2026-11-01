package anikohub

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/anime/internal/db"
	"github.com/v0-0x/mediaserver/anime/internal/history"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "aniko.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	s, err := New(d)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Each account's history is its own; a position is taken only when newer,
// and stored in order (After).
func TestHistoryPerAccount(t *testing.T) {
	s := newStore(t)
	now := time.Now().Unix()
	a, b := s.History(1), s.History(2)
	if m, _ := a.MergeAll([]history.Entry{{MediaID: 21, Episode: 1, Position: 100, Duration: 1400, UpdatedAt: now - 10}}); len(m) != 1 {
		t.Fatalf("first position not taken: %v", m)
	}
	if m, _ := b.MergeAll([]history.Entry{{MediaID: 21, Episode: 1, Position: 900, Duration: 1400, UpdatedAt: now - 5}}); len(m) != 1 {
		t.Fatalf("another account's not taken: %v", m)
	}
	// Older: not taken.
	if m, _ := a.MergeAll([]history.Entry{{MediaID: 21, Episode: 1, Position: 50, Duration: 1400, UpdatedAt: now - 20}}); len(m) != 0 {
		t.Fatalf("an older position was taken: %v", m)
	}
	if got := a.Latest(10); len(got) != 1 || got[0].Position != 100 {
		t.Fatalf("account 1: %+v", got)
	}
	if got := b.Latest(10); len(got) != 1 || got[0].Position != 900 {
		t.Fatalf("account 2: %+v", got)
	}
	// Newer: taken, and after the last seq seen.
	seen := a.Latest(1)[0].Seq
	if m, _ := a.MergeAll([]history.Entry{{MediaID: 21, Episode: 1, Position: 200, Duration: 1400, UpdatedAt: now}, {MediaID: 21, Episode: 2, Position: 10, Duration: 1400, UpdatedAt: now}}); len(m) != 2 {
		t.Fatalf("newer positions: %v", m)
	}
	if got := a.After(seen, 10); len(got) != 2 || got[0].Seq <= seen {
		t.Fatalf("after %d: %+v", seen, got)
	}
	if got := b.After(0, 10); len(got) != 1 {
		t.Fatalf("account 2 sees account 1's: %+v", got)
	}
	if n := s.HistoryCounts(); n[1] != 2 || n[2] != 1 {
		t.Fatalf("counts: %v", n)
	}
}

// Settings are taken when newer; a device gets the login chosen for it, or
// the first given.
func TestPrefsAndAccounts(t *testing.T) {
	s := newStore(t)
	data := json.RawMessage(`{"ui":{"colorTheme":"sakura"}}`)
	if ok, _ := s.SetPrefs(7, Prefs{Data: data, UpdatedAt: 2000}); !ok {
		t.Fatal("prefs not taken")
	}
	if ok, _ := s.SetPrefs(7, Prefs{Data: json.RawMessage(`{"ui":{"colorTheme":"ocean"}}`), UpdatedAt: 1000}); ok {
		t.Fatal("older prefs taken")
	}
	if p := s.Prefs(7); p == nil || string(p.Data) != string(data) || s.PrefsAt(7) != 2000 {
		t.Fatalf("prefs: %+v", p)
	}
	if s.Prefs(8) != nil || s.PrefsAt(8) != 0 {
		t.Fatal("another account has prefs")
	}

	if s.DeviceAccount("tv") != 0 {
		t.Fatal("a login with none given")
	}
	_ = s.SaveAccount(Account{User: 7, Name: "me", AddedAt: 1}, "tok7")
	_ = s.SaveAccount(Account{User: 9, Name: "roommate", AddedAt: 2}, "tok9")
	if s.DeviceAccount("tv") != 7 {
		t.Fatalf("default login: %d", s.DeviceAccount("tv"))
	}
	_ = s.SetDeviceAccount("tv", 9)
	if s.DeviceAccount("tv") != 9 || s.Token(9) != "tok9" {
		t.Fatalf("chosen login: %d", s.DeviceAccount("tv"))
	}
	// Removed: the device falls back to the first.
	_ = s.RemoveAccount(9)
	if s.DeviceAccount("tv") != 7 || len(s.Accounts()) != 1 {
		t.Fatalf("after removing: %d %v", s.DeviceAccount("tv"), s.Accounts())
	}
}

// The setup code and the session secret stay the same.
func TestOwnerSecrets(t *testing.T) {
	s := newStore(t)
	c := s.SetupCode()
	if len(c) != 9 || c[4] != '-' || s.SetupCode() != c {
		t.Fatalf("setup code %q", c)
	}
	if k := s.SessionSecret(); len(k) < 32 || s.SessionSecret() != k {
		t.Fatalf("session secret %q", k)
	}
}

// A stream counts what it sends; a session's (Keyed) is one stream.
func TestStreams(t *testing.T) {
	st := NewStreams()
	a := st.Begin("TV", "/lib/One Piece - 1051.mkv", "file")
	a.Add(3 << 20)
	if l := st.Live(); len(l) != 1 || l[0].File != "One Piece - 1051.mkv" || l[0].Bytes != 3<<20 {
		t.Fatalf("live: %+v", l)
	}
	a.End()
	if len(st.Live()) != 0 || len(st.Recent()) != 1 || st.Sent() != 3<<20 {
		t.Fatalf("after end: %+v %+v", st.Live(), st.Recent())
	}
	st.Label("s1", "/lib/x.mkv", "transcode")
	k := st.Keyed("s1", "PC")
	if st.Keyed("s1", "PC") != k {
		t.Fatal("a session's segments are two streams")
	}
	if l := st.Live(); len(l) != 1 || l[0].Kind != "transcode" {
		t.Fatalf("keyed: %+v", l)
	}
}

// A server's response writer, which sends what's read from a reader itself.
type readerFromWriter struct{ *httptest.ResponseRecorder }

func (w readerFromWriter) ReadFrom(r io.Reader) (int64, error) { return io.Copy(w.ResponseRecorder, r) }

// Converted video (from a pipe) is counted as it goes, not when it ends.
func TestStreamCountsAsItGoes(t *testing.T) {
	st := NewStreams().Begin("PC", "/lib/x.mkv", "transcode")
	var w http.ResponseWriter = readerFromWriter{httptest.NewRecorder()}
	w = st.Writer(w)
	pr, pw := io.Pipe()
	done := make(chan struct{})
	go func() {
		_, _ = w.(io.ReaderFrom).ReadFrom(pr)
		close(done)
	}()
	_, _ = pw.Write(make([]byte, 1000))
	deadline := time.Now().Add(2 * time.Second)
	for st.bytes.Load() < 1000 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if n := st.bytes.Load(); n != 1000 {
		t.Fatalf("counted %d of the 1000 bytes sent so far", n)
	}
	_ = pw.Close()
	<-done
}
