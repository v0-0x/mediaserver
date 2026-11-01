package library

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/db"
	"github.com/v0-0x/mediaserver/movies/internal/release"
	"github.com/v0-0x/mediaserver/movies/internal/util"
)

// Matching: which movie or show each file is, and which of its episodes.
//
// Files are matched in groups, so a whole show folder is matched once: the
// files of one movie's or show's folder ("Breaking Bad (2008)/" with its
// season and extras folders) that have the same title, or the files loose
// in the library with the same title (and year, for movies). A group is
// matched by, in order:
//
//  1. the files already matched in its folder (a new episode of a show
//     matched before, a folder matched by hand);
//  2. the ids in its names (Plex/Jellyfin/Emby tags: {tmdb-603},
//     [imdbid-tt0133093], {tvdb-81189});
//  3. the user's list, a title alike enough (release.Similarity);
//  4. a TMDB search, for a show when the files have episodes or sit in
//     season folders, for a movie otherwise, with the year. What it finds
//     (or doesn't) for a title is remembered, so rescans don't search
//     again.
//
// Then each file gets its episode key (assign): the season and episode in
// its name, a number counted from the show's first episode, the episode
// that aired on the date of a daily show's file; specials are season 0; a
// movie's files are its one "episode" (and a movie split in parts plays
// from part 1). Extras never get a key.

// Matcher matches files to TMDB movies and shows.
type Matcher struct {
	platform    *catalog.Platform
	db          *db.DB // remembers what TMDB searches found; nil: nothing is
	threshold   float64
	outsideList bool
	// listFn gives the titles of the user's list (tests replace it).
	listFn func(ctx context.Context) []*catalog.Media

	mu     sync.Mutex
	failed int // TMDB requests that failed
	// decided holds this run's searches, by title (see decisionKey).
	decided map[string]decision
}

// decision is what a TMDB search found for a title: the media id (0:
// nothing) and how alike its title is.
type decision struct {
	ID    int     `json:"id"`
	Score float64 `json:"score"`
}

// How long what a search found for a title is remembered. A title not
// found is looked for again sooner: TMDB may add it, the name may be fixed.
const (
	decisionTTL     = 30 * 24 * time.Hour
	noDecisionTTL   = 24 * time.Hour
	decisionVersion = 1
)

// listSure is how alike a title of the user's list must be to be taken
// without asking TMDB.
const listSure = 0.9

// NewMatcher returns a matcher. d (optional) remembers searches across
// scans; threshold is the least title similarity a match needs (Settings ›
// Library), outsideList whether titles that aren't on the list are looked
// for on TMDB.
func NewMatcher(p *catalog.Platform, d *db.DB, threshold float64, outsideList bool) *Matcher {
	m := &Matcher{platform: p, db: d, threshold: threshold, outsideList: outsideList, decided: map[string]decision{}}
	m.listFn = func(ctx context.Context) []*catalog.Media {
		if p == nil {
			return nil
		}
		coll, err := p.Collection(ctx, "", false)
		if err != nil {
			return nil
		}
		var pool []*catalog.Media
		for _, e := range coll.Entries() {
			pool = append(pool, e.Media)
		}
		return pool
	}
	return m
}

// SearchFailures is how many TMDB requests failed: matches that may be
// better when TMDB answers.
func (m *Matcher) SearchFailures() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.failed
}

func (m *Matcher) fail() {
	m.mu.Lock()
	m.failed++
	m.mu.Unlock()
}

// ---------------------------------------------------------------------------
// Groups

// group is files matched together.
type group struct {
	folder string // their movie's or show's folder, "" for loose files
	title  string
	// titles are the titles to look for, best first: the name's, the
	// folder's, without a country ("The Office US" → "The Office").
	titles []string
	year   int
	show   bool // the files have episodes or seasons
	mains  int  // files that aren't extras
	tmdbID int
	imdbID string
	tvdbID int
	files  []*LocalFile
}

// folderKey identifies the files of one title in one folder, matched
// together across scans (see siblings).
func folderKey(folder, title string) string {
	if folder == "" {
		return ""
	}
	return folder + "\x00" + release.Normalize(title)
}

