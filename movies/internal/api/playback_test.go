package api

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/library"
	"github.com/v0-0x/mediaserver/movies/internal/player"
)

// withFakeFfprobe makes s's ffprobe print the ffprobe JSON of a show's
// episode with chapters (stream/testdata).
func withFakeFfprobe(t *testing.T, s *Server) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in ffprobe is a shell script")
	}
	fixture, err := filepath.Abs(filepath.Join("..", "stream", "testdata", "ffprobe_chapters.json"))
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "ffprobe")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncat '"+fixture+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := s.app.Settings.Get()
	cfg.Transcode.FfprobePath = script
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
}

func TestPlaybackSkipsFromChapters(t *testing.T) {
	const local = "127.0.0.1:5000"
	s := newTestServer(t)
	withFakeFfprobe(t, s)
	path, _ := addEpisode(t, s, 0, 0)
	want := []player.SkipInterval{
		{Type: "recap", Start: 0, End: 52.386},
		{Type: "intro", Start: 52.386, End: 112.446},
		{Type: "credits", Start: 2731.02, End: 2790},
		{Type: "preview", Start: 2790, End: 2823.021},
	}

	var skips []player.SkipInterval
	decodeBody(t, do(s, "GET", "/api/playback/skips?path="+url.QueryEscape(path), local, "", nil), &skips)
	if !reflect.DeepEqual(skips, want) {
		t.Errorf("skips:\n got %+v\nwant %+v", skips, want)
	}
	// Streams and files outside the library have none.
	for _, q := range []string{"", "?mediaId=5&episode=1002&length=1420", "?path=/etc/passwd"} {
		var none []player.SkipInterval
		decodeBody(t, do(s, "GET", "/api/playback/skips"+q, local, "", nil), &none)
		if none == nil || len(none) != 0 {
			t.Errorf("skips%s = %+v; want an empty list", q, none)
		}
	}

	// The in-app player gets them when it opens the file, with the tracks
	// to start with: English audio (the file's default) is the user's
	// language, so only its forced subtitles.
	var play struct {
		Skips  []player.SkipInterval `json:"skips"`
		Choice struct {
			Audio int    `json:"audio"`
			Sub   string `json:"sub"`
		} `json:"choice"`
	}
	body := `{"path":` + jsonString(path) + `,"player":"builtin"}`
	decodeBody(t, do(s, "POST", "/api/playback/local", local, body, nil), &play)
	if !reflect.DeepEqual(play.Skips, want) {
		t.Errorf("skips when opening the file:\n got %+v\nwant %+v", play.Skips, want)
	}
	if play.Choice.Audio != -1 || play.Choice.Sub != "e0" {
		t.Errorf("tracks %+v; want the file's audio, the forced subtitles (e0)", play.Choice)
	}

	// French audio preferred: French is a language of the user's, no
	// subtitles.
	cfg := s.app.Settings.Get()
	cfg.Playback.PreferredAudioLang = "fre,fra,fr"
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	decodeBody(t, do(s, "POST", "/api/playback/local", local, body, nil), &play)
	if play.Choice.Audio != 1 || play.Choice.Sub != "off" {
		t.Errorf("tracks %+v; want the French audio (1), no subtitles", play.Choice)
	}
	// Subtitles whenever there are some in English (the SDH ones here).
	cfg.Playback.SubtitlesForForeign, cfg.Playback.PreferredSubLang = false, "eng,en"
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	decodeBody(t, do(s, "POST", "/api/playback/local", local, body, nil), &play)
	if play.Choice.Audio != 1 || play.Choice.Sub != "e1" {
		t.Errorf("tracks %+v; want the French audio (1) and English subtitles (e1)", play.Choice)
	}
}

