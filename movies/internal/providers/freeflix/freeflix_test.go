package freeflix

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/v0-0x/mediaserver/movies/internal/providers"
)

// hostRouter sends every request to the fake site, whatever its host:
// links keep their real look (https, a host name) and reach the fake.
type hostRouter struct{ srv *httptest.Server }

func (h hostRouter) RoundTrip(r *http.Request) (*http.Response, error) {
	u, _ := url.Parse(h.srv.URL)
	r = r.Clone(r.Context())
	r.Host = r.URL.Host
	r.URL.Scheme, r.URL.Host = "http", u.Host
	return http.DefaultTransport.RoundTrip(r)
}

// fake is the sites and enc-dec.app: routes maps "host/path" to a
// handler.
type fake struct {
	t      *testing.T
	routes map[string]http.HandlerFunc

	mu   sync.Mutex
	seen []*http.Request
}

func (f *fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.seen = append(f.seen, r)
	f.mu.Unlock()
	if h, ok := f.routes[r.Host+r.URL.Path]; ok {
		h(w, r)
		return
	}
	http.NotFound(w, r)
}

func (f *fake) requests(host string) []*http.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*http.Request
	for _, r := range f.seen {
		if r.Host == host {
			out = append(out, r)
		}
	}
	return out
}

func newTest(t *testing.T, id string, routes map[string]http.HandlerFunc) (*Provider, *fake) {
	t.Helper()
	f := &fake{t: t, routes: routes}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	p := New(SiteByID(id), "https://"+id+".test")
	p.DecryptURL = "https://dec.test/api"
	p.HTTP = &http.Client{Transport: hostRouter{srv}}
	return p, f
}

func writeJSON(v any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(v) }
}

func readJSON(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
		t.Errorf("%s: body isn't JSON: %v", r.URL.Path, err)
	}
	return v
}

var (
	movie   = providers.Request{TMDBID: 603, IMDbID: "tt0133093", Type: providers.TypeMovie, Title: "The Matrix", Year: 1999}
	episode = providers.Request{TMDBID: 1396, Type: providers.TypeTV, Title: "Breaking Bad", Year: 2008, Season: 2, Episode: 5}
)

func TestVidlinkFiles(t *testing.T) {
	p, f := newTest(t, "vidlink", map[string]http.HandlerFunc{
		"dec.test/api/enc-vidlink": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("text") != "1396" {
				t.Errorf("enc-vidlink text = %q", r.URL.Query().Get("text"))
			}
			_, _ = io.WriteString(w, `{"status":200,"result":"ENC1396"}`)
		},
		"vidlink.test/api/b/tv/ENC1396/2/5": writeJSON(map[string]any{"stream": map[string]any{
			"type": "file",
			"qualities": map[string]any{
				"360":  map[string]any{"type": "mp4", "url": "https://cdn.test/360.mp4"},
				"1080": map[string]any{"type": "mp4", "url": "https://cdn.test/1080.mp4"},
				"720":  map[string]any{"type": "mp4", "url": "https://cdn.test/720.mp4"},
			},
			"captions": []any{map[string]any{"language": "English", "url": "https://cdn.test/en.srt"}},
		}}),
	})
	streams, err := p.Streams(context.Background(), episode)
	if err != nil {
		t.Fatal(err)
	}
	var qualities []string
	for _, s := range streams {
		qualities = append(qualities, s.Quality)
		if s.Type != providers.StreamMP4 || s.Headers["Referer"] != "https://vidlink.test/" || s.Headers["Origin"] != "https://vidlink.test" {
			t.Errorf("stream = %+v", s)
		}
		if len(s.Subtitles) != 1 || s.Subtitles[0].Language != "English" {
			t.Errorf("subtitles = %+v", s.Subtitles)
		}
	}
	if strings.Join(qualities, ",") != "1080p,720p,360p" || streams[0].Title != "Vidlink · 1080p" {
		t.Fatalf("streams = %+v", streams)
	}
	if r := f.requests("vidlink.test"); len(r) != 1 || r[0].Header.Get("Referer") != "https://vidlink.test/" {
		t.Errorf("vidlink requests = %v", r)
	}
}

