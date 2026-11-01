package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/app"
	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/config"
	"github.com/v0-0x/mediaserver/movies/internal/downloads"
	"github.com/v0-0x/mediaserver/movies/internal/player"
	"github.com/v0-0x/mediaserver/movies/internal/providers/cinejoy"
	"github.com/v0-0x/mediaserver/movies/internal/stream"
)

const testInfoHash = "0123456789abcdef0123456789abcdef01234567"

// testVideoHash is the OpenSubtitles hash of the fake addon's video.mkv
// (327680 bytes of "KumoBox!").
var testVideoHash = func() string {
	word := uint64(0)
	for i, c := range []byte("KumoBox!") {
		word |= uint64(c) << (8 * i)
	}
	h := strconv.FormatUint(327680+2*8192*word, 16)
	return strings.Repeat("0", 16-len(h)) + h
}()

// fakeAddon is a Stremio addon on this computer (so it needs the local
// confirmation): streams for The Dark Knight and Breaking Bad S01E02,
// English subtitles for the movie, and, next to it, an adult addon and a
// catalog-only one.
type fakeAddon struct {
	*httptest.Server
	mu    sync.Mutex
	paths []string
}

func newFakeAddon(t *testing.T) *fakeAddon {
	t.Helper()
	f := &fakeAddon{}
	manifest := func(id, name string, extra string) string {
		return `{"id":"` + id + `","version":"1.0.0","name":"` + name + `","description":"Streams for tests","logo":"https://addon.example/logo.png",
			"types":["movie","series"],"idPrefixes":["tt"],"catalogs":[]` + extra + `}`
	}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.paths = append(f.paths, r.URL.Path)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/manifest.json":
			_, _ = io.WriteString(w, manifest("test.addon", "Test Addon", `,"resources":["stream","subtitles"]`))
		case "/adult/manifest.json":
			_, _ = io.WriteString(w, manifest("test.adult", "Adult Addon", `,"resources":["stream"],"behaviorHints":{"adult":true}`))
		case "/meta/manifest.json":
			_, _ = io.WriteString(w, manifest("test.meta", "Catalog Only", `,"resources":["catalog","meta"]`))
		case "/stream/movie/tt0468569.json":
			_, _ = io.WriteString(w, `{"streams":[
				{"name":"Test\n4k","title":"The.Dark.Knight.2008.2160p.mkv\n👤 120 💾 30 GB","infoHash":"`+testInfoHash+`","fileIdx":0},
				{"name":"Test\n1080p","title":"The.Dark.Knight.2008.1080p.mp4\n💾 2.1 GB","url":"https://cdn.example/dk.1080p.mp4",
				 "behaviorHints":{"proxyHeaders":{"request":{"Referer":"https://cdn.example/"}}},
				 "subtitles":[{"id":"1","url":"https://cdn.example/dk.fr.srt","lang":"fre"}]},
				{"name":"Test","title":"Trailer","ytId":"EXeTwQWrcwY"}]}`)
		case "/subtitles/movie/tt0468569.json":
			_, _ = io.WriteString(w, `{"subtitles":[{"id":"2","url":"https://subs.example/dk.en.srt","lang":"eng"}]}`)
		case "/video.mkv":
			// A video file the subtitles are matched with (by its hash).
			http.ServeContent(w, r, "video.mkv", time.Time{}, strings.NewReader(strings.Repeat("KumoBox!", 40<<10)))
		case "/subtitles/movie/tt0468569/filename=The.Dark.Knight.mkv&videoHash=" + testVideoHash + "&videoSize=327680.json":
			_, _ = io.WriteString(w, `{"subtitles":[{"id":"3","url":"https://subs.example/dk.fr.matched.srt","lang":"fre"},
				{"id":"4","url":"https://subs.example/dk.en.matched.srt","lang":"eng","label":"English (this release)"}]}`)
		case "/stream/series/tt0903747:1:2.json":
			_, _ = io.WriteString(w, `{"streams":[{"name":"Test 720p","title":"Breaking.Bad.S01E02.720p","url":"https://cdn.example/bb.s01e02.m3u8"}]}`)
		default:
			_, _ = io.WriteString(w, `{"streams":[],"subtitles":[]}`)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeAddon) asked(path string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.paths {
		if p == path {
			return true
		}
	}
	return false
}

