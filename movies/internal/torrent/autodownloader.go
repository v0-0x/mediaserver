package torrent

// Auto downloader
//
// The auto downloader keeps the shows the user follows downloaded: each
// rule names a show, and every few minutes (Settings › Torrent client ›
// Auto downloader) Moviko works out which of its episodes are wanted —
// the regular episodes that aired after the last one watched (or the
// rule's start), that aren't in the library and weren't grabbed before —
// searches every torrent source for each, picks the best release that
// passes the rule's filters and sends it to the torrent client, saved
// where the library finds it. When several episodes of a season that has
// finished airing are wanted and the rule allows it, a season pack is
// looked for first.
//
// What was grabbed is remembered (autodownload_items: "mediaId:key", key
// being the episode's, or season*1000 for a season pack), so an episode is
// grabbed once even before its file shows up in the library.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/db"
	"github.com/v0-0x/mediaserver/movies/internal/events"
	"github.com/v0-0x/mediaserver/movies/internal/library"
)

// Rule tells the auto downloader to keep a show downloaded, and which
// releases it may take.
type Rule struct {
	ID      int  `json:"id"`
	MediaID int  `json:"mediaId"`
	Enabled bool `json:"enabled"`
	// Title is the show's title when the rule was saved (for display).
	Title string `json:"title"`
	// Resolution a release must have: "2160", "1080", "720", "480" or ""
	// (any).
	Resolution string `json:"resolution"`
	// ReleaseGroups are preferred, in order: a release of one of them is
	// taken over better-seeded ones of other groups.
	ReleaseGroups []string `json:"releaseGroups"`
	// MustContain: words a release name must all contain (any case);
	// MustNotContain: words it must not contain.
	MustContain    []string `json:"mustContain"`
	MustNotContain []string `json:"mustNotContain"`
	// MinSeeders a release needs (at least 1 anyway).
	MinSeeders int `json:"minSeeders"`
	// Packs allows season packs when several episodes of a season that
	// finished airing are wanted.
	Packs bool `json:"packs"`
	// Destination is a folder to save to instead of the library's.
	Destination string `json:"destination"`
	// StartFrom is the key of the first episode wanted; 0: the one after
	// the last watched (the list's progress). Episodes before the progress
	// are never wanted.
	StartFrom int `json:"startFrom"`
}

// AutoItem is something the auto downloader grabbed.
type AutoItem struct {
	Key    string `json:"key"`
	RuleID int    `json:"ruleId"`
	// MediaID is the show, Episode the episode's key, or the season's
	// (season*1000) for a season pack.
	MediaID   int    `json:"mediaId"`
	Episode   int    `json:"episode"`
	Pack      bool   `json:"pack"`
	Title     string `json:"title"` // the torrent's name
	CreatedAt int64  `json:"createdAt"`
}

// RunReport tells what a run did.
type RunReport struct {
	Added int `json:"added"`
	// Wanted is how many episodes were looked for.
	Wanted int `json:"wanted"`
	// Errors are the rules' (and sources') problems, "Show: what".
	Errors []string `json:"errors"`
	// Busy: another run was going on; this one did nothing.
	Busy bool `json:"busy,omitempty"`
}

// showCatalog is what the auto downloader needs of the catalog
// (catalog.Platform).
type showCatalog interface {
	MediaLite(ctx context.Context, id int) (*catalog.Media, error)
	Collection(ctx context.Context, mediaType string, refresh bool) (*catalog.Collection, error)
	Season(ctx context.Context, id, season int) (*catalog.SeasonDetail, error)
}

// libraryFiles is what it needs of the library (library.Store).
type libraryFiles interface {
	ByMedia(mediaID int) ([]*library.LocalFile, error)
}

type AutoDownloader struct {
	db       *db.DB
	torrents *Manager
	catalog  showCatalog
	files    libraryFiles
	hub      *events.Hub
	mu       sync.Mutex
	now      func() time.Time
	// Send, when set, sends a release where this Moviko's downloads go (the
	// computer a TV's go to), and reports whether it did; else it goes to
	// the torrent client here.
	Send func(ctx context.Context, mediaID int, uris []string) (bool, error)
}

