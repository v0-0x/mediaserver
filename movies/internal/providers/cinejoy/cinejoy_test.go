package cinejoy

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/providers"
)

// A stand-in for crush.wasm, assembled by hand: alloc is a bump allocator,
// and seal_request writes a 98-byte header of 7s followed by the request
// as it is, so the fake resolver can read requests and seal its answers
// with a key of 7s.
//
//	(module
//	  (memory (export "memory") 1)
//	  (global $next (mut i32) (i32.const 1024))
//	  (func (export "alloc") (param $n i32) (result i32) (local $p i32)
//	    global.get $next  local.set $p
//	    global.get $next  local.get $n  i32.add  global.set $next
//	    local.get $p)
//	  (func (export "seal_request") (param $in i32) (param $len i32) (param $seed i32)
//	        (param $seedLen i32) (param $out i32) (param $cap i32) (result i32)
//	    local.get $out  i32.const 7  i32.const 98  memory.fill
//	    local.get $out  i32.const 98  i32.add  local.get $in  local.get $len  memory.copy
//	    local.get $len  i32.const 98  i32.add))
func fakeEncoder(withImport bool) []byte {
	uleb := func(n int) []byte {
		var b []byte
		for {
			c := byte(n & 0x7f)
			n >>= 7
			if n != 0 {
				c |= 0x80
			}
			b = append(b, c)
			if n == 0 {
				return b
			}
		}
	}
	i32 := func(v int) []byte { // i32.const with a signed LEB128 operand
		b := []byte{0x41}
		for {
			c := byte(v & 0x7f)
			v >>= 7
			if (v == 0 && c&0x40 == 0) || (v == -1 && c&0x40 != 0) {
				return append(b, c)
			}
			b = append(b, c|0x80)
		}
	}
	vec := func(items ...[]byte) []byte {
		out := uleb(len(items))
		for _, it := range items {
			out = append(out, it...)
		}
		return out
	}
	section := func(id byte, content []byte) []byte {
		return append(append([]byte{id}, uleb(len(content))...), content...)
	}
	name := func(s string) []byte { return append(uleb(len(s)), s...) }
	cat := func(parts ...[]byte) []byte { return bytes.Join(parts, nil) }
	body := func(code []byte) []byte { return append(uleb(len(code)), code...) }

	const i32t = 0x7f
	types := vec(
		[]byte{0x60, 1, i32t, 1, i32t},
		[]byte{0x60, 6, i32t, i32t, i32t, i32t, i32t, i32t, 1, i32t},
	)
	alloc := cat(
		[]byte{1, 1, i32t},                      // one i32 local
		[]byte{0x23, 0, 0x21, 1},                // global.get 0, local.set 1
		[]byte{0x23, 0, 0x20, 0, 0x6a, 0x24, 0}, // next += n
		[]byte{0x20, 1, 0x0b},
	)
	seal := cat(
		[]byte{0},                                               // no locals
		[]byte{0x20, 4}, i32(7), i32(98), []byte{0xfc, 0x0b, 0}, // memory.fill
		[]byte{0x20, 4}, i32(98), []byte{0x6a, 0x20, 0, 0x20, 1, 0xfc, 0x0a, 0, 0}, // memory.copy
		[]byte{0x20, 1}, i32(98), []byte{0x6a, 0x0b},
	)
	mod := []byte{0, 'a', 's', 'm', 1, 0, 0, 0}
	mod = append(mod, section(1, types)...)
	if withImport {
		mod = append(mod, section(2, vec(cat(name("env"), name("f"), []byte{0, 0})))...)
	}
	mod = append(mod, section(3, vec([]byte{0}, []byte{1}))...)
	mod = append(mod, section(5, vec([]byte{0, 1}))...)
	mod = append(mod, section(6, vec(cat([]byte{i32t, 1}, i32(1024), []byte{0x0b})))...)
	exports := vec(
		cat(name("memory"), []byte{2, 0}),
		cat(name("alloc"), []byte{0, 0}),
		cat(name("seal_request"), []byte{0, 1}),
	)
	if withImport { // function indexes start after the imported one
		exports = vec(
			cat(name("memory"), []byte{2, 0}),
			cat(name("alloc"), []byte{0, 1}),
			cat(name("seal_request"), []byte{0, 2}),
		)
	}
	mod = append(mod, section(7, exports)...)
	mod = append(mod, section(10, vec(body(alloc), body(seal)))...)
	return mod
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// fakeSealed is what the fake encoder's requests are sealed with.
var fakeSealed = &sealed{responseKey: bytes.Repeat([]byte{7}, 32), keyID: 7, ephemeralPublic: bytes.Repeat([]byte{7}, 65)}

func seal(t testing.TB, s *sealed, v any) []byte {
	t.Helper()
	plain, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := aes.NewCipher(s.responseKey)
	gcm, _ := cipher.NewGCM(block)
	iv := make([]byte, 12)
	_, _ = rand.Read(iv)
	return append(iv, gcm.Seal(nil, iv, plain, s.aad())...)
}

type sealedRequest struct {
	Path    string         `json:"path"`
	Payload map[string]any `json:"payload"`
}

// fakeCinejoy is the resolver and its CDN: answers maps a request's path
// (and embed, after a space) to the answer's data, or to a status.
type fakeCinejoy struct {
	t       *testing.T
	wasm    []byte
	servers string
	answers map[string]any

	mu       sync.Mutex
	requests []sealedRequest
	headers  []http.Header
}

func (f *fakeCinejoy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.headers = append(f.headers, r.Header.Clone())
	f.mu.Unlock()
	switch r.Host + r.URL.Path {
	case "api.test/crush.wasm":
		_, _ = w.Write(f.wasm)
	case "api.test/servers":
		_, _ = io.WriteString(w, f.servers)
	case "api.test/g":
		body, _ := io.ReadAll(r.Body)
		var req sealedRequest
		if err := json.Unmarshal(body, &req); err != nil {
			f.t.Errorf("request isn't the fake encoder's: %q", body)
			http.Error(w, "bad", 400)
			return
		}
		f.mu.Lock()
		f.requests = append(f.requests, req)
		f.mu.Unlock()
		key := req.Path
		if e, ok := req.Payload["embed"]; ok {
			key += " " + e.(string)
		}
		env := map[string]any{"status": 404, "data": nil}
		if a, ok := f.answers[key]; ok {
			if status, ok := a.(int); ok {
				env["status"] = status
			} else {
				env = map[string]any{"status": 200, "data": a}
			}
		}
		_, _ = w.Write(seal(f.t, fakeSealed, env))
	case "cdn.test/master.m3u8":
		_, _ = io.WriteString(w, "#EXTM3U\n#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"a\",URI=\"audio.m3u8\"\n"+
			"#EXT-X-STREAM-INF:BANDWIDTH=800000,RESOLUTION=1280x720,AUDIO=\"a\"\nhd/index.m3u8\n"+
			"#EXT-X-STREAM-INF:BANDWIDTH=2000000,RESOLUTION=1920x800,AUDIO=\"a\"\nfhd/index.m3u8\n")
	case "cdn.test/hd/index.m3u8":
		_, _ = io.WriteString(w, "#EXTM3U\n#EXT-X-TARGETDURATION:6\n#EXTINF:6,\nseg0.ts\n#EXT-X-ENDLIST\n")
	case "cdn.test/hd/seg0.ts", "cdn.test/1080.mp4":
		_, _ = w.Write(bytes.Repeat([]byte{0x47}, 376))
	case "cdn.test/broken.m3u8":
		_, _ = io.WriteString(w, "#EXTM3U\n#EXTINF:6,\nseg.ts\n")
	case "cdn.test/seg.ts":
		_, _ = io.WriteString(w, "<!DOCTYPE html><html><body>"+strings.Repeat("blocked ", 40)+"</body></html>")
	default:
		http.NotFound(w, r)
	}
}

// hostRouter sends every request to srv, whatever its host: links keep
// their real look (https, a host name) and reach the fake servers.
type hostRouter struct{ srv *httptest.Server }

func (h hostRouter) RoundTrip(r *http.Request) (*http.Response, error) {
	u, _ := url.Parse(h.srv.URL)
	r = r.Clone(r.Context())
	r.Host = r.URL.Host
	r.URL.Scheme, r.URL.Host = "http", u.Host
	return http.DefaultTransport.RoundTrip(r)
}

func newTest(t *testing.T, answers map[string]any) (*Provider, *fakeCinejoy) {
	t.Helper()
	f := &fakeCinejoy{
		t: t, wasm: fakeEncoder(false), answers: answers,
		servers: `{"servers":[{"name":"Lisbon","status":"ok"},{"name":"Nebula","status":"ok"},{"name":"Solara","status":"down"},{"name":"Other","status":"ok"}]}`,
	}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	p := &Provider{APIURL: "https://api.test", HTTP: &http.Client{Transport: hostRouter{srv}}, EncoderSHA256: sum(f.wasm), Grace: 200 * time.Millisecond}
	// The server list is read again.
	catalogsMu.Lock()
	delete(catalogs, p.APIURL)
	catalogsMu.Unlock()
	return p, f
}

func TestStreamsMovie(t *testing.T) {
	p, f := newTest(t, map[string]any{
		"/Lisbon/movie": map[string]any{"stream": []any{map[string]any{
			"id": "s1", "type": "hls", "playlist": "https://cdn.test/master.m3u8",
			"captions": []any{map[string]any{"url": "https://cdn.test/en.vtt", "language": "en", "type": "vtt"}, map[string]any{"url": "javascript:x"}},
		}}},
	})
	streams, err := p.Streams(context.Background(), providers.Request{TMDBID: 603, Type: providers.TypeMovie, Title: "The Matrix", Year: 1999})
	if err != nil {
		t.Fatal(err)
	}
	if len(streams) != 1 {
		t.Fatalf("streams = %+v", streams)
	}
	s := streams[0]
	if s.URL != "https://cdn.test/master.m3u8" || s.Type != providers.StreamM3U8 || s.Quality != "1080p" || s.Title != "Cinejoy · Lisbon" {
		t.Errorf("stream = %+v", s)
	}
	if s.Headers["Origin"] != DefaultSiteURL || s.Headers["Referer"] != DefaultSiteURL+"/" {
		t.Errorf("headers = %v", s.Headers)
	}
	if len(s.Subtitles) != 1 || s.Subtitles[0].URL != "https://cdn.test/en.vtt" || s.Subtitles[0].Language != "en" {
		t.Errorf("subtitles = %+v", s.Subtitles)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	paths := map[string]bool{}
	for _, r := range f.requests {
		paths[r.Path] = true
		if r.Payload["tmdb"] != "603" || r.Payload["title"] != "The Matrix" || r.Payload["year"] != "1999" {
			t.Errorf("payload = %v", r.Payload)
		}
		if _, ok := r.Payload["season"]; ok {
			t.Errorf("movie payload with a season: %v", r.Payload)
		}
	}
	// Only the verified servers that are up are asked.
	if len(paths) != 2 || !paths["/Lisbon/movie"] || !paths["/Nebula/movie"] {
		t.Errorf("paths asked = %v", paths)
	}
	for _, h := range f.headers {
		if h.Get("Origin") != DefaultSiteURL {
			t.Errorf("request without Cinejoy's Origin: %v", h)
		}
	}
}

func TestStreamsEpisodeFiles(t *testing.T) {
	p, f := newTest(t, map[string]any{
		"/Nebula/series": map[string]any{"stream": []any{map[string]any{
			"type": "file",
			"qualities": map[string]any{
				"720":  map[string]any{"type": "mp4", "url": "https://cdn.test/720.mp4"},
				"1080": map[string]any{"type": "mp4", "url": "https://cdn.test/1080.mp4"},
				"480":  map[string]any{"type": "mp4", "url": "http://cdn.test/480.mp4"},
			},
		}}},
	})
	streams, err := p.Streams(context.Background(), providers.Request{TMDBID: 1396, Type: providers.TypeTV, Title: "Breaking Bad", Season: 2, Episode: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(streams) != 2 || streams[0].Quality != "1080p" || streams[1].Quality != "720p" || streams[0].Type != providers.StreamMP4 {
		t.Fatalf("streams = %+v", streams)
	}
	if streams[0].Title != "Cinejoy · Nebula · 1080p" {
		t.Errorf("title = %q", streams[0].Title)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.requests {
		if r.Payload["season"] != "2" || r.Payload["episode"] != "5" || r.Payload["tmdb"] != "1396" {
			t.Errorf("payload = %v", r.Payload)
		}
	}
}

func TestStreamsNested(t *testing.T) {
	p, _ := newTest(t, map[string]any{
		"/Lisbon/movie":    map[string]any{"stream": []any{map[string]any{"id": "e1", "playlist": "embed"}}},
		"/Lisbon/movie e1": map[string]any{"stream": []any{map[string]any{"playlist": "https://cdn.test/master.m3u8"}}},
		"/Nebula/movie e1": 404,
		"/Nebula/movie":    500,
	})
	streams, err := p.Streams(context.Background(), providers.Request{TMDBID: 1, Type: providers.TypeMovie, Title: "X"})
	if err != nil {
		t.Fatal(err)
	}
	if len(streams) != 1 || streams[0].URL != "https://cdn.test/master.m3u8" {
		t.Fatalf("streams = %+v", streams)
	}
}

func TestStreamsNotFound(t *testing.T) {
	p, _ := newTest(t, map[string]any{})
	streams, err := p.Streams(context.Background(), providers.Request{TMDBID: 2, Type: providers.TypeMovie, Title: "Nothing"})
	if err != nil || len(streams) != 0 {
		t.Fatalf("streams, err = %+v, %v; want none, no error", streams, err)
	}
}

func TestStreamsRejectsStreamsThatDontPlay(t *testing.T) {
	p, _ := newTest(t, map[string]any{
		"/Lisbon/movie": map[string]any{"stream": []any{map[string]any{"playlist": "https://cdn.test/broken.m3u8"}}},
		"/Nebula/movie": map[string]any{"stream": []any{map[string]any{"playlist": "https://cdn.test/gone.m3u8"}}},
	})
	_, err := p.Streams(context.Background(), providers.Request{TMDBID: 3, Type: providers.TypeMovie, Title: "Broken"})
	if err == nil || !strings.Contains(err.Error(), "Lisbon: no video") || !strings.Contains(err.Error(), "Nebula: 404") {
		t.Fatalf("err = %v", err)
	}
}

func TestStreamsNeedsTMDBID(t *testing.T) {
	_, err := (&Provider{}).Streams(context.Background(), providers.Request{Type: providers.TypeMovie, Title: "X"})
	if !providers.IsSkip(err) {
		t.Fatalf("err = %v, want a skip", err)
	}
}

func TestEncoderPinned(t *testing.T) {
	p, _ := newTest(t, nil)
	p.EncoderSHA256 = strings.Repeat("0", 64)
	_, err := p.Streams(context.Background(), providers.Request{TMDBID: 4, Type: providers.TypeMovie})
	if err == nil || !strings.Contains(err.Error(), "changed upstream") {
		t.Fatalf("err = %v", err)
	}
	// The real thing is refused for wanting something from its host.
	withImport := fakeEncoder(true)
	if _, err := loadEncoder(context.Background(), withImport, sum(withImport)); err == nil || !strings.Contains(err.Error(), "host") {
		t.Fatalf("encoder with an import: err = %v", err)
	}
	if cachedEncoder(sum(withImport)) != nil {
		t.Fatal("refused encoder kept")
	}
}

func TestEncoderSeals(t *testing.T) {
	wasm := fakeEncoder(false)
	e, err := loadEncoder(context.Background(), wasm, sum(wasm))
	if err != nil {
		t.Fatal(err)
	}
	// Instances run side by side.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := e.seal(context.Background(), "/Lisbon/movie", map[string]any{"tmdb": "603"})
			if err != nil {
				t.Error(err)
				return
			}
			if !bytes.Equal(s.responseKey, fakeSealed.responseKey) || s.keyID != 7 || len(s.ephemeralPublic) != 65 {
				t.Errorf("header = %+v", s)
			}
			if string(s.body) != `{"path":"/Lisbon/movie","payload":{"tmdb":"603"}}` {
				t.Errorf("body = %s", s.body)
			}
		}()
	}
	wg.Wait()
}

func TestDecrypt(t *testing.T) {
	wire := seal(t, fakeSealed, map[string]any{"status": 200, "data": map[string]any{"stream": []any{}}})
	env, err := decrypt(fakeSealed, wire)
	if err != nil || env.Status != 200 || string(env.Data) != `{"stream":[]}` {
		t.Fatalf("env, err = %+v, %v", env, err)
	}
	tampered := append([]byte(nil), wire...)
	tampered[len(tampered)-1] ^= 1
	if _, err := decrypt(fakeSealed, tampered); err == nil {
		t.Error("tampered answer authenticated")
	}
	other := *fakeSealed
	other.keyID = 8
	if _, err := decrypt(&other, wire); err == nil {
		t.Error("answer for another key id authenticated")
	}
	if _, err := decrypt(fakeSealed, wire[:20]); err == nil {
		t.Error("short answer accepted")
	}
	if _, err := splitSealed(make([]byte, headerSize)); err == nil {
		t.Error("sealed request without a body accepted")
	}
}

func TestBestQuality(t *testing.T) {
	for master, want := range map[string]string{
		"#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1,RESOLUTION=3840x2160\na\n#EXT-X-STREAM-INF:RESOLUTION=1280x720,CODECS=\"x\"\nb": "2160p",
		"#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\na": "",
	} {
		if got := bestQuality(master); got != want {
			t.Errorf("bestQuality(%q) = %q, want %q", master, got, want)
		}
	}
}

func TestAPIURL(t *testing.T) {
	for in, want := range map[string]string{
		"":                         DefaultAPIURL,
		"resolver.example":         "https://resolver.example",
		" https://resolver.test/ ": "https://resolver.test",
	} {
		if got := New(in).api(); got != want {
			t.Errorf("api(%q) = %q, want %q", in, got, want)
		}
	}
}