// streamSites are the built-in streaming sites, listed (off by default)
// after the Internet Archive.
var streamSites = []string{"cinejoy", "vidlink", "hexa", "xpass", "mapple", "videasy"}

// newProvidersServer is a catalog test server (fake TMDB) without the
// Internet Archive (no network in tests), open to the LAN.
func newProvidersServer(t *testing.T) *Server {
	t.Helper()
	s := newCatalogServer(t)
	cfg := s.app.Settings.Get()
	cfg.Providers.Archive = false
	cfg.Server.AllowLAN = true
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	return s
}

// local and lan (the clients' addresses) are letterboxd_test.go's.

func jsonBody(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestStremioAddonInstall(t *testing.T) {
	s := newProvidersServer(t)
	addon := newFakeAddon(t)
	install := func(remote string, body map[string]any) *httptest.ResponseRecorder {
		return do(s, "POST", "/api/providers/stremio/addons", remote, jsonBody(t, body), nil)
	}
	code := func(w *httptest.ResponseRecorder) string {
		var out struct {
			Error, Code, ConfigureURL string
		}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return out.Code
	}

	// On this computer: asks first.
	if w := install(local, map[string]any{"url": addon.URL + "/manifest.json"}); w.Code != http.StatusConflict || code(w) != "local" {
		t.Fatalf("local addon: %d %s", w.Code, w.Body.String())
	}
	// Devices on the LAN don't install providers.
	if w := install(lan, map[string]any{"url": addon.URL, "allowLocal": true}); w.Code != http.StatusForbidden {
		t.Fatalf("from the LAN: %d %s", w.Code, w.Body.String())
	}
	if w := install(local, map[string]any{"url": addon.URL + "/adult/manifest.json", "allowLocal": true}); w.Code != http.StatusConflict || code(w) != "adult" {
		t.Fatalf("adult addon: %d %s", w.Code, w.Body.String())
	}
	if w := install(local, map[string]any{"url": addon.URL + "/meta", "allowLocal": true}); w.Code != http.StatusConflict || code(w) != "unsupported" {
		t.Fatalf("catalog addon: %d %s", w.Code, w.Body.String())
	}
	cfgAddon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"id":"test.cfg","version":"1","name":"Needs Config","resources":["stream"],"types":["movie"],"behaviorHints":{"configurable":true,"configurationRequired":true}}`)
	}))
	defer cfgAddon.Close()
	w := install(local, map[string]any{"url": cfgAddon.URL, "allowLocal": true})
	var cfgOut struct{ Code, ConfigureURL string }
	_ = json.Unmarshal(w.Body.Bytes(), &cfgOut)
	if w.Code != http.StatusConflict || cfgOut.Code != "configure" || cfgOut.ConfigureURL != cfgAddon.URL+"/configure" {
		t.Fatalf("addon to configure: %d %s", w.Code, w.Body.String())
	}

	w = install(local, map[string]any{"url": addon.URL, "allowLocal": true})
	var installed struct {
		TransportURL string `json:"transportUrl"`
		Enabled      bool   `json:"enabled"`
		Local        bool   `json:"local"`
	}
	decodeBody(t, w, &installed)
	if installed.TransportURL != addon.URL+"/manifest.json" || !installed.Enabled || !installed.Local {
		t.Fatalf("installed: %+v", installed)
	}
	if w := install(local, map[string]any{"url": addon.URL + "/adult/manifest.json", "allowLocal": true, "allowAdult": true}); w.Code != http.StatusOK {
		t.Fatalf("adult addon, confirmed: %d %s", w.Code, w.Body.String())
	}

	var list []struct {
		TransportURL string `json:"transportUrl"`
		Manifest     struct{ ID string }
	}
	decodeBody(t, do(s, "GET", "/api/providers/stremio/addons", local, "", nil), &list)
	if len(list) != 2 || list[0].Manifest.ID != "test.addon" || list[1].Manifest.ID != "test.adult" {
		t.Fatalf("addons: %+v", list)
	}
	// Reordered and refreshed on this computer (not from the LAN: lan_test.go).
	decodeBody(t, do(s, "PUT", "/api/providers/stremio/addons/order", local, jsonBody(t, map[string]any{"keys": []string{"stremio:test.adult"}}), nil), &list)
	if list[0].Manifest.ID != "test.adult" {
		t.Fatalf("reordered: %+v", list)
	}
	var refreshed struct {
		Errors map[string]string `json:"errors"`
	}
	decodeBody(t, do(s, "POST", "/api/providers/stremio/refresh", local, "{}", nil), &refreshed)
	if len(refreshed.Errors) != 0 {
		t.Fatalf("refresh: %+v", refreshed.Errors)
	}

	// The Internet Archive and the streaming sites (off), then the addons
	// in their order.
	var infos []app.ProviderInfo
	decodeBody(t, do(s, "GET", "/api/providers", local, "", nil), &infos)
	n := 1 + len(streamSites)
	if len(infos) != n+2 || infos[0].ID != "archive" || infos[0].Enabled || infos[n].ID != "stremio:test.adult" || infos[n+1].ID != "stremio:test.addon" ||
		!infos[n+1].Enabled || infos[n+1].Kind != "stremio" || infos[n+1].Icon == "" || strings.Join(infos[n+1].Types, ",") != "movie,tv" {
		t.Fatalf("providers: %+v", infos)
	}
	for i, id := range streamSites {
		if x := infos[1+i]; x.ID != id || x.Enabled || x.Kind != "builtin" || strings.Join(x.Types, ",") != "movie,tv" {
			t.Fatalf("site %d: %+v", i, x)
		}
	}

	// Disabled: still listed, not asked.
	if w := do(s, "PUT", "/api/providers/stremio/addons/enabled", local, jsonBody(t, map[string]any{"key": "stremio:test.adult", "enabled": false}), nil); w.Code != http.StatusOK {
		t.Fatalf("disable: %d %s", w.Code, w.Body.String())
	}
	decodeBody(t, do(s, "GET", "/api/providers", local, "", nil), &infos)
	if infos[n].ID != "stremio:test.adult" || infos[n].Enabled {
		t.Fatalf("disabled: %+v", infos[n])
	}

	if w := do(s, "DELETE", "/api/providers/stremio/addons?key="+url.QueryEscape("stremio:test.adult"), lan, "", nil); w.Code != http.StatusForbidden {
		t.Fatalf("uninstall from the LAN: %d", w.Code)
	}
	if w := do(s, "DELETE", "/api/providers/stremio/addons?key="+url.QueryEscape(addon.URL+"/adult/manifest.json"), local, "", nil); w.Code != http.StatusOK {
		t.Fatalf("uninstall: %d %s", w.Code, w.Body.String())
	}
	if w := do(s, "DELETE", "/api/providers/stremio/addons?key=nope", local, "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("uninstall unknown: %d", w.Code)
	}
	decodeBody(t, do(s, "GET", "/api/providers/stremio/addons", local, "", nil), &list)
	if len(list) != 1 {
		t.Fatalf("after uninstall: %+v", list)
	}
	// Collections on this computer aren't read.
	if w := do(s, "GET", "/api/providers/stremio/catalog?url="+url.QueryEscape(addon.URL+"/collection.json"), local, "", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("local collection: %d %s", w.Code, w.Body.String())
	}
}

// installTestAddon installs the fake addon (confirmed as local).
func installTestAddon(t *testing.T, s *Server) *fakeAddon {
	t.Helper()
	addon := newFakeAddon(t)
	if w := do(s, "POST", "/api/providers/stremio/addons", local, jsonBody(t, map[string]any{"url": addon.URL, "allowLocal": true}), nil); w.Code != http.StatusOK {
		t.Fatalf("install: %d %s", w.Code, w.Body.String())
	}
	return addon
}

type streamsResponse struct {
	Streams   []stream.Stream `json:"streams"`
	Errors    []struct{ Provider, Name, Error string }
	Subtitles []struct{ URL, Language string }
	Episode   int `json:"episode"`
}

func TestProviderStreams(t *testing.T) {
	s := newProvidersServer(t)
	if w := do(s, "GET", "/api/providers/streams?mediaId=155", local, "", nil); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "no streaming provider") {
		t.Fatalf("no providers: %d %s", w.Code, w.Body.String())
	}
	addon := installTestAddon(t, s)

	var res streamsResponse
	decodeBody(t, do(s, "GET", "/api/providers/streams?mediaId=155", local, "", nil), &res)
	if len(res.Streams) != 3 || len(res.Errors) != 0 || res.Episode != catalog.MovieEpisode {
		t.Fatalf("movie: %+v", res)
	}
	direct, torrent, yt := res.Streams[0], res.Streams[1], res.Streams[2]
	if direct.Kind != stream.KindDirect || direct.Quality != "1080p" || direct.URL != "https://cdn.example/dk.1080p.mp4" || direct.ID == "" || direct.ProviderName != "Test Addon" {
		t.Errorf("direct: %+v", direct)
	}
	if torrent.Kind != stream.KindTorrent || torrent.Quality != "2160p" || torrent.Seeders != 120 || torrent.InfoHash != testInfoHash {
		t.Errorf("torrent: %+v", torrent)
	}
	if yt.Kind != stream.KindYouTube {
		t.Errorf("youtube: %+v", yt)
	}
	if len(res.Subtitles) != 1 || res.Subtitles[0].Language != "eng" {
		t.Errorf("subtitles: %+v", res.Subtitles)
	}

	// A show's episode: IMDb id, season and episode.
	show := catalog.ShowID(1396)
	if w := do(s, "GET", "/api/providers/streams?mediaId="+strconv.Itoa(show), local, "", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("show without an episode: %d %s", w.Code, w.Body.String())
	}
	decodeBody(t, do(s, "GET", "/api/providers/streams?mediaId="+strconv.Itoa(show)+"&episode=1002&provider=stremio:test.addon", lan, "", nil), &res)
	if len(res.Streams) != 1 || res.Streams[0].Type != "m3u8" || res.Episode != 1002 || !addon.asked("/stream/series/tt0903747:1:2.json") {
		t.Fatalf("episode: %+v", res)
	}
	if w := do(s, "GET", "/api/providers/streams?mediaId=155&provider=nope", local, "", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown provider: %d", w.Code)
	}
	// Addons find titles by id: nothing to match by hand.
	if w := do(s, "GET", "/api/providers/search?provider=stremio:test.addon&q=dark", local, "", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("manual search: %d", w.Code)
	}
}

func TestProviderPlay(t *testing.T) {
	s := newProvidersServer(t)
	installTestAddon(t, s)
	var res streamsResponse
	decodeBody(t, do(s, "GET", "/api/providers/streams?mediaId=155", local, "", nil), &res)
	direct, torrent, yt := res.Streams[0], res.Streams[1], res.Streams[2]

	type source struct {
		URL, OriginalURL, Type, Quality, Server, Provider, Referrer, Remote string
		Headers                                                             map[string]string
		Subtitles                                                           []struct{ URL, Language, Original string }
	}
	var played struct {
		Action, Player, Title string
		Sources               []source
		ResumeAt              float64
		Episode               int
	}
	// In the app (asked by a LAN device for mpv: it can't see this
	// computer's mpv).
	for _, remote := range []string{local, lan} {
		player := "builtin"
		if remote == lan {
			player = "mpv"
		}
		decodeBody(t, do(s, "POST", "/api/providers/play", remote, jsonBody(t, map[string]any{"mediaId": 155, "streamId": direct.ID, "player": player}), nil), &played)
		if played.Action != "builtin" || played.Title != "The Dark Knight (2008)" || played.Episode != 1 || len(played.Sources) != 1 {
			t.Fatalf("builtin (%s): %+v", remote, played)
		}
		src := played.Sources[0]
		if !strings.HasPrefix(src.URL, "/api/proxy?") || src.OriginalURL != direct.URL || src.Quality != "1080p" || src.Server != "Test Addon" ||
			src.Provider != "stremio:test.addon" || src.Referrer != "https://cdn.example/" || src.Headers["Referer"] != "https://cdn.example/" {
			t.Fatalf("source: %+v", src)
		}
		// The stream's own subtitles, then the addons' (as WebVTT, through
		// the proxy).
		if len(src.Subtitles) != 2 || src.Subtitles[0].Original != "https://cdn.example/dk.fr.srt" || src.Subtitles[1].Language != "eng" ||
			!strings.Contains(src.Subtitles[1].URL, "fmt=vtt") {
			t.Fatalf("subtitles: %+v", src.Subtitles)
		}
	}

	// Without a stream: the best direct link.
	decodeBody(t, do(s, "POST", "/api/providers/play", local, jsonBody(t, map[string]any{"mediaId": 155, "player": "builtin"}), nil), &played)
	if played.Action != "builtin" || played.Sources[0].OriginalURL != direct.URL {
		t.Fatalf("best: %+v", played)
	}
	// A stream that's gone, or given in full.
	if w := do(s, "POST", "/api/providers/play", local, jsonBody(t, map[string]any{"mediaId": 155, "streamId": "0000000000000000"}), nil); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "no longer available") {
		t.Fatalf("gone: %d %s", w.Code, w.Body.String())
	}
	full := map[string]any{"url": "https://other.example/x.m3u8", "title": "Somewhere else", "provider": "stremio:test.addon"}
	decodeBody(t, do(s, "POST", "/api/providers/play", local, jsonBody(t, map[string]any{"mediaId": 155, "streamId": "0000000000000000", "stream": full, "player": "builtin"}), nil), &played)
	if played.Action != "builtin" || played.Sources[0].OriginalURL != "https://other.example/x.m3u8" {
		t.Fatalf("given stream: %+v", played)
	}
	// An HLS stream can be converted too (read through the relay).
	if r := played.Sources[0].Remote; !stream.IsRemoteInput(r) || r == stream.RegisterRemote("https://other.example/x.m3u8", played.Sources[0].Headers) {
		t.Fatalf("HLS stream named %q for the conversions", r)
	}

	// YouTube opens in the browser.
	var ext struct{ Action, URL string }
	decodeBody(t, do(s, "POST", "/api/providers/play", local, jsonBody(t, map[string]any{"mediaId": 155, "streamId": yt.ID}), nil), &ext)
	if ext.Action != "external" || ext.URL != "https://www.youtube.com/watch?v=EXeTwQWrcwY" {
		t.Fatalf("youtube: %+v", ext)
	}

	// A torrent downloaded instead of played goes to the torrent client,
	// into the library (played, see TestTorrentStreamPlay).
	var addedMu sync.Mutex
	var added []map[string]any
	transmission := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Transmission-Session-Id") != "sess" {
			w.Header().Set("X-Transmission-Session-Id", "sess")
			w.WriteHeader(http.StatusConflict)
			return
		}
		var body struct {
			Method    string         `json:"method"`
			Arguments map[string]any `json:"arguments"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch body.Method {
		case "session-get":
			_, _ = io.WriteString(w, `{"result":"success","arguments":{"version":"4.0.5"}}`)
		case "torrent-add":
			addedMu.Lock()
			added = append(added, body.Arguments)
			addedMu.Unlock()
			_, _ = io.WriteString(w, `{"result":"success","arguments":{"torrent-added":{"hashString":"h","id":1,"name":"x"}}}`)
		default:
			_, _ = io.WriteString(w, `{"result":"success","arguments":{}}`)
		}
	}))
	defer transmission.Close()
	u, _ := url.Parse(transmission.URL)
	port, _ := strconv.Atoi(u.Port())
	lib := t.TempDir()
	cfg := s.app.Settings.Get()
	cfg.Torrent.DefaultClient = "transmission"
	cfg.Transmission.Host, cfg.Transmission.Port = u.Hostname(), port
	cfg.Library.Dir = lib
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	var sent struct{ Action, SavePath, Magnet string }
	decodeBody(t, do(s, "POST", "/api/providers/play", local, jsonBody(t, map[string]any{"mediaId": 155, "streamId": torrent.ID, "download": true}), nil), &sent)
	addedMu.Lock()
	defer addedMu.Unlock()
	if sent.Action != "torrent" || sent.SavePath != filepath.Join(lib, "The Dark Knight (2008)") || len(added) != 1 ||
		!strings.Contains(added[0]["filename"].(string), testInfoHash) || added[0]["download-dir"] != sent.SavePath {
		t.Fatalf("torrent: %+v, added %+v", sent, added)
	}
}

