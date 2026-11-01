package extensions

import (
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/imroc/req/v3"

	"github.com/v0-0x/mediaserver/movies/internal/events"
	"github.com/v0-0x/mediaserver/movies/internal/providers"
)

// fakeNuvioSite serves testdata/nuvio (the repository) and the fake
// streaming site its providers scrape, as http://nuvio.test.
type fakeNuvioSite struct {
	mu       sync.Mutex
	manifest []byte
	requests []string
}

func newFakeNuvioSite(t *testing.T) *fakeNuvioSite {
	raw, err := os.ReadFile(filepath.Join("testdata", "nuvio", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	return &fakeNuvioSite{manifest: raw}
}

func (f *fakeNuvioSite) setManifest(b []byte) {
	f.mu.Lock()
	f.manifest = b
	f.mu.Unlock()
}

func (f *fakeNuvioSite) seen(prefix string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, r := range f.requests {
		if strings.HasPrefix(r, prefix) {
			out = append(out, r)
		}
	}
	return out
}

func (f *fakeNuvioSite) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, r.URL.RequestURI())
	manifest := f.manifest
	f.mu.Unlock()
	p := r.URL.Path
	switch {
	case p == "/manifest.json":
		_, _ = w.Write(manifest)
	case p == "/scrapers.json":
		http.ServeFile(w, r, filepath.Join("testdata", "nuvio", "scrapers.json"))
	case strings.HasPrefix(p, "/providers/"):
		http.ServeFile(w, r, filepath.Join("testdata", "nuvio", "providers", filepath.Base(p)))
	case p == "/movie/550":
		if r.Header.Get("User-Agent") != "Fixture/1.0" {
			http.Error(w, "missing user agent", http.StatusForbidden)
			return
		}
		_, _ = io.WriteString(w, `<html><body><h1>Fight Club</h1>
			<div class="source" data-quality="1080p" data-size="1.4 GB"><span class="label"> Fixture 1080p </span><a href="http://cdn.nuvio.test/550/1080.mp4">Play</a></div>
			<div class="source" data-quality="720" data-size="734003200"><span class="label">Fixture 720p</span><a href="http://cdn.nuvio.test/550/720.mp4">Play</a></div>
			<div class="source"><span class="label">No link</span></div>
		</body></html>`)
	case p == "/tv/1399/1/2":
		_, _ = io.WriteString(w, `<div class="source" data-quality="1080p"><span class="label">S01E02</span><a href="http://cdn.nuvio.test/1399/s1e2.mp4">Play</a></div>`)
	case p == "/api/sources":
		sum := md5.Sum([]byte("kumobox"))
		if r.URL.Query().Get("token") != hex.EncodeToString(sum[:]) {
			http.Error(w, `{"error":"bad token"}`, http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"sources":[{"label":"API stream","file":"http://cdn.nuvio.test/`+r.URL.Query().Get("id")+`/master","res":"4K","type":"hls",
			"tracks":[{"url":"http://cdn.nuvio.test/en.vtt","lang":"English"},{"url":"javascript:alert(1)","lang":"x"}]}]}`)
	case strings.HasPrefix(p, "/built/"):
		w.Header().Set("Content-Type", "application/json")
		raw, _ := json.Marshal(map[string]string{"html": `<video><source src="http://cdn.nuvio.test/built/` + strings.TrimPrefix(p, "/built/") +
			`.mkv" title="Built 2160p" data-res="2160p" data-bytes="123456"></video>`})
		_, _ = w.Write(raw)
	case p == "/echo":
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Add("Set-Cookie", "a=1")
		w.Header().Add("Set-Cookie", "b=2")
		w.Header().Set("X-Echo", "yes")
		raw, _ := json.Marshal(map[string]any{
			"method": r.Method, "query": r.URL.RawQuery, "body": string(body),
			"contentType": r.Header.Get("Content-Type"), "ua": r.Header.Get("User-Agent"), "auth": r.Header.Get("Authorization"),
		})
		_, _ = w.Write(raw)
	case p == "/slow":
		select {
		case <-time.After(3 * time.Second):
		case <-r.Context().Done():
		}
		_, _ = io.WriteString(w, "late")
	case p == "/notfound":
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"nope"}`)
	default:
		http.NotFound(w, r)
	}
}

// testFetcher is the sandbox fetcher with every connection routed to srv,
// so providers can use the public-looking http://nuvio.test (the real
// fetcher refuses loopback addresses, which is the point of it).
func testFetcher(srv *httptest.Server) *Fetcher {
	addr := srv.Listener.Addr().String()
	c := req.C().SetTimeout(10 * time.Second)
	c.SetDial(func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	})
	c.SetProxy(nil)
	return &Fetcher{follow: c, noFollow: c, plain: c}
}

func newNuvioTestManager(t *testing.T) (*Manager, *fakeNuvioSite, *httptest.Server) {
	t.Helper()
	site := newFakeNuvioSite(t)
	srv := httptest.NewServer(site)
	t.Cleanup(srv.Close)
	m := newTestManager(t)
	m.fetcher = testFetcher(srv)
	return m, site, srv
}

// nuvioRuntime runs code in a Nuvio runtime.
func nuvioRuntime(t *testing.T, f *Fetcher, code string) *Runtime {
	t.Helper()
	man := &Manifest{ID: "nuvio.test", Name: "Test", Version: "1", Type: TypeNuvioProvider, Language: "javascript"}
	prog, err := compileNuvio(man, code)
	if err != nil {
		t.Fatal(err)
	}
	rt, err := NewRuntime(man, prog, RuntimeOptions{Fetcher: f, Extra: installNuvio, LoadTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rt.Close)
	return rt
}

// nuvioResult calls getStreams and decodes whatever it resolves to.
func nuvioResult(t *testing.T, rt *Runtime, out any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	raw, err := rt.callNuvio(ctx, "1", "movie", 0, 0)
	if err != nil {
		for _, l := range rt.Logs() {
			t.Logf("%s: %s", l.Level, l.Message)
		}
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
}

func TestParseNuvioRepo(t *testing.T) {
	raw, _ := os.ReadFile(filepath.Join("testdata", "nuvio", "manifest.json"))
	repo, err := ParseNuvioRepo("https://raw.githubusercontent.com/someone/repo/refs/heads/main/manifest.json", raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(repo.Providers) != 3 {
		t.Fatalf("providers = %+v", repo.Providers)
	}
	fx, built, esm := repo.Providers[0], repo.Providers[1], repo.Providers[2]
	if fx.ID != "fixture" || fx.ExtensionID != "nuvio.fixture" || !fx.Enabled || fx.Version != "1.0.0" ||
		fx.URL != "https://raw.githubusercontent.com/someone/repo/refs/heads/main/providers/fixture.js" ||
		!reflect.DeepEqual(fx.SupportedTypes, []string{"movie", "tv"}) || fx.Logo != "http://nuvio.test/logo.png" {
		t.Errorf("fixture = %+v", fx)
	}
	// Missing "enabled" means enabled; numeric versions are fine.
	if !built.Enabled || built.Version != "2" || !reflect.DeepEqual(built.SupportedTypes, []string{"movie"}) {
		t.Errorf("built = %+v", built)
	}
	if esm.Enabled || esm.URL != "https://raw.githubusercontent.com/providers/esm.js" || !reflect.DeepEqual(esm.SupportedTypes, []string{"movie"}) {
		t.Errorf("esm = %+v", esm)
	}

	raw, _ = os.ReadFile(filepath.Join("testdata", "nuvio", "scrapers.json"))
	repo, err = ParseNuvioRepo("https://example.org/repo/scrapers.json", raw)
	if err != nil {
		t.Fatal(err)
	}
	if repo.Name != "Test Scrapers" || repo.Version != "3.2.1" || len(repo.Providers) != 2 {
		t.Fatalf("repo = %+v", repo)
	}
	two, named := repo.Providers[0], repo.Providers[1]
	if two.ExtensionID != "nuvio.fixture-two" || two.URL != "https://cdn.nuvio.test/other/fixture.js" ||
		!reflect.DeepEqual(two.SupportedTypes, []string{"tv"}) || !reflect.DeepEqual(two.ContentLanguage, []string{"hi"}) {
		t.Errorf("two = %+v", two)
	}
	if named.ID != "built" || named.Name != "Named after its file" || named.Version != "0" {
		t.Errorf("named = %+v", named)
	}

	for _, bad := range []string{`{"name":"x"}`, `"x"`, `not json`} {
		if _, err := ParseNuvioRepo("https://example.org/m.json", []byte(bad)); err == nil {
			t.Errorf("ParseNuvioRepo(%s) succeeded", bad)
		}
	}
}

func TestNuvioProvidersEndToEnd(t *testing.T) {
	m, site, _ := newNuvioTestManager(t)
	ctx := context.Background()
	const repoURL = "http://nuvio.test/manifest.json"

	repo, err := m.NuvioRepo(ctx, repoURL)
	if err != nil {
		t.Fatal(err)
	}
	if len(repo.Providers) != 3 || repo.Providers[0].Installed {
		t.Fatalf("repo = %+v", repo)
	}

	// The repository's enabled providers install at once; "esm" is
	// disabled by default.
	infos, err := m.InstallNuvioRepo(ctx, repoURL)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 {
		t.Fatalf("installed %+v", infos)
	}
	if _, err := m.InstallNuvio(ctx, repoURL, "esm"); err != nil {
		t.Fatalf("install esm: %v", err)
	}
	if _, err := m.InstallNuvio(ctx, repoURL, "missing"); err == nil {
		t.Error("installed a provider the repository doesn't have")
	}
	inf, err := m.Get("nuvio.fixture")
	if err != nil || inf.Manifest.Type != TypeNuvioProvider || inf.Manifest.Nuvio == nil || inf.Manifest.Nuvio.ID != "fixture" ||
		inf.Manifest.PayloadURI != "http://nuvio.test/providers/fixture.js" || inf.Manifest.ManifestURI != repoURL || !inf.Supported {
		t.Fatalf("info = %+v, %v", inf, err)
	}
	repo, _ = m.NuvioRepo(ctx, repoURL)
	if !repo.Providers[0].Installed || repo.Providers[0].HasUpdate {
		t.Errorf("repo after install = %+v", repo.Providers[0])
	}

	ps := m.NuvioProviders()
	if len(ps) != 3 {
		t.Fatalf("providers = %d", len(ps))
	}
	byID := map[string]providers.Provider{}
	for _, p := range ps {
		byID[p.ID()] = p
	}
	fixture := byID["nuvio:fixture"]
	if fixture == nil || fixture.Kind() != providers.KindNuvio || fixture.Name() != "Fixture Provider" ||
		!fixture.Supports(providers.TypeTV) || byID["nuvio:built"].Supports(providers.TypeTV) {
		t.Fatalf("providers = %+v", byID)
	}
	if providers.Describe(fixture).Icon != "http://nuvio.test/logo.png" {
		t.Error("no icon")
	}

	// A movie: every provider answers.
	streams, errs := providers.Aggregate(ctx, ps, providers.Request{Type: providers.TypeMovie, TMDBID: 550, IMDbID: "tt0137523", Title: "Fight Club"}, 10*time.Second)
	if len(errs) != 0 {
		t.Fatalf("errors: %+v", errs)
	}
	got := map[string]providers.Stream{}
	for _, s := range streams {
		got[s.URL] = s
	}
	if len(streams) != 5 {
		t.Fatalf("streams = %+v", streams)
	}
	s1080 := got["http://cdn.nuvio.test/550/1080.mp4"]
	if s1080.Provider != "nuvio:fixture" || s1080.ProviderName != "Fixture Provider" || s1080.Title != "Fixture 1080p" ||
		s1080.Quality != "1080p" || s1080.Type != providers.StreamMP4 || s1080.Size != providers.SizeFromText("1.4 GB") ||
		s1080.Headers["Referer"] != "http://nuvio.test/" || s1080.Headers["X-Index"] != "0" || s1080.FileIdx != -1 {
		t.Errorf("1080p = %+v", s1080)
	}
	if s := got["http://cdn.nuvio.test/550/720.mp4"]; s.Quality != "720p" || s.Size != 734003200 {
		t.Errorf("720p = %+v", s)
	}
	api := got["http://cdn.nuvio.test/550/master"]
	if api.Type != providers.StreamM3U8 || api.Quality != "2160p" || len(api.Subtitles) != 1 || api.Subtitles[0].Language != "English" {
		t.Errorf("api = %+v", api)
	}
	if s := got["http://cdn.nuvio.test/built/550.mkv"]; s.Provider != "nuvio:built" || s.Quality != "2160p" || s.Size != 123456 || s.Type != providers.StreamMKV || s.Title != "Built 2160p" {
		t.Errorf("built = %+v", s)
	}
	if s := got["http://cdn.nuvio.test/esm/550.m3u8"]; s.Provider != "nuvio:esm" || s.Quality != "720p" || s.Title != "ESM movie" {
		t.Errorf("esm = %+v", s)
	}
	if q := site.seen("/api/sources"); len(q) != 1 || !strings.Contains(q[0], "id=550") || !strings.Contains(q[0], "type=movie") {
		t.Errorf("api requests = %v", q)
	}

	// An episode: only the providers that do shows are asked.
	streams, errs = providers.Aggregate(ctx, ps, providers.Request{Type: providers.TypeTV, TMDBID: 1399, Season: 1, Episode: 2}, 10*time.Second)
	if len(errs) != 0 || len(streams) != 2 || streams[0].URL != "http://cdn.nuvio.test/1399/s1e2.mp4" {
		t.Errorf("episode streams = %+v, %v", streams, errs)
	}
	if _, err := fixture.Streams(ctx, providers.Request{Type: providers.TypeMovie}); err == nil {
		t.Error("no error without a TMDB id")
	}

	// console output lands in the extension's logs.
	found := false
	for _, l := range m.Logs("nuvio.fixture") {
		if strings.Contains(l.Message, "[Fixture] tv 1399 S1E2") {
			found = true
		}
	}
	if !found {
		t.Errorf("logs = %+v", m.Logs("nuvio.fixture"))
	}

	// Updates go through the repository.
	if ups := m.CheckUpdates(ctx); len(ups) != 0 {
		t.Errorf("updates = %+v", ups)
	}
	site.setManifest([]byte(strings.Replace(string(mustRead(t, "testdata/nuvio/manifest.json")), `"version": "1.0.0"`, `"version": "1.1.0"`, 1)))
	ups := m.CheckUpdates(ctx)
	if len(ups) != 1 || ups[0].ID != "nuvio.fixture" || ups[0].CurrentVersion != "1.0.0" || ups[0].LatestVersion != "1.1.0" {
		t.Fatalf("updates = %+v", ups)
	}
	if err := m.SetEnabled("nuvio.fixture", false); err != nil {
		t.Fatal(err)
	}
	inf, err = m.Update(ctx, "nuvio.fixture")
	if err != nil || inf.Manifest.Version != "1.1.0" || inf.Enabled {
		t.Fatalf("update = %+v, %v (the update must keep it disabled)", inf, err)
	}
	if _, err := m.NuvioProvider("nuvio:fixture"); err == nil {
		t.Error("a disabled provider is offered")
	}
	if err := m.SetEnabled("nuvio.fixture", true); err != nil {
		t.Fatal(err)
	}
	if err := m.Uninstall("nuvio.esm"); err != nil {
		t.Fatal(err)
	}

	// Installed providers survive a restart.
	m2 := NewManager(m.db, m.settings, events.NewHub())
	m2.fetcher = m.fetcher
	m2.LoadAll()
	t.Cleanup(m2.Shutdown)
	p, err := m2.NuvioProvider("nuvio:fixture")
	if err != nil {
		t.Fatal(err)
	}
	if !p.Supports(providers.TypeTV) {
		t.Error("supported types lost")
	}
	if streams, err := p.Streams(ctx, providers.Request{Type: providers.TypeMovie, TMDBID: 550}); err != nil || len(streams) != 3 {
		t.Errorf("after restart: %+v, %v", streams, err)
	}
	if len(m2.NuvioProviders()) != 2 {
		t.Errorf("providers after restart = %d", len(m2.NuvioProviders()))
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.FromSlash(p))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestNuvioSandbox(t *testing.T) {
	site := newFakeNuvioSite(t)
	srv := httptest.NewServer(site)
	t.Cleanup(srv.Close)

	// The real fetcher: no loopback or LAN addresses, for repositories as
	// for providers.
	if _, err := FetchNuvioRepo(context.Background(), srv.URL+"/manifest.json"); err == nil || !errors.Is(err, errPrivateAddress) {
		t.Errorf("FetchNuvioRepo(localhost) err = %v", err)
	}
	if _, err := FetchNuvioRepo(context.Background(), "file:///etc/passwd"); err == nil {
		t.Error("file URL accepted")
	}
	rt := nuvioRuntime(t, NewFetcher(), `
		function attempt(fn) { try { return 'ok:' + fn(); } catch (e) { return 'error:' + e.message; } }
		module.exports = { getStreams: function () {
			var out = {
				fs: attempt(function () { return typeof require('fs'); }),
				child: attempt(function () { return typeof require('child_process'); }),
				buffer: attempt(function () { return typeof require('buffer').Buffer; }),
				nodeUrl: attempt(function () { return typeof require('node:url').URL; }),
			};
			return fetch('`+srv.URL+`/manifest.json').then(function () { out.local = 'fetched'; return out; },
				function (e) { out.local = 'error:' + e.message; return out; });
		} };`)
	var out map[string]string
	nuvioResult(t, rt, &out)
	if !strings.Contains(out["fs"], "Cannot find module 'fs'") || !strings.HasPrefix(out["child"], "error:") {
		t.Errorf("require = %v", out)
	}
	if out["buffer"] != "ok:function" || out["nodeUrl"] != "ok:function" {
		t.Errorf("built-in modules = %v", out)
	}
	if !strings.Contains(out["local"], "not allowed") {
		t.Errorf("local fetch = %q", out["local"])
	}

	// No getStreams.
	rt = nuvioRuntime(t, NewFetcher(), `module.exports = { other: 1 };`)
	if _, err := rt.callNuvio(context.Background(), "1", "movie", 0, 0); err == nil || !strings.Contains(err.Error(), "getStreams") {
		t.Errorf("err = %v", err)
	}
	// The caller's deadline wins over a stuck provider.
	rt = nuvioRuntime(t, NewFetcher(), `module.exports = { getStreams: function () { return new Promise(function () {}); } };`)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := rt.callNuvio(ctx, "1", "movie", 0, 0); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v", err)
	}
	// A provider stuck in an endless loop is stopped once its call is given
	// up on: it doesn't keep a core busy, and answers the next call.
	rt = nuvioRuntime(t, NewFetcher(), `var calls = 0;
		module.exports = { getStreams: function () { if (++calls === 1) { for (;;) {} } return [{ url: 'https://cdn.example/v.mp4' }]; } };`)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel2()
	if _, err := rt.callNuvio(ctx2, "1", "movie", 0, 0); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("stuck: err = %v", err)
	}
	ctx3, cancel3 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel3()
	if raw, err := rt.callNuvio(ctx3, "1", "movie", 0, 0); err != nil || !strings.Contains(string(raw), "cdn.example") {
		t.Errorf("after a stuck call: %s, %v", raw, err)
	}
	// A global getStreams (no module.exports) is found too; a rejected
	// promise is an error.
	rt = nuvioRuntime(t, NewFetcher(), `function getStreams(id, type, s, e) { return Promise.reject(new Error('site down: ' + id + type + s + e)); }`)
	if _, err := rt.callNuvio(context.Background(), "7", "movie", 0, 0); err == nil || err.Error() != "site down: 7movienullnull" {
		t.Errorf("err = %v", err)
	}
}

func TestNuvioCheerio(t *testing.T) {
	rt := nuvioRuntime(t, nil, `
