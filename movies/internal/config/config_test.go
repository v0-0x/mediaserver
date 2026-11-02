package config

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/v0-0x/mediaserver/movies/internal/db"
)

func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "moviko.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// A first launch saves the defaults, at the current schema version.
func TestFirstLaunchSavesDefaults(t *testing.T) {
	d := openTestDB(t)
	s, err := NewStore(d)
	if err != nil {
		t.Fatal(err)
	}
	var saved Settings
	if ok, err := d.GetKV(settingsKey, &saved); !ok || err != nil {
		t.Fatalf("defaults not saved (%v)", err)
	}
	if saved.SchemaVersion != schemaVersion || s.Get().SchemaVersion != schemaVersion {
		t.Fatalf("schema version %d / %d, want %d", saved.SchemaVersion, s.Get().SchemaVersion, schemaVersion)
	}
	got := s.Get()
	if got.Playback.DefaultPlayer != "builtin" || got.OnlineStream.DefaultProvider != "" || got.Downloads.Downloader != "auto" {
		t.Fatalf("defaults: player %q, provider %q, downloader %q", got.Playback.DefaultPlayer, got.OnlineStream.DefaultProvider, got.Downloads.Downloader)
	}
	if got.TMDB.Language != "en-US" || got.TMDB.Region != "US" || got.TMDB.APIKey != "" {
		t.Fatalf("TMDB defaults: %+v", got.TMDB)
	}
	// Original audio, subtitles for foreign languages, nothing skipped.
	if p := got.Playback; p.PreferredAudioLang != "" || p.PreferredSubLang != "" || !p.SubtitlesForForeign || p.SkipIntro || p.SkipCredits {
		t.Fatalf("playback defaults: %+v", p)
	}
}

// Settings saved without some fields (by an older version, or edited by
// hand) get the defaults for them; the saved values stay.
func TestSavedSettingsKeepTheirValues(t *testing.T) {
	d := openTestDB(t)
	if err := d.SetKV(settingsKey, map[string]any{
		"schemaVersion": 1,
		"library":       map[string]any{"dir": "/mnt/big/Movies"},
		"playback":      map[string]any{"defaultPlayer": "mpv"},
		"tmdb":          map[string]any{"apiKey": "  0123456789abcdef  ", "region": "fr"},
		"letterboxd":    map[string]any{"username": "@dave/", "syncMinutes": 1},
		"downloads":     map[string]any{"dir": "/mnt/big/Downloads", "downloader": "aria2c"},
		"torrent":       map[string]any{"defaultProvider": "nyaa"}, // Kumo's
	}); err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(d)
	if err != nil {
		t.Fatal(err)
	}
	got := s.Get()
	for _, c := range []struct{ name, got, want string }{
		{"library", got.Library.Dir, "/mnt/big/Movies"},
		{"player", got.Playback.DefaultPlayer, "mpv"},
		{"api key", got.TMDB.APIKey, "0123456789abcdef"},
		{"region", got.TMDB.Region, "FR"},
		{"language", got.TMDB.Language, "en-US"},
		{"letterboxd", got.Letterboxd.Username, "dave"},
		{"download dir", got.Downloads.Dir, "/mnt/big/Downloads"},
		{"downloader", got.Downloads.Downloader, "auto"},
		{"provider", got.OnlineStream.DefaultProvider, ""}, // all of them
		{"torrent source", got.Torrent.DefaultProvider, "all"},
	} {
		if c.got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, c.got, c.want)
		}
	}
	if got.Letterboxd.SyncMinutes != Defaults().Letterboxd.SyncMinutes {
		t.Errorf("sync every %d minutes, want the default", got.Letterboxd.SyncMinutes)
	}
	// Changed afterwards, it sticks.
	got.Playback.DefaultPlayer = "builtin"
	if _, err := s.Save(got); err != nil {
		t.Fatal(err)
	}
	s2, err := NewStore(d)
	if err != nil {
		t.Fatal(err)
	}
	if s2.Get().Playback.DefaultPlayer != "builtin" {
		t.Fatalf("player lost after restart: %q", s2.Get().Playback.DefaultPlayer)
	}
}

