package library

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/db"
	"github.com/v0-0x/mediaserver/movies/internal/history"
	"github.com/v0-0x/mediaserver/movies/internal/util"
)

// Service builds the views the UI needs by combining the list, the local
// files, the episodes of TMDB's seasons and the watch history.
type Service struct {
	Store    *Store
	Scanner  *Scanner
	Platform *catalog.Platform
	History  *history.Store
	DB       *db.DB // items removed from "Continue watching"
	// Remote: the files other Moviko apps on the network share with this one
	// (package share), shown with the local ones; nil without sharing.
	Remote func() []*LocalFile

	hiddenMu sync.Mutex
}

// EpisodeView is an episode of the title page (a movie has one), or a
// file of it that isn't one (an extra, a later part of a movie). Number and
// Key are both the episode key (see catalog; the web UI reads Number),
// Season and Episode the season and the episode's number in it; 0 for a
// file without an episode.
type EpisodeView struct {
	Number   int            `json:"number"`
	Key      int            `json:"key"`
	Season   int            `json:"season"`
	Episode  int            `json:"episode"`
	Title    string         `json:"title"`
	Image    string         `json:"image"`
	Summary  string         `json:"summary"`
	AirDate  string         `json:"airDate"`
	Runtime  int            `json:"runtime"`
	Kind     string         `json:"kind"` // main | special | extra | part
	File     *LocalFile     `json:"file"`
	Watched  bool           `json:"watched"`
	Aired    bool           `json:"aired"`
	History  *history.Entry `json:"history"`
	ResumeAt float64        `json:"resumeAt"`
	HasFile  bool           `json:"hasFile"`
}

// Images is the artwork of a title page.
type Images struct {
	Banner    string `json:"banner"`
	Poster    string `json:"poster"`
	Fanart    string `json:"fanart"`
	Clearlogo string `json:"clearlogo"`
}

// Mappings are a title's ids on other sites.
type Mappings struct {
	TmdbID int    `json:"tmdbId"`
	ImdbID string `json:"imdbId,omitempty"`
	TvdbID int    `json:"tvdbId,omitempty"`
}

type EntryView struct {
	Media     *catalog.Media     `json:"media"`
	ListEntry *catalog.ListEntry `json:"listEntry"`
	// Episodes are the regular episodes (a movie's one), Specials season 0
	// and the show's files without a known episode, Others the extras and
	// the later parts of a movie.
	Episodes    []*EpisodeView `json:"episodes"`
	Specials    []*EpisodeView `json:"specials"`
	Others      []*EpisodeView `json:"others"`
	NextEpisode *EpisodeView   `json:"nextEpisode"`
	LocalCount  int            `json:"localCount"`
	// SharedCount: files of it other Moviko apps share with this one.
	SharedCount  int      `json:"sharedCount"`
	Images       Images   `json:"images"`
	Mappings     Mappings `json:"mappings"`
	MetadataNote string   `json:"metadataNote,omitempty"`
}

// Entry returns everything the title page needs.
func (s *Service) Entry(ctx context.Context, mediaID int, refresh bool) (*EntryView, error) {
	media, err := s.Platform.Media(ctx, mediaID, refresh)
	if err != nil {
		return nil, err
	}
	coll, _ := s.Platform.Collection(ctx, "", false)
	var entry *catalog.ListEntry
	if coll != nil {
		entry = coll.Find(mediaID)
	}
	files, _ := s.Store.ByMedia(mediaID)
	localCount := len(files)
	files = append(files, s.sharedFor(mediaID, files)...)

	v := &EntryView{
		Media: media, ListEntry: entry, LocalCount: localCount, SharedCount: len(files) - localCount,
		Images:   Images{Fanart: media.BannerImage, Poster: media.CoverImage.ExtraLarge},
		Mappings: Mappings{TmdbID: media.TMDBID, ImdbID: media.IMDbID, TvdbID: media.TVDBID},
	}
	progress := 0
	if entry != nil {
		progress = entry.Progress
	}
	details, err := s.SeasonEpisodes(ctx, media, SeasonsWanted(media, files, progress))
	if err != nil {
		v.MetadataNote = "Episode details unavailable: " + err.Error()
	}
	s.fillEpisodes(v, media, entry, files, details)
	return v, nil
}