const cheerio = require('cheerio');
const html = '<html><body>' +
  '<div id="list">' +
  '<a class="item" href="/a" data-id="1" data-info=\'{"q":"1080p"}\' data-some-thing="x">  First </a>' +
  '<a class="item hot" href="/b" data-id="2">Second</a>' +
  '<span class="item">Third</span>' +
  '</div>' +
  '<script>var player = {"file":"http://cdn.test/x.m3u8"};</script>' +
  '<select name="s"><option value="1">One</option><option value="2" selected>Two</option></select>' +
  '<input id="tok" value="abc">' +
  '<p>Hello <b>World</b>!</p>' +
  '</body></html>';
const $ = cheerio.load(html);
const out = {};
out.length = $('a.item').length;
out.hrefs = $('a.item').map((i, el) => $(el).attr('href')).get();
out.mapFlat = $('a.item').map(function () { return [$(this).attr('data-id'), 'x']; }).toArray();
out.thisEach = []; $('.item').each(function (i) { out.thisEach.push(i + ':' + $(this).text().trim()); });
out.breakEach = 0; $('.item').each(function () { out.breakEach++; return false; });
out.attribs = $('a.item')[0].attribs.href;
out.tag = $('a.item')[0].tagName + '/' + $('a.item')[0].name + '/' + $('a.item')[0].type + '/' + $('script')[0].type;
out.first = $('a.item').first().text().trim();
out.eqLast = $('.item').eq(-1).text();
out.filterFn = $('.item').filter((i, el) => $(el).hasClass('hot')).text();
out.filterSel = $('.item').filter('span').text();
out.not = $('.item').not('span').length;
out.is = $('a.hot').is('.item') && !$('a.hot').is('span');
out.data = $('a.item').first().data('id');
out.dataObj = $('a.item').first().data('info');
out.dataCamel = $('a.item').first().data('someThing');
out.dataAll = $('a.item').first().data();
out.script = /"file":"([^"]+)"/.exec($('script').html())[1];
out.val = $('#tok').val();
out.selectVal = $('select').val();
out.find = $('#list').find('a').length;
out.ctx = $('a', '#list').length;
out.ctxObj = $('a', $('#list')).length;
out.children = $('#list').children().length + '/' + $('#list').children('span').length;
out.parent = $('a.hot').parent().attr('id');
out.closest = $('b').closest('p').text();
out.next = $('a.item').first().next().text();
out.prev = $('span.item').prev().attr('href');
out.siblings = $('span.item').siblings().length;
out.inner = $('p').html();
out.outer = $.html($('b'));
out.toString = $('b').toString();
out.textNodes = $('p').contents().filter((i, n) => n.type === 'text').map((i, n) => n.data).get();
out.elChildren = $('p')[0].children.length;
out.forOf = []; for (const el of $('a.item')) out.forOf.push($(el).attr('data-id'));
out.spread = [...$('a')].length;
out.toArray = $('a').toArray().length + '/' + $('a').get().length;
out.get1 = $($('a').get(1)).text();
out.identity = $('a')[0] === $('a.item').get(0);
out.missingAttr = $('a').attr('nope') === undefined;
out.emptyHtml = $('.nothing').html();
out.emptyLength = $('.nothing').length;
out.fragment = $('<div><i>x</i></div>').find('i').text();
out.root = $.root().find('p').length;
out.slice = $('.item').slice(1).length + '/' + $('.item').slice(0, 1).text().trim();
out.textOf = cheerio.text($('b'));
out.prop = $('a.hot').prop('href') + '/' + $('a.hot').prop('tagName');
$('script').remove();
out.removed = $('script').length;
out.defaultLoad = typeof require('cheerio-without-node-native').default.load;
out.rnLoad = typeof require('react-native-cheerio').load;
module.exports = { getStreams: () => Promise.resolve(out) };
`)
	var out map[string]any
	nuvioResult(t, rt, &out)
	want := map[string]any{
		"length":      float64(2),
		"hrefs":       []any{"/a", "/b"},
		"mapFlat":     []any{"1", "x", "2", "x"},
		"thisEach":    []any{"0:First", "1:Second", "2:Third"},
		"breakEach":   float64(1),
		"attribs":     "/a",
		"tag":         "a/a/tag/script",
		"first":       "First",
		"eqLast":      "Third",
		"filterFn":    "Second",
		"filterSel":   "Third",
		"not":         float64(2),
		"is":          true,
		"data":        float64(1),
		"dataObj":     map[string]any{"q": "1080p"},
		"dataCamel":   "x",
		"dataAll":     map[string]any{"id": float64(1), "info": map[string]any{"q": "1080p"}, "someThing": "x"},
		"script":      "http://cdn.test/x.m3u8",
		"val":         "abc",
		"selectVal":   "2",
		"find":        float64(2),
		"ctx":         float64(2),
		"ctxObj":      float64(2),
		"children":    "3/1",
		"parent":      "list",
		"closest":     "Hello World!",
		"next":        "Second",
		"prev":        "/b",
		"siblings":    float64(2),
		"inner":       "Hello <b>World</b>!",
		"outer":       "<b>World</b>",
		"toString":    "<b>World</b>",
		"textNodes":   []any{"Hello ", "!"},
		"elChildren":  float64(3),
		"forOf":       []any{"1", "2"},
		"spread":      float64(2),
		"toArray":     "2/2",
		"get1":        "Second",
		"identity":    true,
		"missingAttr": true,
		"emptyHtml":   nil,
		"emptyLength": float64(0),
		"fragment":    "x",
		"root":        float64(1),
		"slice":       "2/First",
		"textOf":      "World",
		"prop":        "/b/A",
		"removed":     float64(0),
		"defaultLoad": "function",
		"rnLoad":      "function",
	}
	for k, w := range want {
		if !reflect.DeepEqual(out[k], w) {
			t.Errorf("%s = %#v, want %#v", k, out[k], w)
		}
	}
}

func TestNuvioWebAPIs(t *testing.T) {
	site := newFakeNuvioSite(t)
	srv := httptest.NewServer(site)
	t.Cleanup(srv.Close)
	rt := nuvioRuntime(t, testFetcher(srv), `
