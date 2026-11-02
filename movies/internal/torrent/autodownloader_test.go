package torrent

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/config"
	"github.com/v0-0x/mediaserver/movies/internal/db"
	"github.com/v0-0x/mediaserver/movies/internal/events"
	"github.com/v0-0x/mediaserver/movies/internal/library"
)

// fakeClient is a torrent client that remembers what it's given.
type fakeClient struct {
	mu   sync.Mutex
	down bool
	fail string // Add fails for URIs containing it
	adds []fakeAdd
}

type fakeAdd struct {
	uri, savePath string
}

func (c *fakeClient) Name() string { return "fake" }
func (c *fakeClient) Version(context.Context) (string, error) {
	if c.down {
		return "", errors.New("connection refused")
	}
	return "1.0", nil
}
func (c *fakeClient) List(context.Context) ([]Torrent, error) { return []Torrent{}, nil }
func (c *fakeClient) Add(_ context.Context, uris []string, savePath string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.down {
		return errors.New("connection refused")
	}
	for _, u := range uris {
		if c.fail != "" && strings.Contains(u, c.fail) {
			return errors.New("torrent file is not valid")
		}
		c.adds = append(c.adds, fakeAdd{u, savePath})
	}
	return nil
}
func (c *fakeClient) Pause(context.Context, []string) error        { return nil }
func (c *fakeClient) Resume(context.Context, []string) error       { return nil }
func (c *fakeClient) Remove(context.Context, []string, bool) error { return nil }

func (c *fakeClient) added() []fakeAdd {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]fakeAdd(nil), c.adds...)
}

// fakeCatalog holds shows, the list's progress and season details.
type fakeCatalog struct {
	media    map[int]*catalog.Media
	progress map[int]int
	seasons  map[int]*catalog.SeasonDetail // by season number
	listErr  error
}

func (c *fakeCatalog) MediaLite(_ context.Context, id int) (*catalog.Media, error) {
	if m := c.media[id]; m != nil {
		return m, nil
	}
	return nil, catalog.ErrNotFound
}

func (c *fakeCatalog) Collection(context.Context, string, bool) (*catalog.Collection, error) {
	if c.listErr != nil {
		return nil, c.listErr
	}
	l := &catalog.List{Status: "CURRENT"}
	for id, p := range c.progress {
		l.Entries = append(l.Entries, &catalog.ListEntry{MediaID: id, Status: "CURRENT", Progress: p, Media: c.media[id]})
	}
	return &catalog.Collection{Lists: []*catalog.List{l}}, nil
}

func (c *fakeCatalog) Season(_ context.Context, _, season int) (*catalog.SeasonDetail, error) {
	if d := c.seasons[season]; d != nil {
		return d, nil
	}
	return nil, catalog.ErrNotFound
}

type fakeFiles map[int][]*library.LocalFile

func (f fakeFiles) ByMedia(id int) ([]*library.LocalFile, error) { return f[id], nil }

// querySource is a show source that answers each query with answer.
type querySource struct {
	id     string
	answer func(q MediaQuery) []*SearchResult
	mu     sync.Mutex
	asked  []MediaQuery
}

func (s *querySource) ID() string             { return s.id }
func (s *querySource) Name() string           { return strings.ToUpper(s.id) }
func (s *querySource) Supports(t string) bool { return t == MediaTV }
func (s *querySource) Search(context.Context, string) ([]*SearchResult, error) {
	return nil, ErrUnsupported
}
func (s *querySource) SearchMedia(_ context.Context, q MediaQuery) ([]*SearchResult, error) {
	s.mu.Lock()
	s.asked = append(s.asked, q)
	s.mu.Unlock()
	return s.answer(q), nil
}
func (s *querySource) Magnet(_ context.Context, r *SearchResult) (string, error) {
	return "magnet:?xt=urn:btih:" + r.InfoHash, nil
}

func (s *querySource) queries() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, q := range s.asked {
		out = append(out, q.episodeTag())
	}
	return out
}

// autoTest is an auto downloader with its own database, library folder,
// torrent client, catalog and library.
type autoTest struct {
	*AutoDownloader
	client *fakeClient
	cat    *fakeCatalog
	files  fakeFiles
	lib    string
}

var autoNow = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