// SeasonsWanted are the seasons of a show whose episodes a title page
// needs: those with files (specials included), and the ones of the last
// episode watched and of the next one. The others are fetched when looked
// at (GET /api/media/{id}/season/{n}): a show of 30 seasons doesn't cost
// 30 requests.
func SeasonsWanted(media *catalog.Media, files []*LocalFile, progress int) []int {
	if !media.IsShow() {
		return nil
	}
	want := map[int]bool{}
	for _, f := range files {
		for _, k := range f.Keys() {
			s, _ := catalog.SplitKey(k)
			want[s] = true
		}
	}
	if progress >= 1000 {
		s, _ := catalog.SplitKey(progress)
		want[s] = true
	}
	if next := media.NextEpisode(progress); next > 0 {
		s, _ := catalog.SplitKey(next)
		want[s] = true
	}
	known := map[int]bool{}
	for _, s := range media.Seasons {
		known[s.Number] = true
	}
	var out []int
	for s := range want {
		if len(known) == 0 || known[s] {
			out = append(out, s)
		}
	}
	sort.Ints(out)
	return out
}

// SeasonEpisodes fetches the episodes of some seasons of a show (the
// Platform keeps them for a while), by key.
func (s *Service) SeasonEpisodes(ctx context.Context, media *catalog.Media, seasons []int) (map[int]*catalog.Episode, error) {
	out := map[int]*catalog.Episode{}
	if !media.IsShow() || s.Platform == nil || len(seasons) == 0 {
		return out, nil
	}
	var (
		mu       sync.Mutex
		firstErr error
		wg       sync.WaitGroup
		next     = make(chan int)
	)
	for range min(4, len(seasons)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := range next {
				sd, err := s.Platform.Season(ctx, media.ID, n)
				mu.Lock()
				if err != nil {
					if firstErr == nil {
						firstErr = err
					}
				} else {
					for _, e := range sd.Episodes {
						out[e.Key] = e
					}
				}
				mu.Unlock()
			}
		}()
	}
	for _, n := range seasons {
		next <- n
	}
	close(next)
	wg.Wait()
	return out, firstErr
}

// finishedAt is how far into an episode it counts as watched.
const finishedAt = 0.85