// A file holding several episodes (S01E01-E03) played for one of them
// moves the list progress on to its last one.
func TestFileEnd(t *testing.T) {
	f := &library.LocalFile{MediaID: 7, Episode: 1001, EpisodeEnd: 1003}
	for _, c := range []struct{ mediaID, episode, want int }{
		{7, 1001, 1003}, {7, 1002, 1003}, {7, 1003, 0}, {7, 1004, 0}, {7, 1, 0}, {8, 1001, 0},
	} {
		if got := fileEnd(f, c.mediaID, c.episode); got != c.want {
			t.Errorf("fileEnd(%d/%d) = %d, want %d", c.mediaID, c.episode, got, c.want)
		}
	}
	if got := fileEnd(&library.LocalFile{MediaID: 7, Episode: 1001}, 7, 1001); got != 0 {
		t.Errorf("one episode: %d", got)
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// The players title episodes "Show — S01E02 · Name" and movies "Movie
// (Year)", and auto play next goes on to the next season.
func TestPlaybackTitlesAndNextEpisode(t *testing.T) {
	const local = "127.0.0.1:5000"
	s := newCatalogServer(t)
	withFakeFfprobe(t, s)
	show := catalog.ShowID(1396) // Breaking Bad: 7 episodes in season 1
	lastOfSeason1, _ := addEpisode(t, s, show, 1007)
	firstOfSeason2, _ := addEpisode(t, s, show, 2001)
	movie, _ := addEpisode(t, s, catalog.MovieID(155), catalog.MovieEpisode)

	var play struct {
		Title        string `json:"title"`
		MediaTitle   string `json:"mediaTitle"`
		EpisodeTitle string `json:"episodeTitle"`
		Episode      int    `json:"episode"`
	}
	decodeBody(t, do(s, "POST", "/api/playback/local", local, `{"path":`+jsonString(lastOfSeason1)+`,"player":"builtin"}`, nil), &play)
	if play.Title != "Breaking Bad — S01E07 · A No-Rough-Stuff-Type Deal" || play.MediaTitle != "Breaking Bad" ||
		play.EpisodeTitle != "A No-Rough-Stuff-Type Deal" || play.Episode != 1007 {
		t.Errorf("episode: %+v", play)
	}
	play.EpisodeTitle = ""
	decodeBody(t, do(s, "POST", "/api/playback/local", local, `{"path":`+jsonString(movie)+`,"player":"builtin"}`, nil), &play)
	if play.Title != "The Dark Knight (2008)" || play.EpisodeTitle != "" || play.Episode != catalog.MovieEpisode {
		t.Errorf("movie: %+v", play)
	}

	ctx := context.Background()
	next, err := s.app.Player.NextResolver(ctx, &player.Session{MediaID: show, Episode: 1007, Source: "local"})
	if err != nil || next == nil {
		t.Fatalf("next after S01E07: %+v, %v", next, err)
	}
	if next.Episode != 2001 || next.Target != firstOfSeason2 || next.Title != "Breaking Bad — S02E01" || next.MediaTitle != "Breaking Bad" {
		t.Errorf("next after S01E07: %+v; want S02E01's file", next)
	}
	cases := []struct {
		s    player.Session
		want bool
	}{
		{player.Session{MediaID: show, Episode: 1007, Source: "local"}, true},
		{player.Session{MediaID: show, Episode: 2001, Source: "local"}, false}, // no file for S02E02
		{player.Session{MediaID: show, Episode: 3, Source: "local"}, false},    // a special
		{player.Session{MediaID: catalog.MovieID(155), Episode: 1, Source: "local"}, false},
		{player.Session{MediaID: show, Episode: 1007, Source: "stream"}, false}, // no provider to ask
		{player.Session{MediaID: show, Episode: 1007, Source: "stream", Provider: "archive"}, true},
	}
	for _, c := range cases {
		if got := s.app.Player.HasNext(&c.s); got != c.want {
			t.Errorf("HasNext(%d/%d from %s %q) = %v, want %v", c.s.MediaID, c.s.Episode, c.s.Source, c.s.Provider, got, c.want)
		}
	}
	for _, c := range cases[1:5] {
		if next, err := s.app.Player.NextResolver(ctx, &c.s); next != nil || err != nil {
			t.Errorf("next after %d/%d from %s: %+v, %v; want none", c.s.MediaID, c.s.Episode, c.s.Source, next, err)
		}
	}
}