func TestProviderDownload(t *testing.T) {
	s := newProvidersServer(t)
	installTestAddon(t, s)
	// Downloads go into a "folder" that is a file: they stop right after
	// the link is found, before anything is downloaded.
	blocker := filepath.Join(t.TempDir(), "not-a-folder")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := s.app.Settings.Get()
	cfg.Downloads.Dir = blocker
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"mediaId": 155, "provider": "stremio:test.addon", "quality": "1080p"}
	var items []downloads.Item
	decodeBody(t, do(s, "POST", "/api/providers/download", local, jsonBody(t, body), nil), &items)
	if len(items) != 1 || items[0].Episode != 1 || items[0].Source != "stream" || items[0].Provider != "stremio:test.addon" || items[0].Quality != "1080p" ||
		items[0].Title != "The Dark Knight (2008)" || items[0].Mode != "" {
		t.Fatalf("queued: %+v", items)
	}
	// The link is found when the download starts.
	deadline := time.Now().Add(10 * time.Second)
	for {
		var it *downloads.Item
		for _, x := range s.app.Downloads.List() {
			if x.ID == items[0].ID {
				it = x
			}
		}
		if it != nil && it.Status == downloads.StatusFailed {
			if !strings.Contains(it.Error, "not-a-folder") {
				t.Fatalf("failed resolving: %s", it.Error)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("still %+v", it)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// The link comes with its container, which names the file.
	res, err := s.app.Downloads.Resolvers["stream"](context.Background(), &downloads.Item{MediaID: 155, Episode: 1, Provider: "stremio:test.addon", Quality: "1080p"})
	if err != nil || res.URL != "https://cdn.example/dk.1080p.mp4" || res.Container != "mp4" || res.Referrer != "https://cdn.example/" {
		t.Fatalf("resolved %+v, %v", res, err)
	}

	if w := do(s, "POST", "/api/providers/download", local, jsonBody(t, map[string]any{"mediaId": 155, "provider": "nope"}), nil); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown provider: %d", w.Code)
	}
	if w := do(s, "POST", "/api/providers/download", local, jsonBody(t, map[string]any{"mediaId": catalog.ShowID(1396), "episodes": []int{1000}}), nil); w.Code != http.StatusBadRequest {
		t.Fatalf("invalid episode: %d", w.Code)
	}
}

// Auto play next: a stream session records its provider and episode key;
// the next episode is that provider's best link for the next key.
func TestProviderNextEpisode(t *testing.T) {
	s := newProvidersServer(t)
	installTestAddon(t, s)
	show := catalog.ShowID(1396)
	next, err := s.app.Player.NextResolver(context.Background(), &player.Session{MediaID: show, Episode: 1001, Source: "stream", Provider: "stremio:test.addon"})
	if err != nil || next == nil {
		t.Fatalf("next: %+v %v", next, err)
	}
	if next.Episode != 1002 || next.Target != "https://cdn.example/bb.s01e02.m3u8" || next.Provider != "stremio:test.addon" || next.Source != "stream" ||
		!strings.Contains(next.Title, "S01E02") {
		t.Fatalf("next: %+v", next)
	}
	if !s.app.Player.HasNext(&player.Session{MediaID: show, Episode: 1001, Source: "stream", Provider: "stremio:test.addon"}) {
		t.Fatal("no next episode")
	}
}

// Devices on the LAN may use providers, not install or remove them.
func TestProviderInstallsFromLAN(t *testing.T) {
	s := newProvidersServer(t)
	for _, tc := range []struct{ method, path, body string }{
		{"POST", "/api/providers/nuvio/install", `{"repo":"https://example.com/manifest.json","id":"x"}`},
		{"POST", "/api/providers/nuvio/install-all", `{"repo":"https://example.com/manifest.json"}`},
		{"POST", "/api/extensions/install", `{"manifestURI":"https://example.com/ext.json"}`},
		{"DELETE", "/api/extensions/nuvio.x", ""},
		{"POST", "/api/providers/stremio/addons", `{"url":"https://example.com/manifest.json"}`},
		{"DELETE", "/api/providers/stremio/addons?key=x", ""},
	} {
		if w := do(s, tc.method, tc.path, lan, tc.body, nil); w.Code != http.StatusForbidden {
			t.Errorf("%s %s from the LAN: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
	// Nuvio repositories on the LAN can't be read (the sandbox refuses),
	// and aren't kept.
	repo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `[]`) }))
	defer repo.Close()
	if w := do(s, "POST", "/api/providers/nuvio/repos", local, jsonBody(t, map[string]any{"url": repo.URL + "/manifest.json"}), nil); w.Code != http.StatusBadRequest {
		t.Fatalf("local repository: %d %s", w.Code, w.Body.String())
	}
	if repos := s.app.Settings.Get().Providers.NuvioRepos; len(repos) != 0 {
		t.Fatalf("kept: %v", repos)
	}
}