func TestVidlinkPlaylist(t *testing.T) {
	p, _ := newTest(t, "vidlink", map[string]http.HandlerFunc{
		"dec.test/api/enc-vidlink":     writeJSON(map[string]any{"result": "E"}),
		"vidlink.test/api/b/movie/E":   writeJSON(map[string]any{"stream": map[string]any{"playlist": "https://cdn.test/master.m3u8"}}),
		"vidlink.test/api/b/movie/bad": writeJSON(nil),
	})
	streams, err := p.Streams(context.Background(), movie)
	if err != nil {
		t.Fatal(err)
	}
	if len(streams) != 1 || streams[0].Type != providers.StreamM3U8 || streams[0].URL != "https://cdn.test/master.m3u8" {
		t.Fatalf("streams = %+v", streams)
	}
}

func TestHexa(t *testing.T) {
	var apiKey string
	p, _ := newTest(t, "hexa", map[string]http.HandlerFunc{
		"dec.test/api/enc-hexa": writeJSON(map[string]any{"result": map[string]any{"token": "CAP"}}),
		"hexa.test/api/tmdb/tv/1396/season/2/episode/5/images": func(w http.ResponseWriter, r *http.Request) {
			apiKey = r.Header.Get("X-Api-Key")
			if r.Header.Get("X-Cap-Token") != "CAP" || r.Header.Get("Referer") != hexaReferer || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(apiKey) {
				t.Errorf("headers = %v", r.Header)
			}
			_, _ = io.WriteString(w, "ENCRYPTED\n")
		},
		"dec.test/api/dec-hexa": func(w http.ResponseWriter, r *http.Request) {
			body := readJSON(t, r)
			if body["text"] != "ENCRYPTED" || body["key"] != apiKey {
				t.Errorf("dec-hexa body = %v (key %s)", body, apiKey)
			}
			writeJSON(map[string]any{"result": map[string]any{"sources": []any{
				map[string]any{"server": "alpha", "url": "https://cdn.test/a.m3u8"},
				map[string]any{"server": "beta", "url": ""},
			}}})(w, r)
		},
	})
	streams, err := p.Streams(context.Background(), episode)
	if err != nil {
		t.Fatal(err)
	}
	if len(streams) != 1 || streams[0].Title != "Hexa · ALPHA" || streams[0].Type != providers.StreamM3U8 || streams[0].Headers["Referer"] != hexaReferer {
		t.Fatalf("streams = %+v", streams)
	}
}

// xpassSeal encrypts v as Xpass's data at path with token.
func xpassSeal(t *testing.T, v any, path, token string) string {
	t.Helper()
	plain, _ := json.Marshal(v)
	key := sha256.Sum256([]byte(XpassKeyPrefix + path + "|" + token))
	block, _ := aes.NewCipher(key[:])
	gcm, _ := cipher.NewGCM(block)
	iv := make([]byte, 12)
	_, _ = rand.Read(iv)
	return base64.RawURLEncoding.EncodeToString(append(iv, gcm.Seal(nil, iv, plain, nil)...))
}

func TestXpass(t *testing.T) {
	token := "djN8cGF0aHxi." + strings.Repeat("ab", 32)
	data := xpassSeal(t, []any{
		map[string]any{"name": "TIK 1", "url": "/mdata/abc/1/playlist.json"},
		map[string]any{"name": "TIK 2", "url": "/mdata/abc/2/playlist.json"},
	}, "/data/movie/603", token)
	p, f := newTest(t, "xpass", map[string]http.HandlerFunc{
		"xpass.test/e/movie/603": func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, `<script>var dataUrl="/data/movie/603?token=`+token+`";</script>`)
		},
		"xpass.test/data/movie/603": func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Referer") != "https://xpass.test/e/movie/603" {
				t.Errorf("data Referer = %q", r.Header.Get("Referer"))
			}
			_, _ = io.WriteString(w, data)
		},
		"xpass.test/mdata/abc/1/playlist.json": writeJSON(map[string]any{"playlist": []any{map[string]any{"sources": []any{
			map[string]any{"file": "https://cdn.test/1/master.m3u8", "label": "1080p"},
		}}}}),
		"xpass.test/mdata/abc/2/playlist.json": writeJSON(map[string]any{"playlist": []any{map[string]any{"sources": []any{
			map[string]any{"file": "https://cdn.test/2/video.mp4", "label": "720p"},
		}}}}),
	})
	streams, err := p.Streams(context.Background(), movie)
	if err != nil {
		t.Fatal(err)
	}
	if len(streams) != 2 ||
		streams[0].Title != "Xpass · TIK 1 · 1080p" || streams[0].Type != providers.StreamM3U8 || streams[0].Quality != "1080p" ||
		streams[1].URL != "https://cdn.test/2/video.mp4" || streams[1].Type != providers.StreamMP4 || streams[1].Headers["Referer"] != "https://xpass.test/" {
		t.Fatalf("streams = %+v", streams)
	}
	if n := len(f.requests("xpass.test")); n != 4 {
		t.Errorf("%d requests to Xpass", n)
	}
}