// groupFiles groups files by their movie's or show's folder and title. A
// folder's files with the same title are one group when any of them is a
// show's; a movie's are split by year (a folder may hold "Dune (1984)" and
// "Dune (2021)").
func groupFiles(files []*LocalFile) []*group {
	type bucket struct {
		folder, title string
		files         []*LocalFile
	}
	buckets := map[string]*bucket{}
	var order []string
	for _, f := range files {
		p := f.Parsed
		title := p.Title
		if title == "" && p.Folder != "" {
			title = p.FolderTitle
		}
		norm := release.Normalize(title)
		key := p.Folder + "\x00" + norm
		if norm == "" {
			key = "\x00file\x00" + f.Path // nothing to group by
		}
		b := buckets[key]
		if b == nil {
			b = &bucket{folder: p.Folder, title: title}
			buckets[key] = b
			order = append(order, key)
		}
		b.files = append(b.files, f)
	}

	var out []*group
	for _, key := range order {
		b := buckets[key]
		show := false
		for _, f := range b.files {
			if f.Parsed.Show && f.Parsed.Kind != KindExtra {
				show = true
			}
		}
		if !show && !slices.ContainsFunc(b.files, func(f *LocalFile) bool { return f.Parsed.Kind != KindExtra }) {
			// Only extras: they tell.
			show = slices.ContainsFunc(b.files, func(f *LocalFile) bool { return f.Parsed.Show })
		}
		if show {
			out = append(out, newGroup(b.folder, b.title, true, commonYear(b.files), b.files))
			continue
		}
		byYear := map[int][]*LocalFile{}
		var years []int
		for _, f := range b.files {
			if _, ok := byYear[f.Parsed.Year]; !ok {
				years = append(years, f.Parsed.Year)
			}
			byYear[f.Parsed.Year] = append(byYear[f.Parsed.Year], f)
		}
		// Files without a year go with the only year there is.
		if len(years) == 2 && byYear[0] != nil {
			y := years[0] + years[1]
			byYear[y] = append(byYear[y], byYear[0]...)
			delete(byYear, 0)
			years = []int{y}
		}
		sort.Ints(years)
		for _, y := range years {
			if fs := byYear[y]; len(fs) > 0 {
				out = append(out, newGroup(b.folder, b.title, false, y, fs))
			}
		}
	}
	return out
}

var reCountry = regexp.MustCompile(`(?i)^(.+?)[\s.]+\(?(US|UK|AU|NZ|CA|IE)\)?$`)

func newGroup(folder, title string, show bool, year int, files []*LocalFile) *group {
	g := &group{folder: folder, title: title, show: show, year: year, files: files}
	add := func(t string) {
		t = strings.TrimSpace(t)
		if release.Normalize(t) == "" {
			return
		}
		for _, x := range g.titles {
			if release.Normalize(x) == release.Normalize(t) {
				return
			}
		}
		g.titles = append(g.titles, t)
	}
	add(title)
	for _, f := range files {
		add(f.Parsed.FolderTitle)
	}
	for _, t := range slices.Clone(g.titles) {
		if m := reCountry.FindStringSubmatch(t); m != nil {
			add(m[1])
		}
	}
	for _, f := range files {
		if f.Parsed.Kind != KindExtra {
			g.mains++
		}
		if g.tmdbID == 0 {
			g.tmdbID = f.Parsed.TMDBID
		}
		if g.imdbID == "" {
			g.imdbID = f.Parsed.IMDbID
		}
		if g.tvdbID == 0 {
			g.tvdbID = f.Parsed.TVDBID
		}
	}
	return g
}

// commonYear is the year most of the files give (the earliest of a tie),
// 0 if none does.
func commonYear(files []*LocalFile) int {
	n := map[int]int{}
	best := 0
	for _, f := range files {
		if y := f.Parsed.Year; y > 0 {
			n[y]++
			if best == 0 || n[y] > n[best] || n[y] == n[best] && y < best {
				best = y
			}
		}
	}
	return best
}