// Get hands out copies: changing one never changes the live settings.
func TestGetReturnsCopies(t *testing.T) {
	s, err := NewStore(openTestDB(t))
	if err != nil {
		t.Fatal(err)
	}
	cur := s.Get()
	cur.TorrentSources.Torznab = []TorznabIndexer{{Name: "Prowlarr", APIKey: "secret"}}
	cur.Providers.NuvioRepos = []string{"https://example.com/manifest.json"}
	cur.Providers.MediaServers = []MediaServer{{Kind: MediaServerJellyfin, URL: "http://nas:8096", Token: "secret"}}
	if _, err := s.Save(cur); err != nil {
		t.Fatal(err)
	}
	c := s.Get()
	c.Library.IgnorePatterns[0] = "changed"
	c.UI.HomeSections[0] = "changed"
	c.Providers.StremioCatalogs[0] = "changed"
	c.Providers.NuvioRepos[0] = "changed"
	c.Providers.MediaServers[0].Token = "changed"
	c.TorrentSources.Torznab[0].APIKey = "changed"
	got := s.Get()
	if got.Library.IgnorePatterns[0] == "changed" || got.UI.HomeSections[0] == "changed" || got.Providers.StremioCatalogs[0] == "changed" ||
		got.Providers.NuvioRepos[0] == "changed" || got.Providers.MediaServers[0].Token == "changed" || got.TorrentSources.Torznab[0].APIKey == "changed" {
		t.Fatal("a copy shares its slices with the live settings")
	}
}