func TestXpassDecrypt(t *testing.T) {
	path, token := "/data/movie/550", "x."+strings.Repeat("0f", 32)
	body := xpassSeal(t, []any{map[string]any{"name": "A"}}, path, token)
	var out []xpassList
	if err := xpassDecrypt([]byte(body), path, token, &out); err != nil || len(out) != 1 || out[0].Name != "A" {
		t.Fatalf("out, err = %+v, %v", out, err)
	}
	// Standard base64 and padding are taken too.
	std := strings.NewReplacer("-", "+", "_", "/").Replace(body) + strings.Repeat("=", (4-len(body)%4)%4)
	if err := xpassDecrypt([]byte(std), path, token, &out); err != nil {
		t.Errorf("standard base64: %v", err)
	}
	for name, tc := range map[string][3]string{
		"tampered":   {body[:len(body)-4] + "AAAA", path, token},
		"other path": {body, "/data/movie/551", token},
		"bad token":  {body, path, "badtoken"},
		"empty":      {"", path, token},
	} {
		if err := xpassDecrypt([]byte(tc[0]), tc[1], tc[2], &out); err == nil {
			t.Errorf("%s: decrypted", name)
		}
	}
}

func TestMapple(t *testing.T) {
	p, f := newTest(t, "mapple", map[string]http.HandlerFunc{
		"mapple.test/api/request-token": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.Header.Get("Referer") != "https://mapple.test/watch/tv/1396/2-5" {
				t.Errorf("request-token: %s %v", r.Method, r.Header)
			}
			_, _ = io.WriteString(w, `{"token":"TOK","expiresAt":99999999999999}`)
		},
		"mapple.test/api/encrypt": func(w http.ResponseWriter, r *http.Request) {
			body := readJSON(t, r)
			data, _ := body["data"].(map[string]any)
			if body["endpoint"] != "stream-encrypted" || data["mediaId"] != float64(1396) || data["mediaType"] != "tv" || data["tv_slug"] != "2-5" {
				t.Errorf("encrypt body = %v", body)
			}
			if data["source"] == "oak" {
				_, _ = io.WriteString(w, `{"url":"/api/stream?x=1"}`)
				return
			}
			_, _ = io.WriteString(w, `{}`)
		},
		"mapple.test/api/stream": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("requestToken") != "TOK" || r.URL.Query().Get("x") != "1" {
				t.Errorf("stream query = %q", r.URL.RawQuery)
			}
			_, _ = io.WriteString(w, `{"success":true,"data":{"stream_url":"https://cdn.test/oak.m3u8"}}`)
		},
	})
	streams, err := p.Streams(context.Background(), episode)
	if err != nil {
		t.Fatal(err)
	}
	if len(streams) != 1 || streams[0].Title != "Mapple · Oak" || streams[0].URL != "https://cdn.test/oak.m3u8" || streams[0].Headers["Referer"] != "https://mapple.test/" {
		t.Fatalf("streams = %+v", streams)
	}
	if n := len(f.requests("mapple.test")); n != 1+len(mappleSources)+1 {
		t.Errorf("%d requests to Mapple", n)
	}
}

func TestMappleTokenFromPage(t *testing.T) {
	p, _ := newTest(t, "mapple", map[string]http.HandlerFunc{
		"mapple.test/api/request-token": writeJSON(map[string]any{"token": "OLD", "expiresAt": 1}),
		"mapple.test/watch/movie/603": func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, `<script>window.__REQUEST_TOKEN__ = "PAGE";</script>`)
		},
	})
	tok, err := p.mappleToken(context.Background(), "https://mapple.test", "https://mapple.test/watch/movie/603")
	if err != nil || tok != "PAGE" {
		t.Fatalf("token, err = %q, %v", tok, err)
	}
}

