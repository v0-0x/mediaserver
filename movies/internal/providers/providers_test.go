package providers

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestQualityFromText(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"1080p", "1080p"},
		{"Torrentio\n4k", "2160p"},
		{"Torrentio\n4k DV | HDR", "2160p"},
		{"The.Matrix.1999.2160p.UHD.BluRay.x265", "2160p"},
		{"Movie.2019.720p.WEB-DL", "720p"},
		{"UHD", "2160p"},
		{"Ultra HD", "2160p"},
		{"Full HD", "1080p"},
		{"FHD", "1080p"},
		{"1920x1080", "1080p"},
		{"1920 x 800", "1080p"},
		{"1080", "1080p"},
		{"480p", "480p"},
		{"1080i", "1080p"},
		{"Show.S01E02.1080p60.WEB", "1080p"},
		{"HD", "720p"},
		{"SD", "480p"},
		{"HDRip", ""},
		{"CAM", ""},
		{"4KHDHub", ""},
		{"x264", ""},
		{"", ""},
		{"8K", "4320p"},
	} {
		if got := QualityFromText(tc.in); got != tc.want {
			t.Errorf("QualityFromText(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestQualityFromHeight(t *testing.T) {
	for _, tc := range []struct {
		h    int
		want string
	}{{2160, "2160p"}, {1080, "1080p"}, {800, "1080p"}, {720, "720p"}, {576, "576p"}, {480, "480p"}, {360, "360p"}, {240, "240p"}, {0, ""}} {
		if got := QualityFromHeight(tc.h); got != tc.want {
			t.Errorf("QualityFromHeight(%d) = %q, want %q", tc.h, got, tc.want)
		}
	}
}

func TestSizeFromText(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int64
	}{
		{"👤 12 💾 1.25 GB ⚙️ YTS", 1342177280},
		{"1.5GB", 1610612736},
		{"700 MB", 700 << 20},
		{"700MiB", 700 << 20},
		{"1,5 GB", 1610612736},
		{"1,400 MB", 1400 << 20},
		{"2 TB", 2 << 40},
		{"no size", 0},
		{"5 GBit", 0},
	} {
		if got := SizeFromText(tc.in); got != tc.want {
			t.Errorf("SizeFromText(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestSeedersFromText(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int
	}{
		{"Movie.mkv\n👤 123 💾 1 GB", 123},
		{"Seeders: 45", 45},
		{"seeds 7", 7},
		{"👥 9", 9},
		{"nothing", 0},
	} {
		if got := SeedersFromText(tc.in); got != tc.want {
			t.Errorf("SeedersFromText(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestGuessType(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"https://cdn.example/v/master.m3u8?token=1", StreamM3U8},
		{"https://cdn.example/hls/m3u8/abc", StreamM3U8},
		{"https://archive.org/download/x/Film.mp4", StreamMP4},
		{"https://example.com/a.MKV", StreamMKV},
		{"https://example.com/a.webm#t=1", StreamWebM},
		{"https://example.com/watch?v=1", StreamUnknown},
		{"Movie.2019.1080p.mkv", StreamMKV},
		{"Night of the Living Dead #1.mkv", StreamMKV},
	} {
		if got := GuessType(tc.in); got != tc.want {
			t.Errorf("GuessType(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestMagnet(t *testing.T) {
	const h = "DD8255ECDC7CA55FB0BBF81323D87062DB1F6D1C"
	m := MagnetURI(h, "Big Buck Bunny", []string{"udp://tracker.example:80/announce", ""})
	want := "magnet:?xt=urn:btih:dd8255ecdc7ca55fb0bbf81323d87062db1f6d1c&dn=Big+Buck+Bunny&tr=udp%3A%2F%2Ftracker.example%3A80%2Fannounce"
	if m != want {
		t.Fatalf("MagnetURI = %q, want %q", m, want)
	}
	if got := InfoHashFromMagnet(m); got != strings.ToLower(h) {
		t.Errorf("InfoHashFromMagnet = %q", got)
	}
	if !strings.Contains(MagnetURI(h, "", nil), "&tr=udp%3A%2F%2Ftracker.opentrackr.org") {
		t.Error("default trackers missing")
	}
	if MagnetURI("not a hash", "x", nil) != "" {
		t.Error("invalid hash accepted")
	}
	// base32 info hashes are converted to hex.
	if got := NormalizeInfoHash("3WBFL3G4PSSV7MF37AJSHWDQMLNR63I4"); got != "dd8255ecdc7ca55fb0bbf81323d87062db1f6d1c" {
		t.Errorf("base32 hash = %q", got)
	}
}

func TestRank(t *testing.T) {
	in := []Stream{
		{Title: "torrent 2160p", InfoHash: "dd8255ecdc7ca55fb0bbf81323d87062db1f6d1c", Quality: "2160p", Seeders: 500},
		{Title: "direct 720p", URL: "https://a/720.mp4", Quality: "720p"},
		{Title: "direct unknown", URL: "https://a/x.m3u8"},
		{Title: "direct 2160p", URL: "https://a/2160.mkv", Quality: "2160p", NotWebReady: true},
		{Title: "direct 1080p", URL: "https://a/1080.mkv", Quality: "1080p", NotWebReady: true},
		{Title: "direct 1080p web", URL: "https://a/1080.mp4", Quality: "1080p"},
		{Title: "youtube", YouTubeID: "abc"},
		{Title: "external", ExternalURL: "https://example.com"},
		{Title: "torrent 1080p few", InfoHash: "aa8255ecdc7ca55fb0bbf81323d87062db1f6d1c", Quality: "1080p", Seeders: 3},
		{Title: "torrent 1080p many", InfoHash: "bb8255ecdc7ca55fb0bbf81323d87062db1f6d1c", Quality: "1080p", Seeders: 300},
	}
	got := titles(Rank(in, "1080p"))
	want := []string{
		"direct 1080p web", "direct 1080p", "direct 2160p", "direct 720p", "direct unknown",
		"torrent 1080p many", "torrent 1080p few", "torrent 2160p",
		"youtube", "external",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("Rank(1080p) =\n%v\nwant\n%v", got, want)
	}
	got = titles(Rank(in, ""))
	want = []string{
		"direct 2160p", "direct 1080p web", "direct 1080p", "direct 720p", "direct unknown",
		"torrent 2160p", "torrent 1080p many", "torrent 1080p few",
		"youtube", "external",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("Rank('') =\n%v\nwant\n%v", got, want)
	}
	if in[0].Title != "torrent 2160p" {
		t.Error("Rank modified its input")
	}
	// "4K" is understood as a preference.
	if got := Rank(in, "4K"); got[0].Title != "direct 2160p" {
		t.Errorf("Rank(4K) first = %q", got[0].Title)
	}
}

func titles(ss []Stream) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.Title
	}
	return out
}

type fakeProvider struct {
	id     string
	types  []string
	delay  time.Duration
	err    error
	panics bool
	block  bool // ignores its context
	out    []Stream
	got    *Request
}

func (f *fakeProvider) ID() string   { return f.id }
func (f *fakeProvider) Name() string { return strings.ToUpper(f.id) }
func (f *fakeProvider) Kind() string { return KindBuiltin }
func (f *fakeProvider) Supports(t string) bool {
	for _, x := range f.types {
		if x == t {
			return true
		}
	}
	return false
}
func (f *fakeProvider) Streams(ctx context.Context, r Request) ([]Stream, error) {
	if f.got != nil {
		*f.got = r
	}
	if f.panics {
		panic("boom")
	}
	if f.block {
		time.Sleep(2 * time.Second)
		return f.out, nil
	}
	select {
	case <-time.After(f.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return f.out, f.err
}

func TestAggregate(t *testing.T) {
	both := []string{TypeMovie, TypeTV}
	var seen Request
	ps := []Provider{
		&fakeProvider{id: "slow", types: both, delay: time.Second, out: []Stream{{URL: "https://slow/1.mp4"}}},
		&fakeProvider{id: "a", types: both, delay: 20 * time.Millisecond, got: &seen, out: []Stream{
			{URL: "https://a/1.mp4", Title: "a1"},
			{URL: "https://a/2.m3u8", Title: "a2", Provider: "custom", ProviderName: "Custom"},
			{Title: "nothing to play"},
		}},
		&fakeProvider{id: "movies-only", types: []string{TypeMovie}, out: []Stream{{URL: "https://m/1.mp4"}}},
		&fakeProvider{id: "fails", types: both, err: errors.New("site down")},
		&fakeProvider{id: "panics", types: both, panics: true},
		&fakeProvider{id: "stuck", types: both, block: true},
		&fakeProvider{id: "b", types: both, out: []Stream{
			{URL: "https://a/1.mp4", Title: "duplicate of a1"},
			{InfoHash: "DD8255ECDC7CA55FB0BBF81323D87062DB1F6D1C", FileIdx: 2, Title: "torrent"},
		}},
	}
	req := Request{Type: TypeTV, TMDBID: 1399, Season: 1, Episode: 2}
	start := time.Now()
	streams, errs := Aggregate(context.Background(), ps, req, 200*time.Millisecond)
	if d := time.Since(start); d > time.Second {
		t.Errorf("Aggregate took %s: slow providers held it up", d)
	}
	if seen != req {
		t.Errorf("provider got %+v", seen)
	}
	got := titles(streams)
	if strings.Join(got, "|") != "a1|a2|torrent" {
		t.Errorf("streams = %v", got)
	}
	if streams[0].Provider != "a" || streams[0].ProviderName != "A" || streams[0].Type != StreamMP4 || streams[0].FileIdx != -1 {
		t.Errorf("first stream = %+v", streams[0])
	}
	if streams[1].Provider != "custom" || streams[1].Type != StreamM3U8 {
		t.Errorf("second stream = %+v", streams[1])
	}
	if streams[2].FileIdx != 2 || streams[2].Provider != "b" {
		t.Errorf("torrent stream = %+v", streams[2])
	}
	byID := map[string]string{}
	for _, e := range errs {
		byID[e.Provider] = e.Message
	}
	if len(byID) != 4 {
		t.Errorf("errors = %+v", errs)
	}
	for id, want := range map[string]string{"slow": "no answer within", "fails": "site down", "panics": "boom", "stuck": "no answer within"} {
		if !strings.Contains(byID[id], want) {
			t.Errorf("error of %s = %q, want %q", id, byID[id], want)
		}
	}
	if _, ok := byID["movies-only"]; ok {
		t.Error("a provider that doesn't support shows was asked")
	}
}

type fakeSubs struct {
	id   string
	subs []Subtitle
	err  error
}

func (f fakeSubs) ID() string { return f.id }
func (f fakeSubs) Subtitles(ctx context.Context, r Request) ([]Subtitle, error) {
	return f.subs, f.err
}

func TestAggregateSubtitles(t *testing.T) {
	subs, errs := AggregateSubtitles(context.Background(), []SubtitleProvider{
		fakeSubs{id: "one", subs: []Subtitle{{URL: "https://s/1.srt", Language: "eng"}, {URL: ""}}},
		fakeSubs{id: "two", err: errors.New("nope")},
		fakeSubs{id: "three", subs: []Subtitle{{URL: "https://s/1.srt", Language: "eng"}, {URL: "https://s/2.vtt", Language: "fre"}}},
	}, Request{Type: TypeMovie}, time.Second)
	if len(subs) != 2 || subs[1].Language != "fre" {
		t.Errorf("subs = %+v", subs)
	}
	if len(errs) != 1 || errs[0].Provider != "two" || errs[0].Name != "two" {
		t.Errorf("errs = %+v", errs)
	}
}

func TestDescribe(t *testing.T) {
	inf := Describe(&fakeProvider{id: "x", types: []string{TypeMovie}})
	if inf.ID != "x" || inf.Name != "X" || len(inf.Types) != 1 || inf.Types[0] != TypeMovie {
		t.Errorf("Describe = %+v", inf)
	}
}
