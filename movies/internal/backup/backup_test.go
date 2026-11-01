package backup

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/config"
	"github.com/v0-0x/mediaserver/movies/internal/db"
	"github.com/v0-0x/mediaserver/movies/internal/history"
)

type kumobox struct {
	d     *db.DB
	hist  *history.Store
	store *config.Store
	dir   string
}

func newKumoBox(t *testing.T) *kumobox {
	t.Helper()
	dir := t.TempDir()
	d, err := db.Open(filepath.Join(dir, "kumobox.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	store, err := config.NewStore(d)
	if err != nil {
		t.Fatal(err)
	}
	return &kumobox{d: d, hist: history.NewStore(d), store: store, dir: dir}
}

func (k *kumobox) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := k.d.Write(q, args...); err != nil {
		t.Fatal(err)
	}
}

func (k *kumobox) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := k.d.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A backup goes into another KumoBox: what's newer there stays, its network
// and sharing settings too; logins only when asked; the data from before
// is saved.
func TestBackupRoundTrip(t *testing.T) {
	a := newKumoBox(t)
	now := time.Now().Unix()
	cfg := a.store.Get()
	cfg.Playback.AutoPlayNext = true
	cfg.Server.Port = 41000
	cfg.Qbittorrent.Password = "secret"
	cfg.Library.Dir = a.dir // exists on both (a test's temp folder)
	if _, err := a.store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	_ = a.d.SetKV("letterboxd:token", "tok")
	_ = a.d.SetKV("share:identity", map[string]string{"id": "abc"})
	_ = a.d.SetKV(hiddenKey, map[string]map[string]int64{"5": {"episode": 3, "hiddenAt": now - 100}})
	a.exec(t, `INSERT INTO watch_history(media_id, episode, position, duration, source, updated_at, seq) VALUES(1, 1, 600, 1400, 'local', ?, 1), (1, 2, 100, 1400, 'local', ?, 2)`, now-50, now-500)
	a.exec(t, `INSERT INTO local_list(media_id, status, progress, media, updated_at) VALUES(7, 'CURRENT', 3, '{}', ?)`, now-10)
	a.exec(t, `INSERT INTO track_prefs(media_id, audio_lang, updated_at) VALUES(7, 'jpn', ?)`, now)
	a.exec(t, `INSERT INTO local_files(path, dir, name, media_id, episode, locked) VALUES('/a/x.mkv', '/a', 'x.mkv', 9, 4, 1), ('/a/y.mkv', '/a', 'y.mkv', 9, 5, 0)`)
	a.exec(t, `INSERT INTO local_files(path, dir, name, media_id, episode, ignored) VALUES('/a/w.mkv', '/a', 'w.mkv', 0, 1, 1)`)
	a.exec(t, `INSERT INTO autodownload_rules(data) VALUES('{"title":"A"}'), ('{"title":"B"}')`)
	a.exec(t, `INSERT INTO autodownload_items(key, rule_id, title, created_at) VALUES('k1', 2, 'B 01', ?)`, now)
	a.exec(t, `INSERT INTO extensions(id, manifest, payload, installed_at, updated_at) VALUES('ext.one', '{}', 'code', ?, ?)`, now, now)
	a.exec(t, `INSERT INTO cache(key, value, expires_at) VALUES('c', 'v', ?)`, now+100)

	f, err := Make(a.d, a.store.Get(), false, "1.0.140", "PC")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(f); err != nil {
		t.Fatal(err)
	}
	if s := buf.String(); strings.Contains(s, "secret") || strings.Contains(s, "tok") || strings.Contains(s, "abc") || strings.Contains(s, `"c"`) {
		t.Fatalf("a backup without logins holds them, or the cache or identity: %s", s)
	}
	if got := len(f.Tables["local_files"]); got != 2 {
		t.Errorf("files kept: %d, want only the one matched by hand", got)
	}

	b := newKumoBox(t)
	bcfg := b.store.Get()
	bcfg.Server.Port = 42000
	if _, err := b.store.Save(bcfg); err != nil {
		t.Fatal(err)
	}
	// b watched episode 1 further, later; it has a rule the same as A.
	b.exec(t, `INSERT INTO watch_history(media_id, episode, position, duration, source, updated_at, seq) VALUES(1, 1, 1300, 1400, 'local', ?, 1)`, now)
	b.exec(t, `INSERT INTO autodownload_rules(data) VALUES('{"title":"Z"}'), ('{"title":"A"}')`)
	// Since the backup, w.mkv was un-ignored and matched by hand here.
	b.exec(t, `INSERT INTO local_files(path, dir, name, media_id, episode, locked, ignored) VALUES('/a/w.mkv', '/a', 'w.mkv', 42, 1, 1, 0)`)

	read, err := Read(&buf)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := Restore(b.d, b.hist, b.store, read, filepath.Join(b.dir, "backups"), "1.0.140", "Laptop")
	if err != nil {
		t.Fatal(err)
	}
	if sum.History != 1 || sum.List != 1 || sum.Tracks != 1 || sum.Matches != 1 || sum.Rules != 1 || sum.Extensions != 1 || !sum.Settings || sum.Logins {
		t.Errorf("summary: %+v", sum)
	}
	if e := b.hist.Get(1, 1); e == nil || e.Position != 1300 {
		t.Errorf("a newer position here was replaced: %+v", e)
	}
	if e := b.hist.Get(1, 2); e == nil || e.Position != 100 {
		t.Errorf("position not restored: %+v", e)
	}
	got := b.store.Get()
	if !got.Playback.AutoPlayNext || got.Server.Port != 42000 || got.Library.Dir != a.dir {
		t.Errorf("settings: autoplay %v port %d dir %q", got.Playback.AutoPlayNext, got.Server.Port, got.Library.Dir)
	}
	// The rule A had is the one here; B is added, its downloaded item with it.
	if n := b.count(t, `SELECT COUNT(*) FROM autodownload_rules`); n != 3 {
		t.Errorf("rules: %d", n)
	}
	if n := b.count(t, `SELECT COUNT(*) FROM autodownload_items i JOIN autodownload_rules r ON r.id = i.rule_id WHERE r.data = '{"title":"B"}'`); n != 1 {
		t.Errorf("the item didn't follow its rule: %d", n)
	}
	if n := b.count(t, `SELECT COUNT(*) FROM local_files WHERE path = '/a/x.mkv' AND locked = 1 AND media_id = 9`); n != 1 {
		t.Error("match made by hand not restored")
	}
	if n := b.count(t, `SELECT COUNT(*) FROM local_files WHERE path = '/a/w.mkv' AND locked = 1 AND ignored = 0 AND media_id = 42`); n != 1 {
		t.Error("a decision made here since the backup was undone")
	}
	if ok, _ := b.d.GetKV("letterboxd:token", new(string)); ok {
		t.Error("login restored from a backup without logins")
	}
	var hidden map[string]map[string]int64
	_, _ = b.d.GetKV(hiddenKey, &hidden)
	if hidden["5"]["episode"] != 3 {
		t.Errorf("continue watching put away: %v", hidden)
	}
	// What was there before is saved, as a backup.
	if sum.SavedTo == "" {
		t.Fatal("not saved")
	}
	// Again: nothing new.
	again, err := Restore(b.d, b.hist, b.store, read, filepath.Join(b.dir, "backups"), "1.0.140", "Laptop")
	if err != nil {
		t.Fatal(err)
	}
	if again.History != 0 || again.List != 0 || again.Rules != 0 || again.Extensions != 0 {
		t.Errorf("restored twice: %+v", again)
	}
	if n := b.count(t, `SELECT COUNT(*) FROM autodownload_rules`); n != 3 {
		t.Errorf("rules doubled: %d", n)
	}
}

// With logins: the AniList login and the passwords come along (the login
// only where there's none).
func TestBackupWithLogins(t *testing.T) {
	a := newKumoBox(t)
	cfg := a.store.Get()
	cfg.Qbittorrent.Password = "secret"
	cfg.TMDB.APIKey = "0123456789abcdef0123456789abcdef"
	_, _ = a.store.Save(cfg)
	_ = a.d.SetKV("letterboxd:token", "tok")
	f, err := Make(a.d, a.store.Get(), true, "1.0.140", "PC")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(f)
	read, err := Read(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	b := newKumoBox(t)
	sum, err := Restore(b.d, b.hist, b.store, read, filepath.Join(b.dir, "backups"), "1.0.140", "Laptop")
	if err != nil {
		t.Fatal(err)
	}
	var tok string
	if ok, _ := b.d.GetKV("letterboxd:token", &tok); !ok || tok != "tok" || !sum.Logins {
		t.Errorf("login: %q %+v", tok, sum)
	}
	if b.store.Get().Qbittorrent.Password != "secret" || b.store.Get().TMDB.APIKey != "0123456789abcdef0123456789abcdef" {
		t.Error("password or TMDB key not restored")
	}
}

func TestReadRejects(t *testing.T) {
	for _, in := range []string{"", "{}", "not json", `{"kumobox": 99, "version": "9.9.9"}`} {
		if _, err := Read(strings.NewReader(in)); err == nil {
			t.Errorf("read %q", in)
		}
	}
}

// Only the newest undo copies stay (each is a whole backup).
func TestPruneUndo(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{"2026-01-01-100000", "2026-03-01-100000", "2026-02-01-100000", "2026-04-01-100000", "2026-05-01-100000"} {
		if err := os.WriteFile(filepath.Join(dir, "before-restore-"+d+".json"), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(filepath.Join(dir, "mine.json"), []byte("{}"), 0o600)
	pruneUndo(dir, 3)
	left, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	var names []string
	for _, p := range left {
		names = append(names, filepath.Base(p))
	}
	if strings.Join(names, ",") != "before-restore-2026-03-01-100000.json,before-restore-2026-04-01-100000.json,before-restore-2026-05-01-100000.json,mine.json" {
		t.Errorf("left: %v", names)
	}
}

// The diary comes along: each viewing once, however often it's restored,
// next to what was logged here.
func TestBackupDiary(t *testing.T) {
	a := newKumoBox(t)
	a.exec(t, `INSERT INTO diary(media_id, watched_date, rating, review, created_at) VALUES(603, '2026-10-01', 9, 'Again', 100), (155, '2026-10-02', 10, '', 200)`)
	f, err := Make(a.d, a.store.Get(), false, "2.0.0", "PC")
	if err != nil {
		t.Fatal(err)
	}
	b := newKumoBox(t)
	b.exec(t, `INSERT INTO diary(media_id, watched_date, created_at) VALUES(27205, '2026-09-30', 50)`)
	for i := 0; i < 2; i++ {
		sum, err := Restore(b.d, b.hist, b.store, f, filepath.Join(b.dir, "backups"), "2.0.0", "Laptop")
		if err != nil {
			t.Fatal(err)
		}
		if want := 2 - 2*i; sum.Diary != want {
			t.Errorf("restore %d: %d diary entries added, want %d", i+1, sum.Diary, want)
		}
	}
	var n int
	if err := b.d.QueryRow(`SELECT COUNT(*) FROM diary`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("diary rows: %d, %v", n, err)
	}
	var review string
	if err := b.d.QueryRow(`SELECT review FROM diary WHERE media_id = 603`).Scan(&review); err != nil || review != "Again" {
		t.Fatalf("the review: %q, %v", review, err)
	}
}