// Media servers are trimmed; those of an unknown kind or without an
// address are dropped.
func TestSanitizeMediaServers(t *testing.T) {
	got := sanitizeMediaServers([]MediaServer{
		{Kind: " Jellyfin ", Name: " Den ", URL: " http://nas:8096/ ", Token: " key ", UserID: " u1 "},
		{Kind: "plex", URL: "https://plex.example:32400"},
		{Kind: "kodi", URL: "http://kodi:8080"},
		{Kind: "emby", URL: "  "},
	})
	want := []MediaServer{
		{Kind: MediaServerJellyfin, Name: "Den", URL: "http://nas:8096", Token: "key", UserID: "u1"},
		{Kind: MediaServerPlex, URL: "https://plex.example:32400"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got %+v", got)
	}
	if got := sanitizeMediaServers(nil); got == nil || len(got) != 0 {
		t.Fatalf("nil: %#v", got)
	}
}

// ignoredBy is the pattern a file or folder name matches (the way the
// library scanner matches them: case-insensitive globs), "" for none.
func ignoredBy(name string, patterns []string) string {
	for _, p := range patterns {
		if ok, _ := filepath.Match(strings.ToLower(p), strings.ToLower(name)); ok {
			return p
		}
	}
	return ""
}

// The default ignore patterns skip samples, unfinished downloads and the
// folders NAS boxes and Windows keep deleted files and thumbnails in, never
// a movie or an episode.
func TestDefaultIgnorePatterns(t *testing.T) {
	patterns := Defaults().Library.IgnorePatterns
	for _, name := range []string{"Sample", "inception.2010.1080p-sample.mkv", "Movie.2010.mkv.part", "#recycle", "@eaDir", "$RECYCLE.BIN"} {
		if ignoredBy(name, patterns) == "" {
			t.Errorf("%q isn't ignored", name)
		}
	}
	for _, name := range []string{
		"Silenced (2011).mkv", "Sherlock.Holmes.2009.Enhanced.Edition.1080p.mkv", "Unbalanced.2020.mkv",
		"The Advanced Guide (2015)", "Uncopyrighted.S01E01.mkv", "Trailer.mkv", "Season 01", "Extras",
	} {
		if p := ignoredBy(name, patterns); p != "" {
			t.Errorf("%q is ignored by %q", name, p)
		}
	}
}

// Settings saved by an earlier build lose Kumo's anime ignore patterns
// ("*NCED*" hid "Silenced" and "Enhanced" editions) and get the new ones;
// the user's own patterns stay.
func TestMigrateIgnorePatterns(t *testing.T) {
	d := openTestDB(t)
	if err := d.SetKV(settingsKey, map[string]any{
		"schemaVersion": 1,
		"library":       map[string]any{"ignorePatterns": []string{"*sample*", "*.part", "*NCOP*", "*NCED*", "*.iso"}},
	}); err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(d)
	if err != nil {
		t.Fatal(err)
	}
	got := s.Get().Library.IgnorePatterns
	want := []string{"*sample*", "*.part", "*.iso", "@eaDir", "#recycle", "$RECYCLE.BIN"}
	if !slices.Equal(got, want) {
		t.Fatalf("patterns %q, want %q", got, want)
	}
	var saved Settings
	if ok, _ := d.GetKV(settingsKey, &saved); !ok || saved.SchemaVersion != schemaVersion || !slices.Equal(saved.Library.IgnorePatterns, want) {
		t.Fatalf("saved: version %d, patterns %q", saved.SchemaVersion, saved.Library.IgnorePatterns)
	}
	// Removed by hand afterwards: they stay removed.
	cur := s.Get()
	cur.Library.IgnorePatterns = []string{"*.part"}
	if _, err := s.Save(cur); err != nil {
		t.Fatal(err)
	}
	s2, err := NewStore(d)
	if err != nil {
		t.Fatal(err)
	}
	if got := s2.Get().Library.IgnorePatterns; !slices.Equal(got, []string{"*.part"}) {
		t.Fatalf("after a restart: %q", got)
	}
}

func TestThemeSettings(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "moviko.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	// Saved before themes: the defaults (holiday themes on, by month).
	if err := d.SetKV(settingsKey, map[string]any{"ui": map[string]any{"accentColor": "#22c3a6"}}); err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(d)
	if err != nil {
		t.Fatal(err)
	}
	ui := s.Get().UI
	if ui.ColorTheme != "kumobox" || ui.Seasonal != "auto" || !ui.SeasonalEffects || ui.AccentColor != "#22c3a6" || ui.ClassicLook {
		t.Fatalf("old settings: %+v", ui)
	}
	// The classic look is kept.
	next := s.Get()
	next.UI.ClassicLook = true
	if got, err := s.Save(next); err != nil || !got.UI.ClassicLook || !s.Get().UI.ClassicLook {
		t.Fatalf("classic look: %+v, %v", got.UI, err)
	}
	for _, c := range []struct{ theme, seasonal, wantTheme, wantSeasonal string }{
		{"midnight", "off", "midnight", "off"},
		{"black", "christmas", "black", "christmas"},
		{"nope", "easter", "kumobox", "auto"},
		{"", "", "kumobox", "auto"},
	} {
		next := s.Get()
		next.UI.ColorTheme, next.UI.Seasonal, next.UI.SeasonalEffects = c.theme, c.seasonal, false
		got, err := s.Save(next)
		if err != nil {
			t.Fatal(err)
		}
		if got.UI.ColorTheme != c.wantTheme || got.UI.Seasonal != c.wantSeasonal || got.UI.SeasonalEffects {
			t.Fatalf("%q/%q saved as %q/%q effects=%v", c.theme, c.seasonal, got.UI.ColorTheme, got.UI.Seasonal, got.UI.SeasonalEffects)
		}
	}
}

// Pinned settings stay as the pin makes them, whatever is saved.
func TestPin(t *testing.T) {
	s, err := NewStore(openTestDB(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Pin(func(c *Settings) { c.Sharing.Enabled, c.Server.AllowLAN = true, true }); err != nil {
		t.Fatal(err)
	}
	if cur := s.Get(); !cur.Sharing.Enabled || !cur.Server.AllowLAN {
		t.Fatal("not pinned at once")
	}
	next := s.Get()
	next.Sharing.Enabled, next.Server.AllowLAN, next.Sharing.Name = false, false, "Pi"
	saved, err := s.Save(next)
	if err != nil {
		t.Fatal(err)
	}
	if !saved.Sharing.Enabled || !saved.Server.AllowLAN || saved.Sharing.Name != "Pi" {
		t.Fatalf("saved %+v %+v", saved.Sharing, saved.Server)
	}
}