func NewAutoDownloader(d *db.DB, t *Manager, p *catalog.Platform, f *library.Store, hub *events.Hub) *AutoDownloader {
	a := &AutoDownloader{db: d, torrents: t, hub: hub, now: time.Now}
	// Typed nils would make non-nil interfaces.
	if p != nil {
		a.catalog = p
	}
	if f != nil {
		a.files = f
	}
	return a
}

// ---------------------------------------------------------------------------
// Rules

func (a *AutoDownloader) Rules() ([]*Rule, error) {
	rows, err := a.db.Query(`SELECT id, data FROM autodownload_rules ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Rule{}
	for rows.Next() {
		var id int
		var raw string
		if rows.Scan(&id, &raw) != nil {
			continue
		}
		var r Rule
		if json.Unmarshal([]byte(raw), &r) == nil {
			r.ID = id
			r.tidy()
			out = append(out, &r)
		}
	}
	return out, rows.Err()
}

// Rule returns the rule with that id, nil if there's none.
func (a *AutoDownloader) Rule(id int) (*Rule, error) {
	rules, err := a.Rules()
	if err != nil {
		return nil, err
	}
	for _, r := range rules {
		if r.ID == id {
			return r, nil
		}
	}
	return nil, nil
}

// tidy cleans a rule up: trimmed words without blanks, a resolution as
// "1080", no negative numbers.
func (r *Rule) tidy() {
	words := func(list []string) []string {
		out := []string{}
		for _, w := range list {
			if w = strings.TrimSpace(w); w != "" && !hasFold(out, w) {
				out = append(out, w)
			}
		}
		return out
	}
	r.ReleaseGroups = words(r.ReleaseGroups)
	r.MustContain = words(r.MustContain)
	r.MustNotContain = words(r.MustNotContain)
	r.Resolution = strings.TrimSuffix(wantedResolution(r.Resolution), "p")
	r.MinSeeders = max(r.MinSeeders, 0)
	r.StartFrom = max(r.StartFrom, 0)
	r.Title = strings.TrimSpace(r.Title)
	r.Destination = strings.TrimSpace(r.Destination)
}

// SaveRule creates (ID 0) or updates a rule.
func (a *AutoDownloader) SaveRule(ctx context.Context, r Rule) (*Rule, error) {
	if !catalog.IsShow(r.MediaID) {
		return nil, errors.New("a rule needs a show")
	}
	r.tidy()
	if r.Title == "" && a.catalog != nil {
		if m, err := a.catalog.MediaLite(ctx, r.MediaID); err == nil {
			r.Title = m.PreferredTitle()
		}
	}
	raw, _ := json.Marshal(r)
	if r.ID > 0 {
		res, err := a.db.Write(`UPDATE autodownload_rules SET data = ? WHERE id = ?`, string(raw), r.ID)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, fmt.Errorf("there's no rule %d", r.ID)
		}
		return &r, nil
	}
	res, err := a.db.Write(`INSERT INTO autodownload_rules(data) VALUES(?)`, string(raw))
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	r.ID = int(id)
	return &r, nil
}

func (a *AutoDownloader) DeleteRule(id int) error {
	_, err := a.db.Write(`DELETE FROM autodownload_rules WHERE id = ?`, id)
	return err
}

// ---------------------------------------------------------------------------
// What was grabbed

// itemKey is how a grabbed episode (or season pack, key season*1000) is
// recorded.
func itemKey(mediaID, key int) string { return fmt.Sprintf("%d:%d", mediaID, key) }

func parseItemKey(k string) (mediaID, key int) {
	a, b, _ := strings.Cut(k, ":")
	mediaID, _ = strconv.Atoi(a)
	key, _ = strconv.Atoi(b)
	return mediaID, key
}

func (a *AutoDownloader) Items() []AutoItem {
	rows, err := a.db.Query(`SELECT key, rule_id, title, created_at FROM autodownload_items ORDER BY created_at DESC, key LIMIT 200`)
	if err != nil {
		return []AutoItem{}
	}
	defer rows.Close()
	out := []AutoItem{}
	for rows.Next() {
		var it AutoItem
		if rows.Scan(&it.Key, &it.RuleID, &it.Title, &it.CreatedAt) == nil {
			it.MediaID, it.Episode = parseItemKey(it.Key)
			_, e := catalog.SplitKey(it.Episode)
			it.Pack = it.Episode > 0 && e == 0
			out = append(out, it)
		}
	}
	return out
}

// ForgetItem forgets that something was grabbed, so that the episode (or
// season) can be grabbed again.
func (a *AutoDownloader) ForgetItem(key string) error {
	_, err := a.db.Write(`DELETE FROM autodownload_items WHERE key = ?`, key)
	return err
}

// grabbed returns the keys recorded for a show: episodes, and seasons
// (season*1000) whose pack was grabbed.
func (a *AutoDownloader) grabbed(mediaID int) (map[int]bool, error) {
	rows, err := a.db.Query(`SELECT key FROM autodownload_items WHERE key LIKE ?`, strconv.Itoa(mediaID)+":%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]bool{}
	for rows.Next() {
		var k string
		if rows.Scan(&k) == nil {
			if id, key := parseItemKey(k); id == mediaID {
				out[key] = true
			}
		}
	}
	return out, rows.Err()
}

func (a *AutoDownloader) record(ruleID, mediaID, key int, name string) {
	if _, err := a.db.Write(`INSERT OR IGNORE INTO autodownload_items(key, rule_id, title, created_at) VALUES(?, ?, ?, ?)`,
		itemKey(mediaID, key), ruleID, name, a.now().Unix()); err != nil {
		log.Printf("auto downloader: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Running

// maxSearches bounds the searches of one rule in one run (each asks every
// source); what's left is looked for in the next runs.
const maxSearches = 8

// packMinWanted is how many episodes of a season must be wanted for a
// season pack to be looked for.
const packMinWanted = 3

// errClientDown stops a run: every other torrent would fail the same way.
var errClientDown = errors.New("the torrent client isn't available")

// Run checks every enabled rule once.
func (a *AutoDownloader) Run(ctx context.Context) (*RunReport, error) {
	rep := &RunReport{Errors: []string{}}
	if !a.mu.TryLock() {
		rep.Busy = true
		return rep, nil
	}
	defer a.mu.Unlock()
	rules, err := a.Rules()
	if err != nil {
		return rep, err
	}
	var coll *catalog.Collection
	var collErr error
	if a.catalog != nil {
		coll, collErr = a.catalog.Collection(ctx, catalog.TypeTV, false)
	} else {
		collErr = errors.New("no catalog")
	}
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		if ctx.Err() != nil {
			return rep, ctx.Err()
		}
		if coll == nil {
			// Without the progress every missing episode would look new,
			// the ones already watched too.
			rep.fail(r, fmt.Errorf("the list couldn't be loaded to check what's watched: %v", collErr))
			continue
		}
		err := a.runRule(ctx, r, coll, rep)
		if errors.Is(err, errClientDown) {
			rep.fail(r, err)
			return rep, nil
		}
		if err != nil {
			rep.fail(r, err)
		}
	}
	return rep, nil
}

func (rep *RunReport) fail(r *Rule, err error) {
	name := r.Title
	if name == "" {
		name = fmt.Sprintf("Rule %d", r.ID)
	}
	msg := name + ": " + err.Error()
	log.Printf("auto downloader: %s", msg)
	for _, e := range rep.Errors {
		if e == msg {
			return
		}
	}
	rep.Errors = append(rep.Errors, msg)
}

// Wanted returns the keys of the episodes a rule looks for now, in order.
func (a *AutoDownloader) Wanted(ctx context.Context, r *Rule) ([]int, error) {
	if a.catalog == nil || a.files == nil {
		return nil, errors.New("the auto downloader isn't set up")
	}
	coll, err := a.catalog.Collection(ctx, catalog.TypeTV, false)
	if err != nil {
		return nil, err
	}
	w, err := a.wanted(ctx, r, coll)
	if err != nil {
		return nil, err
	}
	if w.keys == nil {
		return []int{}, nil
	}
	return w.keys, nil
}

// want is what a rule wants now: the keys of the episodes, in order, of
// media (whose last aired episode is limit), which has files in the
// library.
type want struct {
	media *catalog.Media
	keys  []int
	files []*library.LocalFile
	limit int
}

// wanted works out which episodes of a rule's show to look for.
func (a *AutoDownloader) wanted(ctx context.Context, r *Rule, coll *catalog.Collection) (*want, error) {
	media, err := a.catalog.MediaLite(ctx, r.MediaID)
	if err != nil {
		return nil, err
	}
	if !media.IsShow() {
		return nil, errors.New("not a show")
	}
	progress := 0
	if e := coll.Find(r.MediaID); e != nil {
		progress = e.Progress
	}
	files, err := a.files.ByMedia(r.MediaID)
	if err != nil {
		// Without the library, episodes already there would be grabbed again.
		return nil, err
	}
	have := map[int]bool{}
	for _, f := range files {
		if f.Kind == "" || f.Kind == "main" {
			have[f.Episode] = true
			for _, k := range f.Keys() { // a multi-episode file's others
				have[k] = true
			}
		}
	}
	grabbed, err := a.grabbed(r.MediaID)
	if err != nil {
		return nil, err
	}
	w := &want{media: media, files: files, limit: a.airedLimit(ctx, media)}
	w.keys = wantedEpisodes(media, progress, r.StartFrom, w.limit, have, grabbed)
	return w, nil
}

// wantedEpisodes lists the regular episodes of a show up to limit (the
// last one aired) after progress (the last one watched), from startFrom
// on, that aren't in the library (have) nor grabbed (episodes, and seasons
// whose pack was).
func wantedEpisodes(media *catalog.Media, progress, startFrom, limit int, have, grabbed map[int]bool) []int {
	if limit <= 0 {
		return nil
	}
	keys := media.EpisodeKeys()
	// A season the show's data doesn't list yet (it's a day old at most):
	// its episodes up to the last aired.
	if ls, le := catalog.SplitKey(limit); ls > 0 && (len(keys) == 0 || keys[len(keys)-1] < catalog.EpisodeKey(ls, 1)) {
		for e := 1; e <= le; e++ {
			keys = append(keys, catalog.EpisodeKey(ls, e))
		}
	}
	var out []int
	for _, k := range keys {
		s, _ := catalog.SplitKey(k)
		if s <= 0 || k <= progress || k < startFrom || k > limit || have[k] || grabbed[k] || grabbed[catalog.EpisodeKey(s, 0)] {
			continue
		}
		out = append(out, k)
	}
	return out
}

// airedLimit is the key of the last episode of the show out now (0:
// none). The show's data says which aired last; when it doesn't, the air
// dates of its latest seasons do.
func (a *AutoDownloader) airedLimit(ctx context.Context, media *catalog.Media) int {
	now := a.now()
	if limit := episodeLimit(media, now); limit > 0 || media.Status == "NOT_YET_RELEASED" {
		return limit
	}
	seasons := append([]catalog.Season(nil), media.Seasons...)
	sort.Slice(seasons, func(i, j int) bool { return seasons[i].Number > seasons[j].Number })
	today := now.UTC().Format("2006-01-02")
	tried := 0
	for _, s := range seasons {
		if s.Number <= 0 || tried == 2 {
			continue
		}
		if s.AirDate != "" && s.AirDate > today {
			continue
		}
		tried++
		d, err := a.catalog.Season(ctx, media.ID, s.Number)
		if err != nil {
			continue
		}
		last := 0
		for _, e := range d.Episodes {
			if e != nil && e.Number > 0 && e.AirDate != "" && e.AirDate <= today {
				last = max(last, catalog.EpisodeKey(s.Number, e.Number))
			}
		}
		if last > 0 {
			return last
		}
	}
	return 0
}

// episodeLimit returns the key of the last episode that can be out (0:
// unknown): the last one aired. The media may come from a cache up to a day
// old: an episode whose airing time has passed since counts as aired.
func episodeLimit(m *catalog.Media, now time.Time) int {
	limit := m.LatestAired()
	if next := m.NextAiringEpisode; next != nil && next.Episode > 0 && next.AiringAt > 0 && int64(next.AiringAt) <= now.Unix() {
		limit = max(limit, next.Episode)
	}
	return limit
}

// runRule looks for the wanted episodes of a rule and grabs what it finds.
func (a *AutoDownloader) runRule(ctx context.Context, r *Rule, coll *catalog.Collection, rep *RunReport) error {
	if a.catalog == nil || a.files == nil {
		return errors.New("the auto downloader isn't set up")
	}
	w, err := a.wanted(ctx, r, coll)
	if err != nil {
		return err
	}
	media, wanted, files, limit := w.media, w.keys, w.files, w.limit
	if r.Title == "" {
		r.Title = media.PreferredTitle()
	}
	rep.Wanted += len(wanted)
	if len(wanted) == 0 {
		return nil
	}
	base := showQuery(media)
	searches := 0
	search := func(q MediaQuery) []*SearchResult {
		searches++
		res, errs := SearchAll(ctx, a.torrents.Sources(), q)
		if len(res) == 0 {
			for _, e := range errs {
				rep.fail(r, e)
			}
		}
		return res
	}
	// The latest season first, then the others in order: episodes of older
	// seasons that no source has (or none that passes the rule) would
	// otherwise use up the searches of every run, and the new episodes
	// would never be looked for.
	seasons := seasonsOf(wanted)
	if n := len(seasons); n > 1 {
		seasons = append([]int{seasons[n-1]}, seasons[:n-1]...)
	}
	for _, season := range seasons {
		eps := episodesOf(wanted, season)
		if r.Packs && len(eps) >= packMinWanted && seasonAired(media, season, limit) && searches < maxSearches {
			q := base
			q.Season, q.Episode, q.Resolution = season, 0, r.Resolution
			if best := pickPack(search(q), r, season); best != nil {
				err := a.grab(ctx, r, media, files, best, season, catalog.EpisodeKey(season, 0))
				if errors.Is(err, errClientDown) {
					return err
				}
				if err == nil {
					rep.Added++
					continue
				}
				rep.fail(r, fmt.Errorf("%s: %w", best.Name, err))
			}
		}
		for _, key := range eps {
			if searches >= maxSearches || ctx.Err() != nil {
				return ctx.Err()
			}
			q := base
			q.Season, q.Episode = catalog.SplitKey(key)
			q.Resolution = r.Resolution
			best := pickEpisode(search(q), r, key)
			if best == nil {
				continue
			}
			err := a.grab(ctx, r, media, files, best, season, key)
			if errors.Is(err, errClientDown) {
				return err
			}
			if err != nil {
				rep.fail(r, fmt.Errorf("%s: %w", best.Name, err))
				continue
			}
			rep.Added++
		}
	}
	return nil
}

// showQuery is the search for a show, without season and episode.
func showQuery(media *catalog.Media) MediaQuery {
	return MediaQuery{
		Title:     media.EnglishOrRomaji(),
		AltTitles: append([]string{media.Title.Native}, media.AllTitles()...),
		Year:      media.Year(),
		IMDbID:    media.IMDbID,
		TMDBID:    media.TMDBID,
		TVDBID:    media.TVDBID,
		Type:      MediaTV,
		Media:     media,
	}
}

func seasonsOf(keys []int) []int {
	var out []int
	for _, k := range keys {
		if s, _ := catalog.SplitKey(k); len(out) == 0 || out[len(out)-1] != s {
			out = append(out, s)
		}
	}
	return out
}

func episodesOf(keys []int, season int) []int {
	var out []int
	for _, k := range keys {
		if s, _ := catalog.SplitKey(k); s == season {
			out = append(out, k)
		}
	}
	return out
}

// seasonAired reports whether every episode of a season has aired: only
// then do whole-season packs exist.
func seasonAired(media *catalog.Media, season, limit int) bool {
	n := media.SeasonEpisodeCount(season)
	return n > 0 && catalog.EpisodeKey(season, n) <= limit
}

// passes reports whether a release passes a rule's filters.
func (r *Rule) passes(res *SearchResult) bool {
	if res.Seeders < max(r.MinSeeders, 1) {
		return false
	}
	if want := wantedResolution(r.Resolution); want != "" && res.Resolution != want {
		return false
	}
	// "WEB-DL" is in "Show.S01E01.WEB-DL" and in "Show S01E01 WEB DL".
	name := strings.ToLower(res.Name)
	words := " " + strings.ToLower(plainTitle(res.Name)) + " "
	has := func(w string) bool {
		w = strings.ToLower(w)
		pw := strings.ToLower(plainTitle(w))
		return strings.Contains(name, w) || (pw != "" && strings.Contains(words, " "+pw+" "))
	}
	for _, w := range r.MustContain {
		if !has(w) {
			return false
		}
	}
	for _, w := range r.MustNotContain {
		if has(w) {
			return false
		}
	}
	return true
}

// groupRank is where a release's group comes in the rule's preferred
// groups (len of them when it isn't one).
func (r *Rule) groupRank(res *SearchResult) int {
	for i, g := range r.ReleaseGroups {
		if strings.EqualFold(g, res.ReleaseGroup) {
			return i
		}
	}
	return len(r.ReleaseGroups)
}

// best is the release a rule prefers among candidates: of a preferred
// group first, then the best seeded.
func (r *Rule) best(candidates []*SearchResult) *SearchResult {
	var best *SearchResult
	for _, c := range candidates {
		if best == nil {
			best = c
			continue
		}
		if gc, gb := r.groupRank(c), r.groupRank(best); gc != gb {
			if gc < gb {
				best = c
			}
			continue
		}
		if c.Seeders > best.Seeders {
			best = c
		}
	}
	return best
}

// pickEpisode picks the release of the one episode key that the rule
// likes best, nil when none passes.
func pickEpisode(results []*SearchResult, r *Rule, key int) *SearchResult {
	var ok []*SearchResult
	for _, res := range results {
		if res != nil && !res.IsBatch && res.EpisodeNumber == key && r.passes(res) {
			ok = append(ok, res)
		}
	}
	return r.best(ok)
}

// pickPack picks the pack of the whole season that the rule likes best
// (not a range of episodes, nor several seasons: those would bring what
// isn't wanted).
func pickPack(results []*SearchResult, r *Rule, season int) *SearchResult {
	var ok []*SearchResult
	for _, res := range results {
		if res == nil || !res.IsBatch {
			continue
		}
		f := checkName(res.Name)
		if f.Episode >= 0 || f.SeasonFrom != season || f.SeasonTo != season || !r.passes(res) {
			continue
		}
		ok = append(ok, res)
	}
	return r.best(ok)
}

// grab sends a release to the torrent client and records key (an
// episode's, or a season's for a pack). A torrent the client already has
// (it was added by hand) counts as added, so that it's recorded.
func (a *AutoDownloader) grab(ctx context.Context, r *Rule, media *catalog.Media, files []*library.LocalFile, res *SearchResult, season, key int) error {
	magnet, err := a.torrents.Magnet(ctx, res)
	if err != nil {
		return err
	}
	save := r.Destination
	if save == "" {
		save = a.torrents.SavePath(media, season, files)
	}
	// Where this Moviko's downloads go, when it isn't here (a TV's go to
	// the computer it uses).
	sent := false
	if a.Send != nil {
		if sent, err = a.Send(ctx, media.ID, []string{magnet}); sent && err != nil {
			return err
		}
	}
	if !sent {
		if err := a.torrents.Add(ctx, []string{magnet}, save); err != nil {
			if !a.torrents.Status(ctx).Connected {
				return fmt.Errorf("%w: %v", errClientDown, err)
			}
			return err
		}
	}
	a.record(r.ID, r.MediaID, key, res.Name)
	what := catalog.FormatKey(key)
	if _, e := catalog.SplitKey(key); e == 0 {
		what = fmt.Sprintf("season %d", season)
	}
	if a.hub != nil {
		a.hub.Success(fmt.Sprintf("Auto downloader: %s %s — %s", media.PreferredTitle(), what, res.Name))
	}
	return nil
}

// Loop runs the auto downloader periodically while enabled.
func (a *AutoDownloader) Loop(ctx context.Context, enabled func() (bool, time.Duration)) {
	for {
		on, every := enabled()
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
		if !on {
			continue
		}
		rep, err := a.Run(ctx)
		if err != nil {
			log.Printf("auto downloader: %v", err)
		} else if rep.Added > 0 {
			log.Printf("auto downloader: added %d torrents", rep.Added)
		}
	}
}