// fillEpisodes lists the episodes of the title page: the regular ones (the
// movie, or the show's from its seasons, plus any its files have beyond
// them), the specials (season 0, and the show's files whose episode isn't
// known) and the extras.
func (s *Service) fillEpisodes(v *EntryView, media *catalog.Media, entry *catalog.ListEntry, files []*LocalFile, details map[int]*catalog.Episode) {
	progress := 0
	if entry != nil {
		progress = entry.Progress
	}
	hist := s.History.ForMedia(media.ID)
	latest := media.LatestAired()
	today := time.Now().UTC().Format(time.DateOnly)
	fallbackImage := util.FirstNonEmpty(media.BannerImage, media.CoverImage.ExtraLarge)

	// The files of the episodes (a multi-episode file holds several).
	episodeFiles := map[int]*LocalFile{}
	for _, f := range files {
		switch f.Kind {
		case KindExtra:
			v.Others = append(v.Others, &EpisodeView{Title: ExtraTitle(f), Kind: KindExtra, File: f, HasFile: true, Aired: true, Image: fallbackImage})
			continue
		case KindPart:
			v.Others = append(v.Others, &EpisodeView{Number: f.Episode, Key: f.Episode, Title: partTitle(f), Kind: KindPart, File: f, HasFile: true, Aired: true, Image: fallbackImage})
			continue
		}
		keys := f.Keys()
		if len(keys) == 0 {
			// A show's file whose episode isn't known.
			sp := &EpisodeView{Title: util.FirstNonEmpty(f.Parsed.EpisodeTitle, f.Name), Kind: KindSpecial, File: f, HasFile: true, Aired: true, Image: fallbackImage}
			v.Specials = append(v.Specials, sp)
			continue
		}
		for _, k := range keys {
			if old, ok := episodeFiles[k]; !ok || betterFile(f, old) {
				episodeFiles[k] = f
			}
		}
	}

	watched := func(key int) bool {
		if entry != nil && key >= catalog.MovieEpisode && (key >= 1000 || !media.IsShow()) {
			return entry.Status == "COMPLETED" || key <= progress
		}
		h := hist[key]
		return h != nil && h.Percent() >= finishedAt
	}
	view := func(key int, kind string) *EpisodeView {
		season, number := catalog.SplitKey(key)
		ep := &EpisodeView{Number: key, Key: key, Season: season, Episode: number, Kind: kind, Image: fallbackImage}
		if !media.IsShow() {
			ep.Season, ep.Episode = 0, 1
			ep.Title = media.PreferredTitle()
			ep.Aired = media.Status != "NOT_YET_RELEASED"
		} else if d := details[key]; d != nil {
			ep.Title, ep.Summary, ep.AirDate, ep.Runtime = d.Title, d.Overview, d.AirDate, d.Runtime
			if d.Still != "" {
				ep.Image = d.Still
			}
			ep.Aired = d.AirDate != "" && d.AirDate <= today
		} else {
			ep.Aired = latest == 0 && media.Status != "NOT_YET_RELEASED" || key <= latest
		}
		if ep.Runtime == 0 && media.Duration != nil {
			ep.Runtime = *media.Duration
		}
		if h, ok := hist[key]; ok {
			ep.History = h
			ep.ResumeAt = s.History.ResumePosition(media.ID, key)
		}
		ep.Watched = watched(key)
		if f, ok := episodeFiles[key]; ok {
			ep.File, ep.HasFile, ep.Aired = f, true, true
			if ep.Title == "" && media.IsShow() {
				ep.Title = f.Parsed.EpisodeTitle
			}
		}
		return ep
	}

	keys := media.EpisodeKeys()
	known := map[int]bool{}
	for _, k := range keys {
		known[k] = true
	}
	for k := range episodeFiles {
		if !known[k] && (k >= 1000 || !media.IsShow()) {
			keys = append(keys, k)
			known[k] = true
		}
	}
	sort.Ints(keys)
	for _, k := range keys {
		v.Episodes = append(v.Episodes, view(k, KindMain))
	}

	// Specials: TMDB's season 0 (as far as it's been fetched), with their
	// files, then the special files it doesn't list.
	specials := map[int]bool{}
	for k := range details {
		if k < 1000 {
			specials[k] = true
		}
	}
	if media.IsShow() {
		for k := range episodeFiles {
			if k < 1000 {
				specials[k] = true
			}
		}
	}
	specialKeys := make([]int, 0, len(specials))
	for k := range specials {
		specialKeys = append(specialKeys, k)
	}
	sort.Ints(specialKeys)
	listed := v.Specials
	v.Specials = nil
	for _, k := range specialKeys {
		ep := view(k, KindSpecial)
		if ep.Title == "" && ep.File != nil {
			ep.Title = util.FirstNonEmpty(ep.File.Parsed.EpisodeTitle, ep.File.Name)
		}
		v.Specials = append(v.Specials, ep)
	}
	v.Specials = append(v.Specials, listed...)

	// Next episode to watch: the first one after progress; the first one
	// again for a title watched to the end (a rewatch) or not on the list.
	next := media.NextEpisode(progress)
	for _, ep := range v.Episodes {
		if ep.Key == next {
			v.NextEpisode = ep
			break
		}
	}
	if v.NextEpisode == nil && (entry == nil || entry.Status == "COMPLETED") && len(v.Episodes) > 0 {
		v.NextEpisode = v.Episodes[0]
	}
}