// A play request with refresh asks the providers again rather than using
// the links found a moment ago (they expire); its sources can be converted
// (a "remote:" path).
// The in-app player asks the subtitle addons for the subtitles of the
// very file an online stream plays: its size and hash (read from the
// stream) and name go along, and the subtitles come as WebVTT through the
// proxy, the preferred language first.
func TestProviderSubtitles(t *testing.T) {
	s := newProvidersServer(t)
	addon := installTestAddon(t, s)
	var played struct {
		Action  string
		Sources []struct{ Remote, Filename string }
	}
	given := map[string]any{"url": addon.URL + "/video.mkv", "title": "The Dark Knight", "filename": "The.Dark.Knight.mkv", "provider": "stremio:test.addon"}
	decodeBody(t, do(s, "POST", "/api/providers/play", lan, jsonBody(t, map[string]any{"mediaId": 155, "stream": given, "player": "builtin"}), nil), &played)
	if played.Action != "builtin" || len(played.Sources) != 1 || played.Sources[0].Filename != "The.Dark.Knight.mkv" {
		t.Fatalf("play: %+v", played)
	}
	src := played.Sources[0]
	var subs []struct {
		URL, Language, Label, Original string
		Addon                          bool
	}
	path := "/api/providers/subtitles?mediaId=155&remote=" + url.QueryEscape(src.Remote) + "&filename=" + url.QueryEscape(src.Filename)
	decodeBody(t, do(s, "GET", path, lan, "", nil), &subs)
	if len(subs) != 2 || subs[0].Original != "https://subs.example/dk.en.matched.srt" || subs[0].Label != "English (this release)" ||
		subs[1].Language != "fre" || !subs[0].Addon || !strings.HasPrefix(subs[0].URL, "/api/proxy?") || !strings.HasSuffix(subs[0].URL, "&fmt=vtt") {
		t.Fatalf("subtitles: %+v (hash %s)", subs, testVideoHash)
	}
	if w := do(s, "GET", "/api/providers/subtitles?mediaId=155&remote=nope", local, "", nil); w.Code != http.StatusBadRequest {
		t.Errorf("not a stream: %d", w.Code)
	}
	if w := do(s, "GET", "/api/providers/subtitles?mediaId=155&remote="+url.QueryEscape(stream.RemotePrefix+"0123456789abcdef01234567"), local, "", nil); w.Code != http.StatusNotFound {
		t.Errorf("a stream KumoBox doesn't know: %d", w.Code)
	}
}

