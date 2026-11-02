package library

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
)

// The Local library page: the movies and shows with files, on this
// computer or on another Moviko sharing its library, each summed up.

// LibraryTitle is a movie or show of the Local library page.
type LibraryTitle struct {
	Media     *catalog.Media     `json:"media"`
	ListEntry *catalog.ListEntry `json:"listEntry"`
	// Files are its files: episodes (by key) or the movie, later parts,
	// specials, extras.
	Files []*LocalFile `json:"files"`
	// Episodes are the keys of the regular episodes with a file (each
	// episode of a multi-episode file), the movie's for a movie; Seasons
	// the same by season, and Ranges all of them written short:
	// "S01E01–E10, S02E01–E03" ("" for a movie).
	Episodes []int         `json:"episodes"`
	Seasons  []SeasonFiles `json:"seasons"`
	Ranges   string        `json:"ranges"`
	Specials int           `json:"specials"` // special files
	Extras   int           `json:"extras"`   // extras and later parts of a movie
	Size     int64         `json:"size"`     // all its files, in bytes
	// AddedAt is when its newest file was written (unix seconds).
	AddedAt       int64    `json:"addedAt"`
	Resolutions   []string `json:"resolutions"`
	ReleaseGroups []string `json:"releaseGroups"`
	// Dir is the folder that holds it: its movie's or show's folder, else
	// the one most of its files are in ("" for another Moviko's).
	Dir string `json:"dir"`
	// Unwatched is how many of its episodes with a file haven't been
	// watched (the movie: 1 or 0), Next the file of the first of them.
	Unwatched int        `json:"unwatched"`
	Next      *LocalFile `json:"next"`
}

// SeasonFiles are the episodes of one season that have a file.
type SeasonFiles struct {
	Season int `json:"season"`
	Count  int `json:"count"`
	// Total is how many episodes TMDB lists for the season, 0 if unknown.
	Total  int    `json:"total"`
	Ranges string `json:"ranges"`
}

// Titles lists the movies and shows with files: this computer's (host ""),
// or those another Moviko shares (its host id). Titles whose data can't
// be had (TMDB not answering, never fetched) are left out until it can.
func (s *Service) Titles(ctx context.Context, host string) ([]*LibraryTitle, error) {
	var files []*LocalFile
	if host == "" {
		all, err := s.Store.All()
		if err != nil {
			return nil, err
		}
		files = all
	} else {
		for _, f := range s.shared() {
			if f.Host == host {
				files = append(files, f)
			}
		}
	}
	byMedia := map[int][]*LocalFile{}
	for _, f := range files {
		if f.MediaID > 0 && !f.Ignored {
			byMedia[f.MediaID] = append(byMedia[f.MediaID], f)
		}
	}
	entries := map[int]*catalog.ListEntry{}
	medias := map[int]*catalog.Media{}
	if s.Platform != nil {
		if coll, err := s.Platform.Collection(ctx, "", false); err == nil {
			for _, e := range coll.Entries() {
				entries[e.MediaID] = e
				medias[e.MediaID] = e.Media
			}
		}
		var missing []int
		for id := range byMedia {
			if medias[id] == nil {
				missing = append(missing, id)
			}
		}
		if len(missing) > 0 {
			got, _ := s.Platform.MediaBatch(ctx, missing)
			for id, m := range got {
				medias[id] = m
			}
		}
	}
	out := []*LibraryTitle{}
	for id, fs := range byMedia {
		media := medias[id]
		if media == nil {
			continue
		}
		t := s.summarize(media, entries[id], fs)
		if host != "" {
			t.Dir = ""
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AddedAt != out[j].AddedAt {
			return out[i].AddedAt > out[j].AddedAt
		}
		return out[i].Media.ID < out[j].Media.ID
	})
	return out, nil
}