// siblings are the media ids of the files already matched, by folder and
// title (folderKey), but those about to be matched again. A folder whose
// files disagree goes by those matched by hand, else isn't used.
func siblings(known []*LocalFile, matching []*LocalFile) map[string]int {
	skip := make(map[string]bool, len(matching))
	for _, f := range matching {
		skip[f.Path] = true
	}
	type vote struct {
		locked, auto map[int]bool
	}
	votes := map[string]*vote{}
	for _, f := range known {
		if f.MediaID <= 0 || f.Ignored || skip[f.Path] {
			continue
		}
		key := folderKey(f.Parsed.Folder, util.FirstNonEmpty(f.Parsed.Title, f.Parsed.FolderTitle))
		if key == "" {
			continue
		}
		v := votes[key]
		if v == nil {
			v = &vote{locked: map[int]bool{}, auto: map[int]bool{}}
			votes[key] = v
		}
		if f.Locked {
			v.locked[f.MediaID] = true
		} else {
			v.auto[f.MediaID] = true
		}
	}
	out := map[string]int{}
	for key, v := range votes {
		switch {
		case len(v.locked) == 1:
			for id := range v.locked {
				out[key] = id
			}
		case len(v.locked) == 0 && len(v.auto) == 1:
			for id := range v.auto {
				out[key] = id
			}
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Matching

// MatchFiles matches files (setting MediaID, Episode, EpisodeEnd, Kind and
// MatchScore). known are the files of the index, whose matches new files
// of the same folders follow. progress is called after each group.
func (m *Matcher) MatchFiles(ctx context.Context, files, known []*LocalFile, progress func(done, total int, title string)) {
	if len(files) == 0 {
		return
	}
	groups := groupFiles(files)
	sib := siblings(known, files)
	var pool []*catalog.Media
	poolLoaded := false
	for i, g := range groups {
		if ctx.Err() != nil {
			return
		}
		if !poolLoaded {
			pool, poolLoaded = m.listFn(ctx), true
		}
		media, score := m.resolve(ctx, g, sib, pool)
		if progress != nil {
			progress(i+1, len(groups), util.FirstNonEmpty(g.title, "files"))
		}
		if media == nil {
			for _, f := range g.files {
				f.MediaID, f.Episode, f.EpisodeEnd, f.MatchScore = 0, 0, 0, score
				f.Kind = f.Parsed.Kind
			}
			continue
		}
		m.assign(ctx, g.files, media, score)
	}
}

// resolve finds the movie or show of a group (nil when none is sure
// enough), and how sure the match is.
func (m *Matcher) resolve(ctx context.Context, g *group, sib map[string]int, pool []*catalog.Media) (*catalog.Media, float64) {
	// 1. The folder's other files.
	if id := sib[folderKey(g.folder, g.title)]; id > 0 {
		if media, err := m.mediaLite(ctx, id); err == nil {
			return media, 1
		}
	}
	// 2. Ids in the names.
	if g.tmdbID > 0 {
		id := catalog.MovieID(g.tmdbID)
		if g.show {
			id = catalog.ShowID(g.tmdbID)
		}
		media, err := m.mediaLite(ctx, id)
		if err == nil {
			return media, 1
		}
		if !errors.Is(err, catalog.ErrNotFound) {
			m.fail()
			return nil, 0
		}
	}
	if g.imdbID != "" && m.platform != nil {
		media, err := m.platform.FindByIMDb(ctx, g.imdbID)
		if err == nil {
			return media, 1
		}
		if !errors.Is(err, catalog.ErrNotFound) {
			m.fail()
			return nil, 0
		}
	}
	if g.tvdbID > 0 && m.platform != nil {
		kind := catalog.TypeMovie
		if g.show {
			kind = catalog.TypeTV
		}
		media, err := m.platform.FindByTVDB(ctx, g.tvdbID, kind)
		if err == nil {
			return media, 1
		}
		if !errors.Is(err, catalog.ErrNotFound) {
			m.fail()
			return nil, 0
		}
	}
	if len(g.titles) == 0 {
		return nil, 0
	}
	// 3. The user's list.
	best, score := m.fromList(g, pool)
	if best != nil && score >= max(m.threshold, listSure) {
		return best, score
	}
	if !m.outsideList || m.platform == nil {
		return nil, score
	}
	// 4. TMDB.
	return m.search(ctx, g)
}

// fromList picks the list's title most like the group's (of the same
// kind: shows for a show's files).
func (m *Matcher) fromList(g *group, pool []*catalog.Media) (*catalog.Media, float64) {
	var best *catalog.Media
	bestScore := 0.0
	for _, media := range pool {
		if media == nil || media.IsShow() != g.show {
			continue
		}
		s := titleScore(g.titles, media)
		if s == 0 {
			continue
		}
		if y := media.Year(); g.year > 0 && y > 0 {
			switch {
			case y == g.year:
				s += 0.05
			case !g.show && abs(y-g.year) > 1:
				// Another film of that name (a remake). A show's files may
				// carry the year of a later season.
				s -= 0.15
			}
		}
		if s > bestScore {
			best, bestScore = media, s
		}
	}
	return best, min(bestScore, 1)
}

// titleScore is how alike the best of titles and the media's titles are.
func titleScore(titles []string, media *catalog.Media) float64 {
	s := 0.0
	for _, t := range titles {
		for _, mt := range media.AllTitles() {
			s = max(s, release.Similarity(t, mt))
		}
	}
	return s
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func decisionKey(show bool, title string, year int) string {
	kind := "movie"
	if show {
		kind = "tv"
	}
	return fmt.Sprintf("library-match:%d:%s:%s:%d", decisionVersion, kind, release.Normalize(title), year)
}

// search looks the group up on TMDB, or remembers what an earlier search
// found.
func (m *Matcher) search(ctx context.Context, g *group) (*catalog.Media, float64) {
	key := decisionKey(g.show, g.title, g.year)
	m.mu.Lock()
	d, ok := m.decided[key]
	m.mu.Unlock()
	if !ok && m.db != nil {
		ok = m.db.GetCache(key, &d)
	}
	if ok {
		if d.ID == 0 {
			return nil, 0
		}
		media, err := m.mediaLite(ctx, d.ID)
		if err == nil && d.Score >= m.threshold {
			return media, d.Score
		}
		if err != nil && !errors.Is(err, catalog.ErrNotFound) {
			m.fail()
			return nil, 0
		}
	}
	media, score, err := m.find(ctx, g)
	if err != nil && !errors.Is(err, catalog.ErrNotFound) {
		m.fail() // not remembered: asked again next time
		return nil, 0
	}
	d = decision{Score: score}
	ttl := noDecisionTTL
	if media != nil {
		d.ID, ttl = media.ID, decisionTTL
	}
	m.mu.Lock()
	m.decided[key] = d
	m.mu.Unlock()
	if m.db != nil {
		m.db.SetCache(key, d, ttl)
	}
	return media, score
}

// find searches TMDB for each of the group's titles: shows for a show's
// files (with the year, then without: a show's files may carry the year of
// a season), movies for the others — and shows too when a folder holds
// several of them ("Sherlock/A Study in Pink.mkv", ...).
func (m *Matcher) find(ctx context.Context, g *group) (*catalog.Media, float64, error) {
	kinds := []string{catalog.TypeMovie}
	if g.show {
		kinds = []string{catalog.TypeTV}
	} else if g.mains > 1 && g.folder != "" {
		kinds = append(kinds, catalog.TypeTV)
	}
	for _, kind := range kinds {
		years := []int{g.year}
		if kind == catalog.TypeTV && g.year > 0 {
			years = append(years, 0)
		}
		for _, t := range g.titles {
			for _, y := range years {
				var media *catalog.Media
				var err error
				if kind == catalog.TypeTV {
					media, err = m.platform.FindShow(ctx, t, y)
				} else {
					media, err = m.platform.FindMovie(ctx, t, y)
				}
				if errors.Is(err, catalog.ErrNotFound) {
					continue
				}
				if err != nil {
					return nil, 0, err
				}
				s := titleScore([]string{t}, media)
				if g.year > 0 && media.Year() == g.year {
					s = min(s+0.05, 1)
				}
				if s >= m.threshold {
					return media, s, nil
				}
			}
		}
	}
	// A film's year can be further off than TMDB's searches allow (the year
	// of its release rather than of its festival premiere...): once more
	// without it, for the same title of a film a few years apart at most,
	// not a remake.
	if !g.show && g.year > 0 {
		for _, t := range g.titles {
			media, err := m.platform.FindMovie(ctx, t, 0)
			if errors.Is(err, catalog.ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, 0, err
			}
			y := media.Year()
			if s := titleScore([]string{t}, media); s >= max(m.threshold, noYearSure) && (y == 0 || abs(y-g.year) <= noYearSpan) {
				return media, s, nil
			}
		}
	}
	return nil, 0, catalog.ErrNotFound
}

// A film found without its year must have the title looked for, at most
// noYearSpan years from the one in the names.
const (
	noYearSure = 0.95
	noYearSpan = 5
)

// mediaLite is a title with its seasons.
func (m *Matcher) mediaLite(ctx context.Context, id int) (*catalog.Media, error) {
	if m.platform == nil {
		return nil, errors.New("no catalog")
	}
	return m.platform.MediaLite(ctx, id)
}

// ---------------------------------------------------------------------------
// Episodes

// assign matches files to media and gives each its episode key.
func (m *Matcher) assign(ctx context.Context, files []*LocalFile, media *catalog.Media, score float64) {
	for _, f := range files {
		f.MediaID, f.MatchScore = media.ID, score
		f.Kind, f.Episode, f.EpisodeEnd = f.Parsed.Kind, 0, 0
		switch {
		case f.Kind == KindExtra:
		case !media.IsShow():
			f.Kind, f.Episode = KindMain, catalog.MovieEpisode
		default:
			f.Episode, f.EpisodeEnd = m.showKey(ctx, f.Parsed, media)
			f.Kind = kindOfKey(f.Episode, f.Kind)
		}
	}
	if !media.IsShow() {
		markParts(files)
	}
}

// kindOfKey is the kind of a show's file with that key: season 0 holds the
// specials.
func kindOfKey(key int, kind string) string {
	switch {
	case kind == KindExtra:
		return KindExtra
	case key >= 1000:
		return KindMain
	case key > 0:
		return KindSpecial
	}
	if kind == KindPart {
		return KindMain
	}
	return kind
}

// markParts marks the later parts of a movie split across files (CD1, CD2
// in one folder): the movie plays from its first part.
func markParts(files []*LocalFile) {
	byDir := map[string][]*LocalFile{}
	for _, f := range files {
		if f.Kind == KindMain && f.Parsed.Part > 0 {
			byDir[f.Dir] = append(byDir[f.Dir], f)
		}
	}
	for _, parts := range byDir {
		if len(parts) < 2 {
			continue
		}
		first := slices.MinFunc(parts, func(a, b *LocalFile) int { return a.Parsed.Part - b.Parsed.Part })
		for _, f := range parts {
			if f != first && f.Parsed.Part > first.Parsed.Part {
				f.Kind = KindPart
			}
		}
	}
}

// showKey is the key of the episode a show's file holds, and of the last
// one for a multi-episode file (0: one episode). 0 when unknown.
func (m *Matcher) showKey(ctx context.Context, p Parsed, media *catalog.Media) (key, end int) {
	switch {
	case p.Season >= 0 && p.Episode > 0 && p.Episode < 1000:
		key = catalog.EpisodeKey(p.Season, p.Episode)
		if p.EpisodeEnd > p.Episode && p.EpisodeEnd < 1000 {
			end = catalog.EpisodeKey(p.Season, p.EpisodeEnd)
		}
		if p.Season > 0 && !hasEpisode(media, key) {
			if k := otherNumbering(media, p.Season, p.Episode, p.Absolute); k > 0 {
				return k, 0
			}
		}
		return key, end
	case p.Season >= 0 && p.Absolute > 0:
		// "Season 21/One Piece - 1050.mkv"
		if k := otherNumbering(media, p.Season, p.Absolute, p.Absolute); k > 0 {
			return k, 0
		}
		return media.KeyAtIndex(p.Absolute), 0
	case p.Season > 0 && p.Episode == 0 && p.EpisodeTitle != "":
		// "Season 04/Show - S04E00 - Title.mkv": a special that aired
		// during season 4 (Plex's way), found by its title.
		return m.specialByTitle(ctx, media, p.EpisodeTitle), 0
	case p.Date != "":
		return m.dateKey(ctx, media, p), 0
	case p.Season == 0 && p.EpisodeTitle != "":
		return m.specialByTitle(ctx, media, p.EpisodeTitle), 0
	case p.Season < 0 && p.Episode > 0:
		key = absoluteKey(media, p.Episode)
		if p.EpisodeEnd > p.Episode {
			if e := absoluteKey(media, p.EpisodeEnd); sameSeasonAfter(key, e) {
				end = e
			}
		}
		return key, end
	}
	return 0, 0
}

// otherNumbering is the key of the episode a file of season's folder
// numbered n means when the season has no episode n (0: none it can be):
// n counted from the show's first episode ("Season 02/Show - 13.mkv" where
// season 2 is episodes 13-24; absolute: the name's number has no season),
// else the season's number followed by the episode's ("Season 01/101.mkv",
// "Show - 1013" in season 10), else, counted from the first episode, an
// episode of another season.
func otherNumbering(media *catalog.Media, season, n, absolute int) int {
	if absolute > 0 {
		if k := media.KeyAtIndex(absolute); k > 0 && k/1000 == season {
			return k
		}
	}
	if len(media.Seasons) > 0 && n/100 == season && n%100 > 0 {
		if k := catalog.EpisodeKey(season, n%100); hasEpisode(media, k) {
			return k
		}
	}
	if absolute > 0 {
		return media.KeyAtIndex(absolute)
	}
	return 0
}

// absoluteKey is the key of episode n of a show counted from its first
// episode ("One Piece - 1050"), season 1's when the seasons aren't known
// (or the number is past them).
func absoluteKey(media *catalog.Media, n int) int {
	if k := media.KeyAtIndex(n); k > 0 {
		return k
	}
	if n > 0 && n < 1000 {
		return catalog.EpisodeKey(1, n)
	}
	return 0
}

// hasEpisode reports whether the show has the episode, as far as its
// season list tells.
func hasEpisode(media *catalog.Media, key int) bool {
	if len(media.Seasons) == 0 {
		return true
	}
	s, e := catalog.SplitKey(key)
	for _, x := range media.Seasons {
		if x.Number == s {
			return x.EpisodeCount == 0 || e <= x.EpisodeCount
		}
	}
	return false
}

// dateKey finds the episode of a daily show that aired on the file's date,
// in the season the name gives, else in the seasons that had started by
// then (the latest two), else in the season numbered after the year.
func (m *Matcher) dateKey(ctx context.Context, media *catalog.Media, p Parsed) int {
	if m.platform == nil || len(p.Date) < 4 {
		return 0
	}
	var tries []int
	add := func(n int) {
		if n >= 0 && !slices.Contains(tries, n) {
			tries = append(tries, n)
		}
	}
	if p.Season >= 0 {
		add(p.Season)
	}
	started := slices.Clone(media.Seasons)
	started = slices.DeleteFunc(started, func(s catalog.Season) bool { return s.Number <= 0 || s.AirDate == "" || s.AirDate > p.Date })
	sort.Slice(started, func(i, j int) bool { return started[i].AirDate > started[j].AirDate })
	for i := 0; i < len(started) && i < 2; i++ {
		add(started[i].Number)
	}
	var year int
	fmt.Sscanf(p.Date[:4], "%d", &year)
	if media.SeasonEpisodeCount(year) > 0 {
		add(year)
	}
	for _, n := range tries {
		sd, err := m.platform.Season(ctx, media.ID, n)
		if err != nil {
			if !errors.Is(err, catalog.ErrNotFound) {
				m.fail()
			}
			continue
		}
		for _, e := range sd.Episodes {
			if e.AirDate == p.Date {
				return e.Key
			}
		}
	}
	return 0
}

// specialByTitle finds a special without a number by its title
// ("Specials/Good Cop Bad Cop.mkv").
func (m *Matcher) specialByTitle(ctx context.Context, media *catalog.Media, title string) int {
	if m.platform == nil || media.SeasonEpisodeCount(0) == 0 {
		return 0
	}
	sd, err := m.platform.Season(ctx, media.ID, 0)
	if err != nil {
		if !errors.Is(err, catalog.ErrNotFound) {
			m.fail()
		}
		return 0
	}
	best, bestScore := 0, 0.0
	for _, e := range sd.Episodes {
		if s := release.Similarity(title, e.Title); s > bestScore {
			best, bestScore = e.Key, s
		}
	}
	if bestScore < listSure {
		return 0
	}
	return best
}

// ---------------------------------------------------------------------------
// Matching by hand

// ManualOptions say how files matched by hand to a show are numbered.
type ManualOptions struct {
	// Absolute: the numbers in the names count from the show's first
	// episode (episode 13 of a show with 12 in season 1 is S02E01).
	Absolute bool
	// Season, when set, is the season the numbers in the names are
	// episodes of (0: the specials).
	Season *int
	// Offset is added to every number.
	Offset int
	// Renumber numbers the files 1, 2, 3… in file-name order instead
	// (counted from the first episode, or in Season).
	Renumber bool
	// Less orders file names for Renumber.
	Less func(a, b string) bool
}

// AssignManual matches files to media by hand: they're locked, so scans
// keep the match. A movie's files are the movie (or its parts and
// extras); a show's are numbered as opt says, from their names otherwise.
func (m *Matcher) AssignManual(ctx context.Context, files []*LocalFile, media *catalog.Media, opt ManualOptions) {
	for _, f := range files {
		f.Locked, f.Ignored = true, false
	}
	m.assign(ctx, files, media, 1)
	if !media.IsShow() || !opt.Absolute && opt.Season == nil && opt.Offset == 0 && !opt.Renumber {
		return
	}
	order := map[*LocalFile]int{}
	if opt.Renumber {
		var main []*LocalFile
		for _, f := range files {
			if f.Parsed.Kind == KindMain {
				main = append(main, f)
			}
		}
		less := opt.Less
		if less == nil {
			less = func(a, b string) bool { return a < b }
		}
		sort.SliceStable(main, func(i, j int) bool { return less(main[i].Name, main[j].Name) })
		for i, f := range main {
			order[f] = i + 1
		}
	}
	for _, f := range files {
		if f.Parsed.Kind == KindExtra {
			continue
		}
		first, last := f.Parsed.Episode, f.Parsed.EpisodeEnd
		if opt.Renumber {
			first, last = order[f], 0
		}
		if first <= 0 {
			if opt.Renumber {
				f.Episode, f.EpisodeEnd = 0, 0
			}
			continue // no number: as the name says (a date...)
		}
		first += opt.Offset
		if last > 0 {
			last += opt.Offset
		}
		key, end := 0, 0
		switch {
		case opt.Season != nil:
			if first < 1000 {
				key = catalog.EpisodeKey(*opt.Season, first)
			}
			if last > first && last < 1000 {
				end = catalog.EpisodeKey(*opt.Season, last)
			}
		case opt.Absolute || opt.Renumber:
			key = absoluteKey(media, first)
			if last > first {
				if e := absoluteKey(media, last); sameSeasonAfter(key, e) {
					end = e
				}
			}
		default: // shifted numbers of the names' seasons
			p := f.Parsed
			p.Episode, p.EpisodeEnd, p.Absolute = first, last, 0
			key, end = m.showKey(ctx, p, media)
		}
		f.Episode, f.EpisodeEnd = key, end
		f.Kind = kindOfKey(key, f.Parsed.Kind)
	}
}