func newAutoTest(t *testing.T, sources ...Source) *autoTest {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "moviko.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	settings, err := config.NewStore(d)
	if err != nil {
		t.Fatal(err)
	}
	lib := t.TempDir()
	s := settings.Get()
	s.Library.Dir = lib
	s.Torrent.CreateSubfolder = true
	s.Torrent.DefaultClient = "qbittorrent"
	s.Qbittorrent.Executable = "" // nothing is ever launched
	if _, err := settings.Save(s); err != nil {
		t.Fatal(err)
	}
	hub := events.NewHub()
	m := NewManager(settings, hub)
	at := &autoTest{client: &fakeClient{}, cat: &fakeCatalog{media: map[int]*catalog.Media{}, progress: map[int]int{}}, files: fakeFiles{}, lib: lib}
	m.UseClient(at.client)
	m.UseSources(sources...)
	at.AutoDownloader = &AutoDownloader{db: d, torrents: m, catalog: at.cat, files: at.files, hub: hub, now: func() time.Time { return autoNow }}
	return at
}

func (at *autoTest) saveRule(t *testing.T, r Rule) *Rule {
	t.Helper()
	saved, err := at.SaveRule(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

func (at *autoTest) itemKeys() []string {
	var out []string
	for _, it := range at.Items() {
		out = append(out, it.Key)
	}
	return out
}

// severance is a show whose first season (9 episodes) is over and whose
// second (10) is airing: S02E03 aired last.
func severance() *catalog.Media {
	y := 2022
	return &catalog.Media{
		ID: catalog.ShowID(95396), TMDBID: 95396, IMDbID: "tt11280740", Type: catalog.TypeTV, Status: "RELEASING",
		Title: catalog.Title{English: "Severance", UserPreferred: "Severance"}, StartDate: catalog.FuzzyDate{Year: &y},
		Seasons:       []catalog.Season{{Number: 0, EpisodeCount: 2}, {Number: 1, EpisodeCount: 9}, {Number: 2, EpisodeCount: 10}},
		LatestEpisode: 2003,
	}
}

// hashOf makes an info hash for a name.
func hashOf(name string) string {
	sum := sha1.Sum([]byte(name))
	return hex.EncodeToString(sum[:])
}

func rel(name string, seeders int) *SearchResult {
	return &SearchResult{Name: name, InfoHash: hashOf(name), Seeders: seeders}
}

// severanceSource answers like the sites would.
func severanceSource() *querySource {
	return &querySource{id: "src", answer: func(q MediaQuery) []*SearchResult {
		switch q.episodeTag() {
		case "S01":
			return []*SearchResult{
				rel("Severance.S01.1080p.ATVP.WEB-DL.DDP5.1.H.264-NTb", 80),
				rel("Severance.S01.2160p.ATVP.WEB-DL.DDP5.1.HDR.H.265-FLUX", 200), // resolution
				rel("Severance.S01-S02.1080p.WEB-DL-GRP", 300),                    // two seasons
				rel("Severance.S01E01-E05.1080p.WEB-DL-GRP", 500),                 // a range
			}
		case "S02E01":
			return []*SearchResult{
				rel("Severance.S02E01.1080p.WEB.H264-GRP", 500),
				rel("Severance.S02E01.1080p.ATVP.WEB-DL.DDP5.1.H.264-NTb", 20), // preferred group
				rel("Severance.S02E01.720p.WEB.H264-GRP", 999),
				rel("Severance.S02E02.1080p.WEB.H264-GRP", 999), // another episode
			}
		case "S02E02":
			return []*SearchResult{
				rel("Severance.S02E02.720p.WEB.H264-GRP", 100),
				rel("Severance.S02E02.1080p.HDTV.x264-GRP", 50), // must not contain HDTV
			}
		case "S02E03":
			return []*SearchResult{
				rel("Severance.S02E03.1080p.WEB.H264-GRP", 0), // dead
				rel("Severance S02E03 1080p WEB-DL x265-GRP2", 30),
			}
		}
		return nil
	}}
}

func TestAutoDownloaderRun(t *testing.T) {
	src := severanceSource()
	at := newAutoTest(t, src)
	show := severance()
	at.cat.media[show.ID] = show
	at.cat.progress[show.ID] = 1003 // watched up to S01E03
	at.files[show.ID] = []*library.LocalFile{{Path: "/elsewhere/Severance.S01E04.mkv", Dir: "/elsewhere", MediaID: show.ID, Episode: 1004, Kind: "main"}}
	at.record(1, show.ID, 1005, "Severance.S01E05.720p")
	rule := at.saveRule(t, Rule{MediaID: show.ID, Enabled: true, Resolution: "1080p", ReleaseGroups: []string{"ntb"}, MustNotContain: []string{"hdtv"}, Packs: true})
	if rule.Title != "Severance" || rule.Resolution != "1080" {
		t.Fatalf("saved rule: %+v", rule)
	}
	at.saveRule(t, Rule{MediaID: catalog.ShowID(1), Enabled: false}) // disabled: never looked at

	if keys, err := at.Wanted(context.Background(), rule); err != nil || !reflect.DeepEqual(keys, []int{1006, 1007, 1008, 1009, 2001, 2002, 2003}) {
		t.Fatalf("wanted %v %v", keys, err)
	}
	rep, err := at.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Added != 3 || rep.Wanted != 7 || len(rep.Errors) != 0 {
		t.Fatalf("report %+v", rep)
	}
	// The latest season first.
	if got := src.queries(); !reflect.DeepEqual(got, []string{"S02E01", "S02E02", "S02E03", "S01"}) {
		t.Fatalf("searched %v", got)
	}
	folder := filepath.Join(at.lib, "Severance (2022)")
	want := []fakeAdd{
		{"magnet:?xt=urn:btih:" + hashOf("Severance.S02E01.1080p.ATVP.WEB-DL.DDP5.1.H.264-NTb"), filepath.Join(folder, "Season 02")},
		{"magnet:?xt=urn:btih:" + hashOf("Severance S02E03 1080p WEB-DL x265-GRP2"), filepath.Join(folder, "Season 02")},
		{"magnet:?xt=urn:btih:" + hashOf("Severance.S01.1080p.ATVP.WEB-DL.DDP5.1.H.264-NTb"), filepath.Join(folder, "Season 01")},
	}
	if got := at.client.added(); !reflect.DeepEqual(got, want) {
		t.Fatalf("added\n%v\nwant\n%v", got, want)
	}
	items := at.Items()
	if len(items) != 4 {
		t.Fatalf("items %+v", items)
	}
	byKey := map[string]AutoItem{}
	for _, it := range items {
		byKey[it.Key] = it
	}
	pack := byKey[itemKey(show.ID, 1000)]
	if !pack.Pack || pack.Episode != 1000 || pack.MediaID != show.ID || pack.RuleID != rule.ID || pack.CreatedAt != autoNow.Unix() || !strings.Contains(pack.Title, "NTb") {
		t.Fatalf("pack item %+v", pack)
	}
	if ep := byKey[itemKey(show.ID, 2003)]; ep.Pack || ep.Episode != 2003 {
		t.Fatalf("episode item %+v", ep)
	}

	// Next time only S02E02 is still wanted, and still not found.
	rep, err = at.Run(context.Background())
	if err != nil || rep.Added != 0 || rep.Wanted != 1 {
		t.Fatalf("second run: %+v %v", rep, err)
	}
	if got := src.queries(); len(got) != 5 || got[4] != "S02E02" {
		t.Fatalf("searched %v", got)
	}

	// Forgetting the pack makes season 1 wanted again.
	if err := at.ForgetItem(itemKey(show.ID, 1000)); err != nil {
		t.Fatal(err)
	}
	if keys, _ := at.Wanted(context.Background(), rule); len(keys) != 5 || keys[0] != 1006 {
		t.Fatalf("after forgetting: %v", keys)
	}
}

// Without packs every episode is searched, at most maxSearches per run.
func TestAutoDownloaderSearchBudget(t *testing.T) {
	src := &querySource{id: "src", answer: func(MediaQuery) []*SearchResult { return nil }}
	at := newAutoTest(t, src)
	show := severance()
	show.Status, show.LatestEpisode = "FINISHED", 0 // everything aired
	at.cat.media[show.ID] = show
	at.saveRule(t, Rule{MediaID: show.ID, Enabled: true})
	rep, err := at.Run(context.Background())
	if err != nil || rep.Wanted != 19 || rep.Added != 0 {
		t.Fatalf("%+v %v", rep, err)
	}
	if got := src.queries(); len(got) != maxSearches || got[0] != "S02E01" {
		t.Fatalf("searched %v", got)
	}
	// Not in the list: nothing was watched, everything is wanted.
	if keys, _ := at.Wanted(context.Background(), &Rule{MediaID: show.ID, StartFrom: 2009}); !reflect.DeepEqual(keys, []int{2009, 2010}) {
		t.Fatalf("start from S02E09: %v", keys)
	}
}

// Earlier episodes that are never found don't keep the latest season from
// being looked for, run after run.
func TestAutoDownloaderLatestSeasonFirst(t *testing.T) {
	src := &querySource{id: "src", answer: func(q MediaQuery) []*SearchResult {
		if q.episodeTag() == "S02E03" {
			return []*SearchResult{rel("Severance.S02E03.1080p.WEB.H264-GRP", 50)}
		}
		return nil // season 1 isn't on the sources
	}}
	at := newAutoTest(t, src)
	show := severance()
	at.cat.media[show.ID] = show
	at.saveRule(t, Rule{MediaID: show.ID, Enabled: true, Resolution: "1080p"})
	for run := 1; run <= 2; run++ {
		rep, err := at.Run(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if want := 2 - run; rep.Added != want {
			t.Fatalf("run %d: %+v, want %d added", run, rep, want)
		}
	}
	if keys := at.itemKeys(); len(keys) != 1 || keys[0] != itemKey(show.ID, 2003) {
		t.Fatalf("items %v", keys)
	}
}

// A torrent the client refuses is skipped; when the client is down the run
// stops there.
func TestAutoDownloaderClientErrors(t *testing.T) {
	src := severanceSource()
	at := newAutoTest(t, src)
	show := severance()
	at.cat.media[show.ID] = show
	at.cat.progress[show.ID] = 1009
	at.client.fail = hashOf("Severance.S02E01.1080p.ATVP.WEB-DL.DDP5.1.H.264-NTb")
	at.saveRule(t, Rule{MediaID: show.ID, Enabled: true, Resolution: "1080", ReleaseGroups: []string{"NTb"}})
	rep, err := at.Run(context.Background())
	if err != nil || rep.Added != 2 || len(rep.Errors) != 1 || !strings.Contains(rep.Errors[0], "not valid") {
		t.Fatalf("refused torrent: %+v %v", rep, err)
	}
	if got := at.itemKeys(); len(got) != 2 {
		t.Fatalf("items %v", got)
	}

	at2 := newAutoTest(t, severanceSource())
	at2.cat.media[show.ID] = show
	at2.cat.progress[show.ID] = 1009
	at2.client.down = true
	other := severance()
	other.ID = catalog.ShowID(7)
	at2.cat.media[other.ID] = other
	at2.saveRule(t, Rule{MediaID: show.ID, Enabled: true, Resolution: "1080"})
	at2.saveRule(t, Rule{MediaID: other.ID, Enabled: true, Resolution: "1080"})
	rep, err = at2.Run(context.Background())
	if err != nil || rep.Added != 0 || len(rep.Errors) != 1 || !strings.Contains(rep.Errors[0], "torrent client") {
		t.Fatalf("client down: %+v %v", rep, err)
	}
	if got := at2.itemKeys(); len(got) != 0 {
		t.Fatalf("recorded %v", got)
	}
	if src2 := at2.torrents.Sources()[0].(*querySource); len(src2.queries()) != 1 {
		t.Fatalf("kept searching with the client down: %v", src2.queries())
	}
}

// Unknown progress would make every missing episode look new.
func TestAutoDownloaderNeedsList(t *testing.T) {
	src := severanceSource()
	at := newAutoTest(t, src)
	at.cat.media[severance().ID] = severance()
	at.cat.listErr = errors.New("database is locked")
	at.saveRule(t, Rule{MediaID: severance().ID, Enabled: true})
	rep, err := at.Run(context.Background())
	if err != nil || len(rep.Errors) != 1 || len(src.queries()) != 0 {
		t.Fatalf("%+v %v, searched %v", rep, err, src.queries())
	}
}

func TestWantedEpisodes(t *testing.T) {
	show := severance()
	none := map[int]bool{}
	for _, tc := range []struct {
		name            string
		progress, start int
		limit           int
		have, grabbed   map[int]bool
		want            []int
	}{
		{"after progress", 1007, 0, 2002, none, none, []int{1008, 1009, 2001, 2002}},
		{"start later", 1007, 2002, 2003, none, none, []int{2002, 2003}},
		{"start earlier than progress", 2001, 1001, 2003, none, none, []int{2002, 2003}},
		{"in the library or grabbed", 1007, 0, 2003, map[int]bool{1008: true}, map[int]bool{2002: true}, []int{1009, 2001, 2003}},
		{"season pack grabbed", 1007, 0, 2003, none, map[int]bool{1000: true}, []int{2001, 2002, 2003}},
		{"nothing aired", 0, 0, 0, none, none, nil},
		{"a season not listed yet", 2009, 0, 3002, none, none, []int{2010, 3001, 3002}},
	} {
		if got := wantedEpisodes(show, tc.progress, tc.start, tc.limit, tc.have, tc.grabbed); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestAiredLimit(t *testing.T) {
	at := newAutoTest(t)
	show := severance()
	show.LatestEpisode = 0
	show.Seasons[2].AirDate = "2026-09-01"
	at.cat.seasons = map[int]*catalog.SeasonDetail{2: {Number: 2, Episodes: []*catalog.Episode{
		{Number: 1, AirDate: "2026-09-01"}, {Number: 2, AirDate: "2026-09-08"}, {Number: 3, AirDate: "2026-10-20"}, {Number: 4},
	}}}
	if got := at.airedLimit(context.Background(), show); got != 2002 {
		t.Fatalf("from air dates: %d", got)
	}
	show.LatestEpisode = 2001
	if got := at.airedLimit(context.Background(), show); got != 2001 {
		t.Fatalf("from the show: %d", got)
	}
	show.LatestEpisode, show.Status = 0, "NOT_YET_RELEASED"
	if got := at.airedLimit(context.Background(), show); got != 0 {
		t.Fatalf("not released: %d", got)
	}
}

func TestEpisodeLimit(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	seasons := []catalog.Season{{Number: 1, EpisodeCount: 12}, {Number: 2, EpisodeCount: 10}}
	airing := func(latest int, next *catalog.AiringEpisode) catalog.Media {
		return catalog.Media{ID: catalog.ShowID(1), Type: catalog.TypeTV, Status: "RELEASING", Seasons: seasons, LatestEpisode: latest, NextAiringEpisode: next}
	}
	for _, tc := range []struct {
		media catalog.Media
		want  int
	}{
		{catalog.Media{ID: catalog.ShowID(1), Type: catalog.TypeTV, Status: "FINISHED", Seasons: seasons}, 2010},
		{catalog.Media{ID: catalog.ShowID(1), Type: catalog.TypeTV, Status: "RELEASING"}, 0},
		{airing(2005, &catalog.AiringEpisode{Episode: 2006, AiringAt: int(now.Unix()) + 3600}), 2005},
		// Cached before S02E06 aired: it is out now.
		{airing(2005, &catalog.AiringEpisode{Episode: 2006, AiringAt: int(now.Unix()) - 3600}), 2006},
		{airing(2005, &catalog.AiringEpisode{Episode: 2006}), 2005},
		{airing(1012, &catalog.AiringEpisode{Episode: 2001, AiringAt: int(now.Unix()) - 3600}), 2001},
	} {
		if got := episodeLimit(&tc.media, now); got != tc.want {
			t.Errorf("%+v: limit %d, want %d", tc.media, got, tc.want)
		}
	}
}

func TestRuleFilters(t *testing.T) {
	r := &Rule{Resolution: "1080", MustContain: []string{"web-dl"}, MustNotContain: []string{"x265"}, MinSeeders: 5, ReleaseGroups: []string{"NTb", "FLUX"}}
	for _, tc := range []struct {
		name    string
		seeders int
		want    bool
	}{
		{"Show.S01E01.1080p.WEB-DL.H.264-NTb", 5, true},
		{"Show S01E01 1080p WEB DL H 264 NTb", 5, true}, // separators don't matter
		{"Show.S01E01.1080p.WEB-DL.H.264-NTb", 4, false},
		{"Show.S01E01.720p.WEB-DL.H.264-NTb", 50, false},
		{"Show.S01E01.1080p.WEBRip.H.264-NTb", 50, false},
		{"Show.S01E01.1080p.WEB-DL.x265-NTb", 50, false},
	} {
		res := &SearchResult{Name: tc.name, Seeders: tc.seeders}
		annotate(res)
		if got := r.passes(res); got != tc.want {
			t.Errorf("%s (%d seeders): %v", tc.name, tc.seeders, got)
		}
	}
	if (&Rule{}).passes(&SearchResult{Name: "x", Seeders: 0}) {
		t.Error("a dead torrent passed")
	}
	best := r.best([]*SearchResult{
		{Name: "a", ReleaseGroup: "GRP", Seeders: 900},
		{Name: "b", ReleaseGroup: "flux", Seeders: 10},
		{Name: "c", ReleaseGroup: "FLUX", Seeders: 20},
	})
	if best.Name != "c" {
		t.Fatalf("best %s", best.Name)
	}
}

func TestRules(t *testing.T) {
	at := newAutoTest(t)
	ctx := context.Background()
	if _, err := at.SaveRule(ctx, Rule{MediaID: 155}); err == nil {
		t.Fatal("saved a rule for a movie")
	}
	r := at.saveRule(t, Rule{MediaID: catalog.ShowID(5), Title: " A show ", Resolution: "4K", ReleaseGroups: []string{" NTb ", "", "ntb", "FLUX"}, MinSeeders: -3, MustContain: nil})
	if r.ID == 0 || r.Title != "A show" || r.Resolution != "2160" || !reflect.DeepEqual(r.ReleaseGroups, []string{"NTb", "FLUX"}) || r.MinSeeders != 0 || r.MustContain == nil {
		t.Fatalf("saved %+v", r)
	}
	r.Enabled = true
	at.saveRule(t, *r)
	if got, err := at.Rule(r.ID); err != nil || got == nil || !got.Enabled {
		t.Fatalf("rule %+v %v", got, err)
	}
	if _, err := at.SaveRule(ctx, Rule{ID: 999, MediaID: catalog.ShowID(5)}); err == nil {
		t.Fatal("updated a rule that doesn't exist")
	}
	if err := at.DeleteRule(r.ID); err != nil {
		t.Fatal(err)
	}
	if rules, _ := at.Rules(); len(rules) != 0 {
		t.Fatalf("rules %+v", rules)
	}
}

// Where this Moviko's downloads go elsewhere (a TV's computer), the releases
// go there, not to a torrent client here (there's none), and are recorded;
// when that Moviko can't take them, nothing is.
func TestAutoDownloaderSendsElsewhere(t *testing.T) {
	show := severance()
	run := func(send func(context.Context, int, []string) (bool, error)) (*autoTest, *RunReport) {
		at := newAutoTest(t, severanceSource())
		at.cat.media[show.ID] = show
		at.cat.progress[show.ID] = 1009
		at.client.down = true // no torrent client here
		at.Send = send
		at.saveRule(t, Rule{MediaID: show.ID, Enabled: true, Resolution: "1080", ReleaseGroups: []string{"NTb"}})
		rep, err := at.Run(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return at, rep
	}
	var sent []string
	at, rep := run(func(_ context.Context, mediaID int, uris []string) (bool, error) {
		if mediaID != show.ID {
			t.Errorf("sent for %d", mediaID)
		}
		sent = append(sent, uris...)
		return true, nil
	})
	if rep.Added == 0 || len(sent) != rep.Added || len(rep.Errors) != 0 {
		t.Fatalf("sent %v: %+v", sent, rep)
	}
	if got := at.itemKeys(); len(got) != rep.Added {
		t.Errorf("recorded %v", got)
	}
	at, rep = run(func(context.Context, int, []string) (bool, error) { return true, io.ErrUnexpectedEOF })
	if rep.Added != 0 || len(rep.Errors) == 0 || len(at.itemKeys()) != 0 {
		t.Fatalf("refused: %+v %v", rep, at.itemKeys())
	}
	// Downloads here again: the client here (down) is used.
	_, rep = run(func(context.Context, int, []string) (bool, error) { return false, nil })
	if rep.Added != 0 || len(rep.Errors) != 1 || !strings.Contains(rep.Errors[0], "torrent client") {
		t.Fatalf("here: %+v", rep)
	}
}
