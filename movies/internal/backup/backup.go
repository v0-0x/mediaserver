// Package backup saves the user's own data in KumoBox as one JSON file, and
// puts it back, on this computer or another: the settings, the watch
// history, KumoBox's own list and diary, the audio and subtitle choices, the
// matches made by hand (and files ignored), the auto downloader's rules,
// extensions and their data, the Stremio addons. Not caches, nor what
// belongs to this computer (its identity on the network, the devices it
// shares with). The logins (the TMDB key, the Letterboxd sign-in, the torrent
// clients' and the network passwords) only when asked.
//
// A restore merges: what's newer here stays (positions, list entries), what's
// here and not in the backup too.
package backup

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/config"
	"github.com/v0-0x/mediaserver/movies/internal/db"
	"github.com/v0-0x/mediaserver/movies/internal/history"
	"github.com/v0-0x/mediaserver/movies/internal/util"
)

// Format is the backup file's version.
const Format = 1

// MaxSize is the biggest backup read (extensions make most of it).
const MaxSize = 256 << 20

type File struct {
	KumoBox   int    `json:"kumobox"` // the format (its key from before the rename: older backups have it)
	Version   string `json:"version"` // KumoBox's, that made it
	CreatedAt int64  `json:"createdAt"`
	Computer  string `json:"computer"`
	// Logins: it holds the TMDB key, the Letterboxd sign-in and the passwords.
	Logins   bool                        `json:"logins"`
	Settings json.RawMessage             `json:"settings"`
	KV       map[string]json.RawMessage  `json:"kv"`
	History  []history.Entry             `json:"history"`
	Tables   map[string][]map[string]any `json:"tables"`
}

// What a backup holds, or a restore put back.
type Summary struct {
	Settings   bool `json:"settings"`
	Logins     bool `json:"logins"`
	History    int  `json:"history"`
	List       int  `json:"list"`
	Tracks     int  `json:"tracks"`
	Matches    int  `json:"matches"`
	Rules      int  `json:"rules"`
	Extensions int  `json:"extensions"`
	Diary      int  `json:"diary"`
	// Restores only: where the data from before it went.
	SavedTo string `json:"savedTo,omitempty"`
}

// The tables kept, and how a restore puts their rows back: "newer" (the row
// changed last wins, by updated_at), "keep" (one here stays), "decision" (a
// file's match or ignoring, from the backup), "add" (rows with no key of
// their own, like diary entries: added unless one with the same key values
// is here).
var tables = []struct {
	name, where string
	key         []string
	merge       string
}{
	{"track_prefs", "", []string{"media_id"}, "newer"},
	{"local_list", "", []string{"media_id"}, "newer"},
	{"diary", "", []string{"media_id", "watched_date", "created_at"}, "add"},
	{"diary_deleted", "", []string{"media_id", "watched_date", "letterboxd_id"}, "add"},
	{"stremio_addons", "", []string{"transport_url"}, "keep"},
	{"stream_mappings", "", []string{"provider", "media_id", "mode"}, "keep"},
	{"extensions", "", []string{"id"}, "keep"},
	{"extension_storage", "", []string{"ext_id", "key"}, "keep"},
	{"local_files", "locked = 1 OR ignored = 1", []string{"path"}, "decision"},
	{"autodownload_rules", "", []string{"id"}, "rules"},
	{"autodownload_items", "", []string{"key"}, "items"},
}

// KV keys kept: the titles put away from Continue watching, the
// permissions given to extensions, the Letterboxd member connected; the
// Letterboxd sign-in (API access) with the logins.
const (
	hiddenKey = "continue-hidden"
	grantKey  = "ext_grant:"
	viewerKey = "letterboxd_viewer"
	tokenKey  = "letterboxd:token"
)

// Make makes a backup of d.
func Make(d *db.DB, settings config.Settings, logins bool, version, computer string) (*File, error) {
	if !logins {
		settings.Server.Password = ""
		settings.Qbittorrent.Password = ""
		settings.Transmission.Password = ""
		settings.TMDB.APIKey = ""
		settings.Letterboxd.APIClientSecret = ""
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		return nil, err
	}
	f := &File{KumoBox: Format, Version: version, CreatedAt: time.Now().Unix(), Computer: computer, Logins: logins, Settings: raw, KV: map[string]json.RawMessage{}, Tables: map[string][]map[string]any{}}
	rows, err := d.Query(`SELECT key, value FROM kv`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			rows.Close()
			return nil, err
		}
		if k == hiddenKey || strings.HasPrefix(k, grantKey) || k == viewerKey || logins && k == tokenKey {
			f.KV[k] = json.RawMessage(v)
		}
	}
	rows.Close()
	if f.History, err = historyRows(d); err != nil {
		return nil, err
	}
	for _, t := range tables {
		if !exists(d, t.name) {
			continue // made only once in use (stremio_addons)
		}
		q := `SELECT * FROM ` + t.name
		if t.where != "" {
			q += ` WHERE ` + t.where
		}
		list, err := dump(d, q)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", t.name, err)
		}
		f.Tables[t.name] = list
	}
	return f, nil
}