func TestVideasy(t *testing.T) {
	r := movie
	r.Title = "Spider-Man: No Way Home"
	p, f := newTest(t, "videasy", map[string]http.HandlerFunc{
		"videasy.test/moviebox/sources-with-title": func(w http.ResponseWriter, r *http.Request) {
			want := "title=Spider-Man%253A%2520No%2520Way%2520Home&mediaType=movie&year=1999&tmdbId=603&imdbId=tt0133093"
			if r.URL.RawQuery != want {
				t.Errorf("query = %s\nwant    %s", r.URL.RawQuery, want)
			}
			if r.Header.Get("Origin") != videasyReferer {
				t.Errorf("Origin = %q", r.Header.Get("Origin"))
			}
			_, _ = io.WriteString(w, "SEALED")
		},
		"dec.test/api/dec-videasy": func(w http.ResponseWriter, r *http.Request) {
			body := readJSON(t, r)
			if body["text"] != "SEALED" || body["id"] != float64(603) {
				t.Errorf("dec-videasy body = %v", body)
			}
			writeJSON(map[string]any{"result": map[string]any{
				"sources":   []any{map[string]any{"quality": "1080p", "url": "https://cdn.test/v.m3u8"}},
				"subtitles": []any{map[string]any{"language": "fr", "url": "https://cdn.test/fr.vtt"}},
			}})(w, r)
		},
	})
	streams, err := p.Streams(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if len(streams) != 1 || streams[0].Title != "Videasy · MOVIEBOX · 1080p" || streams[0].Type != providers.StreamM3U8 ||
		streams[0].Headers["Referer"] != videasyReferer+"/" || len(streams[0].Subtitles) != 1 {
		t.Fatalf("streams = %+v", streams)
	}
	if n := len(f.requests("videasy.test")); n != len(videasyServers) {
		t.Errorf("%d servers asked", n)
	}
}

func TestPyQuote(t *testing.T) {
	for in, want := range map[string]string{
		"The Matrix":  "The%20Matrix",
		"AC/DC":       "AC/DC",
		"Amélie":      "Am%C3%A9lie",
		"a+b&c=d?e#f": "a%2Bb%26c%3Dd%3Fe%23f",
		"x_y.z-w~":    "x_y.z-w~",
	} {
		if got := pyQuote(in); got != want {
			t.Errorf("pyQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNeedsTMDBID(t *testing.T) {
	for _, s := range Sites {
		_, err := New(s, "").Streams(context.Background(), providers.Request{Type: providers.TypeMovie, Title: "X"})
		if !providers.IsSkip(err) {
			t.Errorf("%s: err = %v, want a skip", s.ID, err)
		}
	}
}

func TestErrorsAndNothing(t *testing.T) {
	// enc-dec.app down: the error says so.
	p, _ := newTest(t, "vidlink", map[string]http.HandlerFunc{})
	if _, err := p.Streams(context.Background(), movie); err == nil || !strings.Contains(err.Error(), "enc-dec.app") {
		t.Errorf("vidlink: err = %v", err)
	}
	// No data link on the embed page: not on Xpass.
	p, _ = newTest(t, "xpass", map[string]http.HandlerFunc{
		"xpass.test/e/movie/603": func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "<html></html>") },
	})
	if streams, err := p.Streams(context.Background(), movie); err != nil || len(streams) != 0 {
		t.Errorf("xpass: %v, %v", streams, err)
	}
}

func TestBaseURL(t *testing.T) {
	for in, want := range map[string]string{
		"":                       "https://vidlink.pro",
		"vidlink.example":        "https://vidlink.example",
		" http://mirror.test/ ":  "http://mirror.test",
		"https://mirror.test///": "https://mirror.test",
	} {
		if got := New(vidlinkSite, in).base(); got != want {
			t.Errorf("base(%q) = %q, want %q", in, got, want)
		}
	}
}

// The default client, like util.HTTP, can't reach this computer.
func TestClientRefusesLocal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("reached") }))
	defer srv.Close()
	p := New(vidlinkSite, srv.URL)
	if _, err := p.fetch(context.Background(), http.MethodGet, srv.URL+"/x", nil, nil); err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("err = %v, want a refusal", err)
	}
}