// ExtraTitle names an extra: its own title, else its type ("Trailer").
func ExtraTitle(f *LocalFile) string {
	if t := f.Parsed.EpisodeTitle; t != "" {
		return t
	}
	if x := f.Parsed.Extra; x != "" {
		return strings.ToUpper(x[:1]) + x[1:]
	}
	return f.Name
}

func partTitle(f *LocalFile) string {
	if f.Parsed.Part > 0 {
		return "Part " + strconv.Itoa(f.Parsed.Part)
	}
	return f.Name
}

// EpisodesView is the episodes of a title page.
type EpisodesView struct {
	Episodes    []*EpisodeView `json:"episodes"`
	Specials    []*EpisodeView `json:"specials"`
	Others      []*EpisodeView `json:"others"`
	NextEpisode *EpisodeView   `json:"nextEpisode"`
}

// Episodes returns a title's episodes, with their files and history.
func (s *Service) Episodes(ctx context.Context, mediaID int) (*EpisodesView, error) {
	v, err := s.Entry(ctx, mediaID, false)
	if err != nil {
		return nil, err
	}
	return &EpisodesView{Episodes: v.Episodes, Specials: v.Specials, Others: v.Others, NextEpisode: v.NextEpisode}, nil
}

// shared are the files other Moviko apps share with this one.
func (s *Service) shared() []*LocalFile {
	if s.Remote == nil {
		return nil
	}
	return s.Remote()
}