// summarize sums up a title's files.
func (s *Service) summarize(media *catalog.Media, entry *catalog.ListEntry, files []*LocalFile) *LibraryTitle {
	files = append([]*LocalFile(nil), files...)
	sortFiles(files)
	t := &LibraryTitle{Media: media, ListEntry: entry, Files: files, Resolutions: []string{}, ReleaseGroups: []string{}}
	folders, dirs := map[string]int{}, map[string]int{}
	for _, f := range files {
		t.Size += f.Size
		t.AddedAt = max(t.AddedAt, f.ModTime)
		switch f.Kind {
		case KindExtra, KindPart:
			t.Extras++
			continue
		case KindSpecial:
			t.Specials++
		}
		t.Resolutions = addUnique(t.Resolutions, f.Parsed.Resolution)
		t.ReleaseGroups = addUnique(t.ReleaseGroups, f.Parsed.ReleaseGroup)
		if f.Parsed.Folder != "" {
			folders[f.Parsed.Folder]++
		}
		if f.Dir != "" {
			dirs[f.Dir]++
		}
	}
	t.Dir = mostCommon(folders)
	if t.Dir == "" {
		t.Dir = mostCommon(dirs)
	}
	t.Episodes = downloadedKeys(media, files)
	if t.Episodes == nil {
		t.Episodes = []int{}
	}
	if media.IsShow() {
		t.Ranges = EpisodeRanges(t.Episodes)
		bySeason := map[int][]int{}
		var seasons []int
		for _, k := range t.Episodes {
			sn, _ := catalog.SplitKey(k)
			if bySeason[sn] == nil {
				seasons = append(seasons, sn)
			}
			bySeason[sn] = append(bySeason[sn], k)
		}
		for _, sn := range seasons {
			t.Seasons = append(t.Seasons, SeasonFiles{Season: sn, Count: len(bySeason[sn]), Total: media.SeasonEpisodeCount(sn), Ranges: EpisodeRanges(bySeason[sn])})
		}
	}
	if t.Seasons == nil {
		t.Seasons = []SeasonFiles{}
	}

	// Unwatched: after the list's progress (nothing for a title watched to
	// the end); for a title not on the list, not watched to the end here.
	var hist map[int]bool
	if entry == nil && s.History != nil {
		hist = map[int]bool{}
		for k, h := range s.History.ForMedia(media.ID) {
			hist[k] = h.Percent() >= finishedAt
		}
	}
	for _, k := range t.Episodes {
		watched := false
		switch {
		case entry != nil:
			watched = entry.Status == "COMPLETED" || k <= entry.Progress
		default:
			watched = hist[k]
		}
		if !watched {
			t.Unwatched++
			if t.Next == nil {
				t.Next = EpisodeFile(files, k)
			}
		}
	}
	return t
}

func addUnique(list []string, v string) []string {
	if v == "" {
		return list
	}
	for _, x := range list {
		if strings.EqualFold(x, v) {
			return list
		}
	}
	return append(list, v)
}

func mostCommon(n map[string]int) string {
	best := ""
	for k, c := range n {
		if best == "" || c > n[best] || c == n[best] && k < best {
			best = k
		}
	}
	return best
}

// EpisodeRanges writes episode keys short, as runs: S01E01, S01E02,
// S01E03, S01E05, S02E01, S02E02 → "S01E01–E03, E05, S02E01–E02".
func EpisodeRanges(keys []int) string {
	ks := append([]int(nil), keys...)
	sort.Ints(ks)
	ks = compactInts(ks)
	var out []string
	season := -1
	for i := 0; i < len(ks); {
		j := i
		for j+1 < len(ks) && ks[j+1] == ks[j]+1 {
			j++
		}
		sn, a := catalog.SplitKey(ks[i])
		_, b := catalog.SplitKey(ks[j])
		first := fmt.Sprintf("E%02d", a)
		if sn != season {
			first = catalog.FormatKey(ks[i])
		}
		if i == j {
			out = append(out, first)
		} else {
			out = append(out, fmt.Sprintf("%s–E%02d", first, b))
		}
		season = sn
		i = j + 1
	}
	return strings.Join(out, ", ")
}

func compactInts(s []int) []int {
	out := s[:0]
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}