const axios = require('axios');
const CryptoJS = require('crypto-js');
const B = 'http://nuvio.test';
async function getStreams() {
  const out = {};
  // fetch: Headers, promise-returning text()/json(), URLSearchParams bodies.
  const h = new Headers({ 'X-A': '1' });
  h.append('x-a', '2');
  out.headers = h.get('X-A') + '|' + h.has('x-b');
  let r = await fetch(B + '/echo?x=1', { method: 'POST', headers: h, body: new URLSearchParams({ q: 'a b', n: '2' }) });
  const echo = await r.json();
  out.fetchEcho = echo.method + ' ' + echo.query + ' ' + echo.body + ' ' + echo.contentType;
  out.respHeaders = r.headers.get('content-type') + '|' + r.headers.get('X-ECHO') + '|' + r.headers.get('set-cookie');
  r = await fetch(B + '/notfound');
  out.notFound = r.ok + ' ' + r.status + ' ' + (await r.text());
  try { await (await fetch(B + '/echo')).clone().json(); out.clone = 'ok'; } catch (e) { out.clone = e.message; }
  try { await fetch(B + '/notfound').then(res => res.text()).then(t => JSON.parse(t + '}')); } catch (e) { out.badJSON = e.name; }
  // AbortController
  const ac = new AbortController();
  const p = fetch(B + '/slow', { signal: ac.signal });
  setTimeout(() => ac.abort(), 20);
  try { await p; out.abort = 'not aborted'; } catch (e) { out.abort = e.name; }
  // axios
  let a = await axios.get(B + '/echo', { params: { q: 'x y', list: [1, 2] }, headers: { 'User-Agent': 'Ax' } });
  out.axiosGet = a.status + ' ' + a.data.method + ' ' + a.data.query + ' ' + a.data.ua + ' ' + a.headers['x-echo'];
  a = await axios.post(B + '/echo', { hello: 'world' });
  out.axiosPost = a.data.body + ' ' + a.data.contentType;
  const api = axios.create({ baseURL: B, headers: { Authorization: 'Bearer t' } });
  api.interceptors.request.use(cfg => { cfg.params = Object.assign({}, cfg.params, { added: 1 }); return cfg; });
  a = await api.get('/echo');
  out.axiosInstance = a.data.auth + ' ' + a.data.query;
  try { await axios.get(B + '/notfound'); out.axiosErr = 'no error'; }
  catch (e) { out.axiosErr = e.isAxiosError + ' ' + e.response.status + ' ' + e.response.data.error + ' ' + e.message; }
  a = await axios.get(B + '/notfound', { validateStatus: () => true });
  out.validate = a.status;
  a = await axios({ url: B + '/echo', method: 'put', data: 'raw', responseType: 'text' });
  out.axiosText = typeof a.data;
  // crypto-js
  out.md5 = CryptoJS.MD5('kumobox').toString();
  out.sha256 = CryptoJS.SHA256('kumobox').toString(CryptoJS.enc.Base64);
  out.hmac = CryptoJS.HmacSHA256('message', 'secret').toString(CryptoJS.enc.Hex);
  out.hexOfHash = CryptoJS.enc.Hex.stringify(CryptoJS.MD5('kumobox'));
  out.subModule = require('crypto-js/md5')('kumobox').toString();
  const enc = CryptoJS.AES.encrypt('secret text', CryptoJS.enc.Utf8.parse('0123456789abcdef'), { iv: CryptoJS.enc.Utf8.parse('fedcba9876543210'), mode: CryptoJS.mode.CBC, padding: CryptoJS.pad.Pkcs7 });
  out.aes = CryptoJS.AES.decrypt(enc.toString(), CryptoJS.enc.Utf8.parse('0123456789abcdef'), { iv: CryptoJS.enc.Utf8.parse('fedcba9876543210') }).toString(CryptoJS.enc.Utf8);
  // misc
  out.text = new TextDecoder().decode(new TextEncoder().encode('héllo'));
  out.globals = typeof global + ' ' + typeof process.env + ' ' + typeof atob + ' ' + typeof URL + ' ' + typeof Buffer;
  return out;
}
module.exports = { getStreams };
`)
	var out map[string]any
	nuvioResult(t, rt, &out)
	md := md5.Sum([]byte("kumobox"))
	sh := sha256.Sum256([]byte("kumobox"))
	mac := hmac.New(sha256.New, []byte("secret"))
	mac.Write([]byte("message"))
	want := map[string]any{
		"headers":       "1, 2|false",
		"fetchEcho":     "POST x=1 q=a+b&n=2 application/x-www-form-urlencoded;charset=UTF-8",
		"respHeaders":   "application/json|yes|a=1, b=2",
		"notFound":      `false 404 {"error":"nope"}`,
		"clone":         "ok",
		"badJSON":       "SyntaxError",
		"abort":         "AbortError",
		"axiosGet":      "200 GET q=x%20y&list%5B%5D=1&list%5B%5D=2 Ax yes",
		"axiosPost":     `{"hello":"world"} application/json`,
		"axiosInstance": "Bearer t added=1",
		"axiosErr":      "true 404 nope Request failed with status code 404",
		"validate":      float64(404),
		"axiosText":     "string",
		"md5":           hex.EncodeToString(md[:]),
		"sha256":        base64.StdEncoding.EncodeToString(sh[:]),
		"hmac":          hex.EncodeToString(mac.Sum(nil)),
		"hexOfHash":     hex.EncodeToString(md[:]),
		"subModule":     hex.EncodeToString(md[:]),
		"aes":           "secret text",
		"text":          "héllo",
		"globals":       "object object function function function",
	}
	for k, w := range want {
		if !reflect.DeepEqual(out[k], w) {
			t.Errorf("%s = %#v, want %#v", k, out[k], w)
		}
	}
}