func historyRows(d *db.DB) ([]history.Entry, error) {
	rows, err := d.Query(`SELECT media_id, episode, position, duration, source, updated_at FROM watch_history`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []history.Entry{}
	for rows.Next() {
		var e history.Entry
		if err := rows.Scan(&e.MediaID, &e.Episode, &e.Position, &e.Duration, &e.Source, &e.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func dump(d *db.DB, q string) ([]map[string]any, error) {
	rows, err := d.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := map[string]any{}
		for i, c := range cols {
			if b, ok := vals[i].([]byte); ok {
				row[c] = string(b)
			} else if vals[i] != nil {
				row[c] = vals[i]
			}
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// Write saves a backup to a file only this user can read (it can hold the
// logins).
func Write(path string, f *File) error {
	data, err := json.Marshal(f)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// Read reads a backup file.
func Read(r io.Reader) (*File, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxSize {
		return nil, errors.New("that file is too big to be an KumoBox backup")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var f File
	if err := dec.Decode(&f); err != nil || f.KumoBox == 0 {
		return nil, errors.New("that isn't an KumoBox backup")
	}
	if f.KumoBox > Format {
		return nil, fmt.Errorf("that backup was made by a newer KumoBox (%s): update this one first", f.Version)
	}
	return &f, nil
}

// Summary is what a backup holds.
func (f *File) Summary() Summary {
	n := func(t string) int { return len(f.Tables[t]) }
	return Summary{
		Settings: len(f.Settings) > 0, Logins: f.Logins, History: len(f.History), List: n("local_list"), Tracks: n("track_prefs"),
		Matches: n("local_files"), Rules: n("autodownload_rules"), Extensions: n("extensions"), Diary: n("diary"),
	}
}

// Restore puts a backup into d (its settings through store), after saving
// what's there now as a backup in saveDir. What belongs to this computer
// stays: the network settings, library sharing, folders and programs the
// backup names that aren't here.
func Restore(d *db.DB, hist *history.Store, store *config.Store, f *File, saveDir, version, computer string) (Summary, error) {
	var sum Summary
	// What's here now, first: a restore can be undone.
	before, err := Make(d, store.Get(), true, version, computer)
	if err != nil {
		return sum, fmt.Errorf("couldn't save the data from before: %w", err)
	}
	if err := os.MkdirAll(saveDir, 0o700); err != nil {
		return sum, err
	}
	sum.SavedTo = filepath.Join(saveDir, "before-restore-"+time.Now().Format("2006-01-02-150405")+".json")
	if err := Write(sum.SavedTo, before); err != nil {
		return sum, fmt.Errorf("couldn't save the data from before: %w", err)
	}
	pruneUndo(saveDir, 3)

	merged, err := hist.MergeAll(f.History)
	if err != nil {
		return sum, err
	}
	sum.History = len(merged)
	err = d.Tx(func(tx *sql.Tx) error {
		for k, v := range f.KV {
			switch {
			case k == hiddenKey:
				if err := mergeHidden(tx, v); err != nil {
					return err
				}
			case strings.HasPrefix(k, grantKey), k == viewerKey, k == tokenKey:
				if _, err := tx.Exec(`INSERT OR IGNORE INTO kv(key, value) VALUES(?, ?)`, k, string(v)); err != nil {
					return err
				}
				sum.Logins = sum.Logins || k == tokenKey
			}
		}
		ruleIDs := map[int64]int64{}
		for _, t := range tables {
			if len(f.Tables[t.name]) == 0 {
				continue
			}
			cols, err := columns(tx, t.name)
			if err != nil {
				return err
			}
			for _, row := range f.Tables[t.name] {
				n, err := putRow(tx, t.name, t.key, t.merge, cols, row, ruleIDs)
				if err != nil {
					return fmt.Errorf("%s: %w", t.name, err)
				}
				switch t.name {
				case "local_list":
					sum.List += n
				case "track_prefs":
					sum.Tracks += n
				case "local_files":
					sum.Matches += n
				case "autodownload_rules":
					sum.Rules += n
				case "extensions":
					sum.Extensions += n
				case "diary":
					sum.Diary += n
				}
			}
		}
		return nil
	})
	if err != nil {
		return sum, err
	}
	if len(f.Settings) > 0 {
		next, err := config.Decode(f.Settings)
		if err != nil {
			return sum, fmt.Errorf("the backup's settings: %w", err)
		}
		if _, err := store.Save(keepLocal(next, store.Get())); err != nil {
			return sum, err
		}
		sum.Settings = true
	}
	return sum, nil
}

// keepLocal keeps in restored settings what belongs to this computer: how
// it's reached on the network, library sharing, the passwords a backup
// without logins doesn't have, and the folders and programs it names that
// aren't on this computer.
func keepLocal(next, cur config.Settings) config.Settings {
	next.Server = cur.Server
	next.Sharing = cur.Sharing
	if next.Qbittorrent.Password == "" {
		next.Qbittorrent.Password = cur.Qbittorrent.Password
	}
	if next.Transmission.Password == "" {
		next.Transmission.Password = cur.Transmission.Password
	}
	if next.TMDB.APIKey == "" {
		next.TMDB.APIKey = cur.TMDB.APIKey
	}
	if next.Letterboxd.APIClientSecret == "" {
		next.Letterboxd.APIClientSecret = cur.Letterboxd.APIClientSecret
	}
	if !isDir(next.Library.Dir) {
		next.Library.Dir = cur.Library.Dir
	}
	var dirs []string
	for _, d := range next.Library.ExtraDirs {
		if isDir(d) {
			dirs = append(dirs, d)
		}
	}
	if len(dirs) < len(next.Library.ExtraDirs) {
		dirs = append(dirs, cur.Library.ExtraDirs...)
	}
	next.Library.ExtraDirs = unique(dirs)
	if next.Downloads.Dir != "" && !isDir(next.Downloads.Dir) {
		next.Downloads.Dir = cur.Downloads.Dir
	}
	program := func(p *string, here string) {
		if *p == "" {
			return
		}
		if _, ok := util.LookPath(*p); !ok {
			*p = here
		}
	}
	program(&next.Mpv.Path, cur.Mpv.Path)
	program(&next.Transcode.FfmpegPath, cur.Transcode.FfmpegPath)
	program(&next.Transcode.FfprobePath, cur.Transcode.FfprobePath)
	program(&next.Qbittorrent.Executable, cur.Qbittorrent.Executable)
	program(&next.Transmission.Executable, cur.Transmission.Executable)
	return next
}

func isDir(p string) bool {
	if p == "" {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func unique(list []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range list {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// columns are a table's columns here: only those are written (a backup's
// names never make it into the SQL otherwise).
// exists reports whether d has the table.
func exists(d *db.DB, table string) bool {
	var n int
	return d.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&n) == nil && n > 0
}

func columns(tx *sql.Tx, table string) (map[string]bool, error) {
	rows, err := tx.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out[n] = true
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no table %s", table)
	}
	return out, rows.Err()
}

// putRow writes a backup's row as merge says; it reports 1 when the row was
// taken.
func putRow(tx *sql.Tx, table string, key []string, merge string, have map[string]bool, row map[string]any, ruleIDs map[int64]int64) (int, error) {
	var cols []string
	var args []any
	for c, v := range row {
		if !have[c] {
			continue
		}
		val, ok := sqlValue(v)
		if !ok {
			continue
		}
		cols = append(cols, c)
		args = append(args, val)
	}
	// In the same order each time (the map's isn't).
	sort.Sort(byName{cols, args})
	pos := map[string]int{}
	for i, c := range cols {
		pos[c] = i
	}
	for _, k := range key {
		if _, ok := pos[k]; !ok {
			return 0, nil // not a whole row: skipped
		}
	}

	switch merge {
	case "rules":
		// A rule the same as one here is that one; another is added, with
		// an id of its own here (the items it downloaded follow it).
		i, ok := pos["data"]
		id, okID := asInt(args[pos["id"]])
		if !ok || !okID {
			return 0, nil
		}
		var here int64
		err := tx.QueryRow(`SELECT id FROM autodownload_rules WHERE data = ?`, args[i]).Scan(&here)
		if err == nil {
			ruleIDs[id] = here
			return 0, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
		res, err := tx.Exec(`INSERT INTO autodownload_rules(data) VALUES(?)`, args[i])
		if err != nil {
			return 0, err
		}
		if ruleIDs[id], err = res.LastInsertId(); err != nil {
			return 0, err
		}
		return 1, nil
	case "items":
		i, ok := pos["rule_id"]
		old, okID := asInt(args[i])
		if !ok || !okID {
			return 0, nil
		}
		id, mapped := ruleIDs[old]
		if !mapped {
			return 0, nil
		}
		args[i] = id
	}

	if merge == "add" {
		// The row's id is this database's to give; one with the same key
		// values is the same row.
		if i, ok := pos["id"]; ok {
			cols = append(cols[:i:i], cols[i+1:]...)
			args = append(args[:i:i], args[i+1:]...)
		}
		var where []string
		var vals []any
		for _, k := range key {
			for i, c := range cols {
				if c == k {
					where, vals = append(where, k+" = ?"), append(vals, args[i])
				}
			}
		}
		var here int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE `+strings.Join(where, " AND "), vals...).Scan(&here); err != nil {
			return 0, err
		}
		if here > 0 {
			return 0, nil
		}
	}
	names := strings.Join(cols, ", ")
	marks := strings.TrimSuffix(strings.Repeat("?, ", len(cols)), ", ")
	q := `INSERT OR IGNORE INTO ` + table + `(` + names + `) VALUES(` + marks + `)`
	switch merge {
	case "newer", "decision":
		var set []string
		for _, c := range cols {
			if merge == "decision" && !decisionCols[c] || contains(key, c) {
				continue
			}
			set = append(set, c+" = excluded."+c)
		}
		if len(set) == 0 {
			break
		}
		q = `INSERT INTO ` + table + `(` + names + `) VALUES(` + marks + `) ON CONFLICT(` + strings.Join(key, ", ") + `) DO UPDATE SET ` + strings.Join(set, ", ")
		if merge == "decision" {
			// Only where nothing was decided here: a match set by hand, or a
			// file un-ignored, since the backup stays.
			q += ` WHERE ` + table + `.locked = 0 AND ` + table + `.ignored = 0`
		}
		if merge == "newer" {
			if _, ok := pos["updated_at"]; !ok {
				q = `INSERT OR IGNORE INTO ` + table + `(` + names + `) VALUES(` + marks + `)`
			} else {
				q += ` WHERE excluded.updated_at > ` + table + `.updated_at`
			}
		}
	}
	res, err := tx.Exec(q, args...)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(min(n, 1)), nil
}

// A file's columns a backup's decision about it sets (not its size or
// where it was scanned).
// pruneUndo keeps the newest keep copies saved before restores (their names
// sort by date): each is a whole backup.
func pruneUndo(dir string, keep int) {
	old, _ := filepath.Glob(filepath.Join(dir, "before-restore-*.json"))
	sort.Strings(old)
	for len(old) > keep {
		_ = os.Remove(old[0])
		old = old[1:]
	}
}

var decisionCols = map[string]bool{"media_id": true, "episode": true, "kind": true, "locked": true, "ignored": true, "match_score": true}

type byName struct {
	cols []string
	args []any
}

func (b byName) Len() int           { return len(b.cols) }
func (b byName) Less(i, j int) bool { return b.cols[i] < b.cols[j] }
func (b byName) Swap(i, j int) {
	b.cols[i], b.cols[j] = b.cols[j], b.cols[i]
	b.args[i], b.args[j] = b.args[j], b.args[i]
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// sqlValue turns a value read from a backup (json.Number, string, bool)
// into one for SQLite.
func sqlValue(v any) (any, bool) {
	switch x := v.(type) {
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i, true
		}
		f, err := x.Float64()
		return f, err == nil
	case string:
		return x, true
	case bool:
		if x {
			return int64(1), true
		}
		return int64(0), true
	case int64, float64:
		return x, true
	}
	return nil, false
}

func asInt(v any) (int64, bool) {
	switch x := v.(type) {
	case int64:
		return x, true
	case float64:
		return int64(x), x == float64(int64(x))
	}
	return 0, false
}

// mergeHidden merges the anime put away from Continue watching: for each,
// the one put away last.
func mergeHidden(tx *sql.Tx, raw json.RawMessage) error {
	type item struct {
		Episode  int   `json:"episode"`
		HiddenAt int64 `json:"hiddenAt"`
	}
	var theirs, ours map[string]item
	if err := json.Unmarshal(raw, &theirs); err != nil {
		return nil // not readable: left out
	}
	var cur string
	err := tx.QueryRow(`SELECT value FROM kv WHERE key = ?`, hiddenKey).Scan(&cur)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	ours = map[string]item{}
	if cur != "" {
		_ = json.Unmarshal([]byte(cur), &ours)
	}
	for id, it := range theirs {
		if mine, ok := ours[id]; !ok || it.HiddenAt > mine.HiddenAt {
			ours[id] = it
		}
	}
	out, err := json.Marshal(ours)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO kv(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, hiddenKey, string(out))
	return err
}
