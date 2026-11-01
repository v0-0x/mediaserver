package library

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/v0-0x/mediaserver/movies/internal/db"
	"github.com/v0-0x/mediaserver/movies/internal/release"
	"github.com/v0-0x/mediaserver/movies/internal/util"
)

// LocalFile is a video file found in one of the library directories.
type LocalFile struct {
	Path    string `json:"path"`
	Dir     string `json:"dir"`
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"modTime"`
	Parsed  Parsed `json:"parsed"`
	MediaID int    `json:"mediaId"`
	// Episode is the key of the episode the file holds (see catalog: 1 for
	// a movie, S02E05 is 2005), 0 when unknown and for extras. EpisodeEnd
	// is the key of the last one when it holds several (S01E01E02: Episode
	// 1001, EpisodeEnd 1002), 0 otherwise. (It is kept in the
	// aired_episode column, which Kumo used for the number in the name.)
	Episode    int     `json:"episode"`
	EpisodeEnd int     `json:"episodeEnd,omitempty"`
	Kind       string  `json:"kind"` // main | special | extra | part (see KindMain...)
	Locked     bool    `json:"locked"`
	Ignored    bool    `json:"ignored"`
	MatchScore float64 `json:"matchScore"`
	// Host: another KumoBox on the network shares this file (see package
	// share); its path is kumobox://<host>/<path there>. Never stored.
	Host     string `json:"host,omitempty"`
	HostName string `json:"hostName,omitempty"`
}

type Store struct{ db *db.DB }

func NewStore(d *db.DB) *Store { return &Store{db: d} }

const fileCols = `path, dir, name, size, mod_time, parsed, media_id, episode, aired_episode, kind, locked, ignored, match_score`

func scanFile(row interface{ Scan(...any) error }) (*LocalFile, error) {
	var f LocalFile
	var parsed string
	var locked, ignored int
	if err := row.Scan(&f.Path, &f.Dir, &f.Name, &f.Size, &f.ModTime, &parsed, &f.MediaID, &f.Episode, &f.EpisodeEnd, &f.Kind, &locked, &ignored, &f.MatchScore); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(parsed), &f.Parsed)
	f.Kind = normalizedKind(f.Kind)
	if !sameSeasonAfter(f.Episode, f.EpisodeEnd) {
		f.EpisodeEnd = 0 // the number Kumo kept there
	}
	f.Locked = locked == 1
	f.Ignored = ignored == 1
	return &f, nil
}

func (s *Store) query(where string, args ...any) ([]*LocalFile, error) {
	rows, err := s.db.Query(`SELECT `+fileCols+` FROM local_files `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*LocalFile
	for rows.Next() {
		f, err := scanFile(rows)
		if err == nil {
			out = append(out, f)
		}
	}
	return out, rows.Err()
}

func (s *Store) All() ([]*LocalFile, error) { return s.query(`ORDER BY path`) }

func (s *Store) ByMedia(mediaID int) ([]*LocalFile, error) {
	files, err := s.query(`WHERE media_id = ? AND ignored = 0`, mediaID)
	sortFiles(files)
	return files, err
}

func (s *Store) Get(path string) (*LocalFile, error) {
	return scanFile(s.db.QueryRow(`SELECT `+fileCols+` FROM local_files WHERE path = ?`, path))
}

func (s *Store) Unmatched() ([]*LocalFile, error) {
	return s.query(`WHERE media_id = 0 AND ignored = 0 ORDER BY path`)
}

func (s *Store) Ignored() ([]*LocalFile, error) {
	return s.query(`WHERE ignored = 1 ORDER BY path`)
}

// MediaIDs returns every media id that has at least one file.
func (s *Store) MediaIDs() (map[int]int, error) {
	rows, err := s.db.Query(`SELECT media_id, COUNT(*) FROM local_files WHERE media_id > 0 AND ignored = 0 GROUP BY media_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]int{}
	for rows.Next() {
		var id, n int
		if rows.Scan(&id, &n) == nil {
			out[id] = n
		}
	}
	return out, nil
}