func TestProviderPlayRefresh(t *testing.T) {
	s := newProvidersServer(t)
	addon := installTestAddon(t, s)
	count := func() int {
		addon.mu.Lock()
		defer addon.mu.Unlock()
		n := 0
		for _, p := range addon.paths {
			if p == "/stream/movie/tt0468569.json" {
				n++
			}
		}
		return n
	}
	var played struct {
		Action  string
		Sources []struct{ OriginalURL, Remote string }
	}
	decodeBody(t, do(s, "POST", "/api/providers/play", local, jsonBody(t, map[string]any{"mediaId": 155, "player": "builtin"}), nil), &played)
	decodeBody(t, do(s, "POST", "/api/providers/play", local, jsonBody(t, map[string]any{"mediaId": 155, "player": "builtin"}), nil), &played)
	if n := count(); n != 1 {
		t.Fatalf("asked %d times for two plays a moment apart", n)
	}
	decodeBody(t, do(s, "POST", "/api/providers/play", local, jsonBody(t, map[string]any{"mediaId": 155, "player": "builtin", "refresh": true}), nil), &played)
	if n := count(); n != 2 || played.Action != "builtin" || len(played.Sources) == 0 {
		t.Fatalf("refresh: asked %d times, %+v", n, played)
	}
	if src := played.Sources[0]; !strings.HasPrefix(src.Remote, stream.RemotePrefix) || src.OriginalURL != "https://cdn.example/dk.1080p.mp4" {
		t.Fatalf("source: %+v", src)
	}
}