// sharedFor are a title's shared files, but its specials and extras this
// computer has too (same file name).
func (s *Service) sharedFor(mediaID int, local []*LocalFile) []*LocalFile {
	names := map[string]bool{}
	for _, f := range local {
		names[f.Name] = true
	}
	var out []*LocalFile
	for _, f := range s.shared() {
		if f.MediaID == mediaID && !f.Ignored && (f.Kind == KindMain || !names[f.Name]) {
			out = append(out, f)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Home / library collection view

type CollectionItem struct {
	Media      *catalog.Media     `json:"media"`
	ListEntry  *catalog.ListEntry `json:"listEntry"`
	LocalFiles int                `json:"localFiles"`
	// Downloaded are the keys of the episodes with a file.
	Downloaded []int `json:"downloaded"`
	// NextEpisode is the key of the episode to watch next (0: none).
	NextEpisode int   `json:"nextEpisode"`
	NextHasFile bool  `json:"nextHasFile"`
	LastWatched int64 `json:"lastWatched"`
}

type CollectionList struct {
	Status string            `json:"status"`
	Name   string            `json:"name"`
	Items  []*CollectionItem `json:"items"`
}

// ContinueItem is a title of "Continue watching". Episode is the key of the
// episode to watch, Index its place among the regular episodes ("13 / 24").
type ContinueItem struct {
	Media       *catalog.Media `json:"media"`
	Episode     int            `json:"episode"`
	Index       int            `json:"index"`
	Total       int            `json:"total"`
	Title       string         `json:"title"`
	Image       string         `json:"image"`
	Runtime     int            `json:"runtime"`
	HasFile     bool           `json:"hasFile"`
	FilePath    string         `json:"filePath,omitempty"`
	ResumeAt    float64        `json:"resumeAt"`
	Duration    float64        `json:"duration"`
	LastWatched int64          `json:"lastWatched"`
	Source      string         `json:"source"`
}

type CollectionView struct {
	Lists            []*CollectionList `json:"lists"`
	ContinueWatching []*ContinueItem   `json:"continueWatching"`
	UnmatchedCount   int               `json:"unmatchedCount"`
	IgnoredCount     int               `json:"ignoredCount"`
	LocalOnly        []*CollectionItem `json:"localOnly"` // matched files not in the list
	Genres           []string          `json:"genres"`
	// Incomplete: movies and shows in the library left out for now, TMDB
	// not having said what they are (no network yet, its limit on
	// requests); the app asks again in a moment.
	Incomplete int `json:"incomplete,omitempty"`
}

var statusNames = map[string]string{
	"CURRENT": "Watching", "REPEATING": "Rewatching", "PLANNING": "Watchlist",
	"PAUSED": "Paused", "COMPLETED": "Watched", "DROPPED": "Dropped",
}

// downloadedKeys are the keys of the episodes (the movie) files hold.
func downloadedKeys(media *catalog.Media, files []*LocalFile) []int {
	seen := map[int]bool{}
	var out []int
	for _, f := range files {
		if f.Kind != KindMain {
			continue
		}
		for _, k := range f.Keys() {
			if !seen[k] && (k >= 1000 || !media.IsShow()) {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	sort.Ints(out)
	return out
}

func (s *Service) Collection(ctx context.Context, refresh bool) (*CollectionView, error) {
	coll, err := s.Platform.Collection(ctx, "", refresh)
	if err != nil {
		return nil, err
	}
	all, _ := s.Store.All()
	all = append(all, s.shared()...)
	filesByMedia := map[int][]*LocalFile{}
	view := &CollectionView{}
	for _, f := range all {
		if f.Ignored {
			view.IgnoredCount++
			continue
		}
		if f.MediaID == 0 {
			view.UnmatchedCount++
			continue
		}
		filesByMedia[f.MediaID] = append(filesByMedia[f.MediaID], f)
	}
	last := s.History.LastWatched()

	makeItem := func(e *catalog.ListEntry, media *catalog.Media) *CollectionItem {
		it := &CollectionItem{Media: media, ListEntry: e, LastWatched: last[media.ID]}
		progress := 0
		if e != nil {
			progress = e.Progress
		}
		it.NextEpisode = media.NextEpisode(progress)
		it.Downloaded = downloadedKeys(media, filesByMedia[media.ID])
		it.NextHasFile = EpisodeFile(filesByMedia[media.ID], it.NextEpisode) != nil
		it.LocalFiles = len(filesByMedia[media.ID])
		return it
	}

	genres := map[string]int{}
	inList := map[int]bool{}
	byStatus := map[string]*CollectionList{}
	for _, e := range coll.Entries() {
		inList[e.MediaID] = true
		for _, g := range e.Media.Genres {
			genres[g]++
		}
		l, ok := byStatus[e.Status]
		if !ok {
			l = &CollectionList{Status: e.Status, Name: statusNames[e.Status]}
			byStatus[e.Status] = l
		}
		l.Items = append(l.Items, makeItem(e, e.Media))
	}
	for _, st := range catalog.Statuses {
		if l, ok := byStatus[st]; ok {
			sort.SliceStable(l.Items, func(i, j int) bool {
				a, b := l.Items[i], l.Items[j]
				if a.LastWatched != b.LastWatched {
					return a.LastWatched > b.LastWatched
				}
				if a.ListEntry != nil && b.ListEntry != nil && a.ListEntry.UpdatedAt != b.ListEntry.UpdatedAt {
					return a.ListEntry.UpdatedAt > b.ListEntry.UpdatedAt
				}
				return a.Media.PreferredTitle() < b.Media.PreferredTitle()
			})
			view.Lists = append(view.Lists, l)
		}
	}

	// Matched files whose title isn't in the list.
	var missing []int
	for id := range filesByMedia {
		if !inList[id] {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		medias, _ := s.Platform.MediaBatch(ctx, missing)
		for _, id := range missing {
			if m, ok := medias[id]; ok {
				view.LocalOnly = append(view.LocalOnly, makeItem(nil, m))
			} else {
				view.Incomplete++
			}
		}
		sort.Slice(view.LocalOnly, func(i, j int) bool {
			return view.LocalOnly[i].Media.PreferredTitle() < view.LocalOnly[j].Media.PreferredTitle()
		})
	}

	view.ContinueWatching = s.continueWatching(ctx, coll, filesByMedia)
	for g := range genres {
		view.Genres = append(view.Genres, g)
	}
	sort.Slice(view.Genres, func(i, j int) bool {
		if genres[view.Genres[i]] != genres[view.Genres[j]] {
			return genres[view.Genres[i]] > genres[view.Genres[j]]
		}
		return view.Genres[i] < view.Genres[j]
	})
	return view, nil
}

func (s *Service) continueWatching(ctx context.Context, coll *catalog.Collection, files map[int][]*LocalFile) []*ContinueItem {
	var out []*ContinueItem
	seen := map[int]bool{}
	last := s.History.LastWatched()
	hidden := s.hiddenContinue()

	add := func(media *catalog.Media, ep int, src string) {
		if media == nil || seen[media.ID] || ep <= 0 {
			return
		}
		// Removed by the user: stays away until the episode to continue
		// with changes or the title is watched again.
		if h, ok := hidden[media.ID]; ok && h.Episode == ep && last[media.ID] <= h.HiddenAt {
			seen[media.ID] = true // not offered again from the history below
			return
		}
		// Not out yet.
		if media.Status == "NOT_YET_RELEASED" {
			return
		}
		if aired := media.LatestAired(); media.IsShow() && media.Status == "RELEASING" && aired > 0 && ep > aired {
			return
		}
		it := &ContinueItem{Media: media, Episode: ep, Index: media.EpisodeIndex(ep), Total: media.TotalEpisodes(), LastWatched: last[media.ID], Source: src}
		if f := EpisodeFile(files[media.ID], ep); f != nil {
			it.HasFile, it.FilePath = true, f.Path
		}
		if h := s.History.Get(media.ID, ep); h != nil {
			it.ResumeAt = s.History.ResumePosition(media.ID, ep)
			it.Duration = h.Duration
		}
		if media.Duration != nil {
			it.Runtime = *media.Duration
		}
		it.Image = util.FirstNonEmpty(media.BannerImage, media.CoverImage.ExtraLarge)
		if !media.IsShow() {
			it.Title = media.PreferredTitle()
		} else if s.Platform != nil {
			season, _ := catalog.SplitKey(ep)
			if sd, err := s.Platform.Season(ctx, media.ID, season); err == nil {
				for _, e := range sd.Episodes {
					if e.Key == ep {
						it.Title = e.Title
						if e.Runtime > 0 {
							it.Runtime = e.Runtime
						}
						if e.Still != "" {
							it.Image = e.Still
						}
					}
				}
			}
		}
		seen[media.ID] = true
		out = append(out, it)
	}

	// 1) Titles being watched, ordered by most recent activity: on this
	// computer (or another Moviko on the same account, whose history comes
	// over), or a change of the list (an episode marked by hand, a sync).
	var watching []*catalog.ListEntry
	for _, e := range coll.Entries() {
		if e.Status == "CURRENT" || e.Status == "REPEATING" {
			watching = append(watching, e)
			last[e.MediaID] = max(last[e.MediaID], e.UpdatedAt)
		}
	}
	sort.SliceStable(watching, func(i, j int) bool {
		li, lj := last[watching[i].MediaID], last[watching[j].MediaID]
		if li != lj {
			return li > lj
		}
		return watching[i].UpdatedAt > watching[j].UpdatedAt
	})
	for _, e := range watching {
		ep := e.Media.NextEpisode(e.Progress)
		// A partially watched episode takes priority over the next one.
		if h := s.History.Get(e.MediaID, ep); h == nil && e.Progress > 0 {
			if hc := s.History.Get(e.MediaID, e.Progress); hc != nil && hc.Percent() < finishedAt {
				ep = e.Progress
			}
		}
		add(e.Media, ep, "list")
		if len(out) >= 20 {
			break
		}
	}
	// 2) Recently played things not on the list (local-only, streams).
	for _, h := range s.History.Recent(30) {
		if seen[h.MediaID] || len(out) >= 24 || s.Platform == nil {
			continue
		}
		media, err := s.Platform.MediaLite(ctx, h.MediaID)
		if err != nil {
			continue
		}
		ep := h.Episode
		if h.Percent() >= finishedAt {
			ep = media.NextEpisode(h.Episode)
		}
		add(media, ep, h.Source)
	}
	// Most recently watched first, list-only items keep their order after.
	sort.SliceStable(out, func(i, j int) bool { return out[i].LastWatched > out[j].LastWatched })
	return out
}

// continueHiddenKey holds the items removed from "Continue watching", by
// media id: the episode the item offered and when it was removed.
const continueHiddenKey = "continue-hidden"

type hiddenItem struct {
	Episode  int   `json:"episode"`
	HiddenAt int64 `json:"hiddenAt"` // unix seconds, like the watch history
}

func (s *Service) hiddenContinue() map[int]hiddenItem {
	out := map[int]hiddenItem{}
	_, _ = s.DB.GetKV(continueHiddenKey, &out)
	return out
}

// updateHidden changes the removed items under a lock, so requests at the
// same time can't overwrite each other's changes.
func (s *Service) updateHidden(fn func(items map[int]hiddenItem)) error {
	s.hiddenMu.Lock()
	defer s.hiddenMu.Unlock()
	items := map[int]hiddenItem{}
	if _, err := s.DB.GetKV(continueHiddenKey, &items); err != nil {
		return err
	}
	fn(items)
	if len(items) == 0 {
		return s.DB.DeleteKV(continueHiddenKey)
	}
	return s.DB.SetKV(continueHiddenKey, items)
}

// HideContinue removes a title from "Continue watching" for as long as it
// offers that episode: it comes back by itself once the episode changes
// (progress made elsewhere) or the title is watched again.
func (s *Service) HideContinue(mediaID, episode int) error {
	last := s.History.LastWatched()
	now := time.Now().Unix()
	return s.updateHidden(func(items map[int]hiddenItem) {
		for id, it := range items {
			if last[id] > it.HiddenAt {
				delete(items, id) // watched again since: back for good
			}
		}
		items[mediaID] = hiddenItem{Episode: episode, HiddenAt: now}
	})
}

// UnhideContinue puts a removed title back in "Continue watching".
func (s *Service) UnhideContinue(mediaID int) error {
	return s.updateHidden(func(items map[int]hiddenItem) { delete(items, mediaID) })
}

// EpisodeFile returns the file to play for an episode (by key) among a
// title's files, or nil (see betterFile): the movie's, or the file that
// holds the episode (a multi-episode file holds several). Extras and the
// later parts of a movie are never played for it.
func EpisodeFile(files []*LocalFile, episode int) *LocalFile {
	var best *LocalFile
	for _, f := range files {
		if (f.Kind == KindMain || f.Kind == KindSpecial) && !f.Ignored && f.Covers(episode) && (best == nil || betterFile(f, best)) {
			best = f
		}
	}
	return best
}

// betterFile reports whether f is the one to play when it and old are
// matched to the same episode, e.g. two releases of a movie: a match made
// by hand wins, then the surer match, then a file of that episode alone
// (rather than one holding several), then the bigger file. A file on this
// computer always wins.
func betterFile(f, old *LocalFile) bool {
	// This computer's file, not one another Moviko shares.
	if (f.Host == "") != (old.Host == "") {
		return f.Host == ""
	}
	if f.Locked != old.Locked {
		return f.Locked
	}
	if f.MatchScore != old.MatchScore {
		return f.MatchScore > old.MatchScore
	}
	if fm, om := f.EpisodeEnd > 0, old.EpisodeEnd > 0; fm != om {
		return !fm
	}
	return f.Size > old.Size
}