func upsertFile(tx *sql.Tx, f *LocalFile) error {
	parsed, _ := json.Marshal(f.Parsed)
	_, err := tx.Exec(`INSERT INTO local_files(`+fileCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(path) DO UPDATE SET dir=excluded.dir, name=excluded.name, size=excluded.size, mod_time=excluded.mod_time,
			parsed=excluded.parsed, media_id=excluded.media_id, episode=excluded.episode, aired_episode=excluded.aired_episode,
			kind=excluded.kind, locked=excluded.locked, ignored=excluded.ignored, match_score=excluded.match_score`,
		f.Path, f.Dir, f.Name, f.Size, f.ModTime, string(parsed), f.MediaID, f.Episode, f.EpisodeEnd, f.Kind, b2i(f.Locked), b2i(f.Ignored), f.MatchScore)
	return err
}

func (s *Store) Save(files ...*LocalFile) error {
	return s.db.Tx(func(tx *sql.Tx) error {
		for _, f := range files {
			if err := upsertFile(tx, f); err != nil {
				return err
			}
		}
		return nil
	})
}

// SaveScanned stores scan results without clobbering changes made while
// the scan was running (manual matches, ignores, downloads registering
// files): an existing row is only updated if it still matches the snapshot
// the scan started from, and new rows never replace rows added meanwhile.
func (s *Store) SaveScanned(files []*LocalFile, snapshot map[string]*LocalFile) error {
	return s.db.Tx(func(tx *sql.Tx) error {
		for _, f := range files {
			parsed, _ := json.Marshal(f.Parsed)
			old := snapshot[f.Path]
			if old == nil {
				if _, err := tx.Exec(`INSERT INTO local_files(`+fileCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(path) DO NOTHING`,
					f.Path, f.Dir, f.Name, f.Size, f.ModTime, string(parsed), f.MediaID, f.Episode, f.EpisodeEnd, f.Kind, b2i(f.Locked), b2i(f.Ignored), f.MatchScore); err != nil {
					return err
				}
				continue
			}
			if _, err := tx.Exec(`UPDATE local_files SET dir=?, name=?, size=?, mod_time=?, parsed=?, media_id=?, episode=?, aired_episode=?, kind=?, locked=?, ignored=?, match_score=?
				WHERE path=? AND media_id=? AND episode=? AND kind=? AND locked=? AND ignored=?`,
				f.Dir, f.Name, f.Size, f.ModTime, string(parsed), f.MediaID, f.Episode, f.EpisodeEnd, f.Kind, b2i(f.Locked), b2i(f.Ignored), f.MatchScore,
				f.Path, old.MediaID, old.Episode, old.Kind, b2i(old.Locked), b2i(old.Ignored)); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) Delete(paths ...string) error {
	return s.db.Tx(func(tx *sql.Tx) error {
		for _, p := range paths {
			if _, err := tx.Exec(`DELETE FROM local_files WHERE path = ?`, p); err != nil {
				return err
			}
		}
		return nil
	})
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// sameSeasonAfter reports whether end is a later episode of the season of
// key: a valid EpisodeEnd.
func sameSeasonAfter(key, end int) bool {
	return key > 0 && end > key && end/1000 == key/1000
}

// Keys are the episode keys the file holds: its episode, and the ones up
// to EpisodeEnd for a multi-episode file. None for an extra.
func (f *LocalFile) Keys() []int {
	if f.Episode <= 0 || f.Kind == KindExtra {
		return nil
	}
	if !sameSeasonAfter(f.Episode, f.EpisodeEnd) {
		return []int{f.Episode}
	}
	out := make([]int, 0, f.EpisodeEnd-f.Episode+1)
	for k := f.Episode; k <= f.EpisodeEnd; k++ {
		out = append(out, k)
	}
	return out
}

// Covers reports whether the file holds the episode key.
func (f *LocalFile) Covers(key int) bool {
	if key <= 0 || f.Kind == KindExtra {
		return false
	}
	return f.Episode == key || sameSeasonAfter(f.Episode, f.EpisodeEnd) && key > f.Episode && key <= f.EpisodeEnd
}

var kindRank = map[string]int{KindMain: 0, KindPart: 1, KindSpecial: 2, KindExtra: 3}

// sortFiles orders by kind (episodes and movies, later parts, specials,
// extras), then episode, then part, then name.
func sortFiles(files []*LocalFile) {
	sort.SliceStable(files, func(i, j int) bool {
		a, b := files[i], files[j]
		if kindRank[a.Kind] != kindRank[b.Kind] {
			return kindRank[a.Kind] < kindRank[b.Kind]
		}
		if a.Episode != b.Episode {
			return a.Episode < b.Episode
		}
		if a.Parsed.Part != b.Parsed.Part {
			return a.Parsed.Part < b.Parsed.Part
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
}

// UnmatchedGroup groups unmatched files for the matching UI: the files of
// one movie's or show's folder (its season and extras folders included),
// or of one title when they're loose in a folder.
type UnmatchedGroup struct {
	Dir   string       `json:"dir"`
	Title string       `json:"title"`
	Files []*LocalFile `json:"files"`
}

func GroupUnmatched(files []*LocalFile) []UnmatchedGroup {
	byKey := map[string]*UnmatchedGroup{}
	var order []string
	for _, f := range files {
		dir, key := f.Parsed.Folder, "folder:"+f.Parsed.Folder
		if dir == "" {
			dir, key = f.Dir, "title:"+f.Dir+"|"+release.Normalize(f.Parsed.Title)+"|"+strconv.Itoa(f.Parsed.Year)
		}
		g, ok := byKey[key]
		if !ok {
			g = &UnmatchedGroup{Dir: dir, Title: util.FirstNonEmpty(f.Parsed.Title, f.Parsed.FolderTitle, filepath.Base(dir))}
			byKey[key] = g
			order = append(order, key)
		}
		g.Files = append(g.Files, f)
	}
	out := make([]UnmatchedGroup, 0, len(order))
	for _, k := range order {
		sortFiles(byKey[k].Files)
		out = append(out, *byKey[k])
	}
	return out
}