// The streaming sites are turned on, and pointed at a mirror, on this
// computer only.
func TestStreamSitesSettings(t *testing.T) {
	s := newProvidersServer(t)
	cfg := s.app.Settings.Get()
	cfg.Providers.Sites.Cinejoy = config.StreamSite{Enabled: true, URL: " https://resolver.example "}
	cfg.Providers.Sites.Xpass.Enabled = true
	raw, _ := json.Marshal(cfg)
	wantCode(t, do(s, "PUT", "/api/settings", lan, string(raw), nil), http.StatusOK, "LAN settings")
	if got := s.app.Settings.Get().Providers.Sites; got != (config.StreamSiteSettings{}) {
		t.Fatalf("a LAN device changed the sites: %+v", got)
	}
	wantCode(t, do(s, "PUT", "/api/settings", local, string(raw), nil), http.StatusOK, "settings")
	if got := s.app.Settings.Get().Providers.Sites.Cinejoy; got != (config.StreamSite{Enabled: true, URL: "https://resolver.example"}) {
		t.Fatalf("Cinejoy: %+v", got)
	}
	var ids []string
	for _, p := range s.app.StreamProviders() {
		ids = append(ids, p.ID())
		if c, ok := p.(*cinejoy.Provider); ok && c.APIURL != "https://resolver.example" {
			t.Errorf("Cinejoy's resolver: %q", c.APIURL)
		}
	}
	if strings.Join(ids, ",") != "cinejoy,xpass" {
		t.Errorf("providers asked: %v", ids)
	}
	var infos []app.ProviderInfo
	decodeBody(t, do(s, "GET", "/api/providers", lan, "", nil), &infos)
	on := map[string]bool{}
	for _, x := range infos {
		on[x.ID] = x.Enabled
	}
	if !on["cinejoy"] || !on["xpass"] || on["vidlink"] || on["archive"] {
		t.Errorf("providers: %+v", infos)
	}
}
