package api

import (
	"bufio"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/app"
	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/stream"
)

// liveLine is a line of GET /api/providers/streams/live.
type liveLine struct {
	Asking   []stream.Asked  `json:"asking"`
	Provider string          `json:"provider"`
	Streams  []stream.Stream `json:"streams"`
	Error    string          `json:"error"`
	Cached   bool            `json:"cached"`
	Done     bool            `json:"done"`
	Result   *streamsResponse
}

func liveLines(t *testing.T, body string) []liveLine {
	t.Helper()
	var out []liveLine
	sc := bufio.NewScanner(strings.NewReader(body))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var l liveLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			t.Fatalf("line %q: %v", sc.Text(), err)
		}
		out = append(out, l)
	}
	return out
}

// The live route writes who's asked, each answer, then the whole result
// (as /api/providers/streams has it), one JSON object a line.
func TestProviderStreamsLive(t *testing.T) {
	s := newProvidersServer(t)
	if w := do(s, "GET", "/api/providers/streams/live?mediaId=155", local, "", nil); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "no streaming provider") {
		t.Fatalf("no providers: %d %s", w.Code, w.Body.String())
	}
	installTestAddon(t, s)

	for i, remote := range []string{local, lan} {
		w := do(s, "GET", "/api/providers/streams/live?mediaId=155", remote, "", nil)
		if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/x-ndjson" {
			t.Fatalf("live: %d %s", w.Code, w.Header().Get("Content-Type"))
		}
		lines := liveLines(t, w.Body.String())
		if len(lines) != 3 {
			t.Fatalf("%d lines: %s", len(lines), w.Body.String())
		}
		if len(lines[0].Asking) != 1 || lines[0].Asking[0].Provider != "stremio:test.addon" {
			t.Errorf("asking: %+v", lines[0])
		}
		ans := lines[1]
		if ans.Provider != "stremio:test.addon" || len(ans.Streams) != 3 || ans.Cached != (i == 1) {
			t.Errorf("answer %d: %+v", i, ans)
		}
		done := lines[2]
		if !done.Done || done.Result == nil || len(done.Result.Streams) != 3 || done.Result.Streams[0].Kind != stream.KindDirect || len(done.Result.Subtitles) != 1 {
			t.Fatalf("done: %+v", done)
		}
	}
	if w := do(s, "GET", "/api/providers/streams/live?mediaId=155&provider=nope", local, "", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown provider: %d", w.Code)
	}
}

// A provider's Test button (this computer only) asks it right away, Night
// of the Living Dead by default; health lists every provider with how its
// searches went.
func TestProviderHealthAndTest(t *testing.T) {
	s := newProvidersServer(t)
	addon := installTestAddon(t, s)
	const id = "stremio:test.addon"
	if w := do(s, "POST", "/api/providers/"+id+"/test", lan, "", nil); w.Code != http.StatusForbidden {
		t.Fatalf("test from the LAN: %d", w.Code)
	}
	var tested struct {
		Provider, Title, Error string
		Streams, Direct        int
	}
	decodeBody(t, do(s, "POST", "/api/providers/"+id+"/test", local, "", nil), &tested)
	if tested.Provider != id || tested.Title != "Night of the Living Dead (1968)" || tested.Streams != 0 || tested.Error != "" || !addon.asked("/stream/movie/tt0063350.json") {
		t.Fatalf("test: %+v", tested)
	}
	decodeBody(t, do(s, "POST", "/api/providers/"+id+"/test", local, `{"mediaId":155}`, nil), &tested)
	if tested.Title != "The Dark Knight (2008)" || tested.Streams != 3 || tested.Direct != 1 {
		t.Fatalf("test with a title: %+v", tested)
	}
	if w := do(s, "POST", "/api/providers/nope/test", local, "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("unknown provider: %d", w.Code)
	}

	// Searches count too; devices on the LAN may read the health.
	do(s, "GET", "/api/providers/streams?mediaId="+strconv.Itoa(catalog.ShowID(1396))+"&episode=1002", local, "", nil)
	var health []app.ProviderHealth
	decodeBody(t, do(s, "GET", "/api/providers/health", lan, "", nil), &health)
	n := 1 + len(streamSites) // the Internet Archive and the streaming sites, off
	if len(health) != n+1 || health[0].ID != "archive" || health[0].Successes != 0 || health[n].ID != id ||
		health[n].Successes != 3 || health[n].Demoted || !health[n].Enabled || len(health[n].Latencies) != 3 {
		t.Fatalf("health: %+v", health)
	}
}

// The version of a show that worked (played past its first seconds) comes
// first for its next episodes; the progress reports of the in-app player
// say which stream plays.
func TestProviderRemembersVersion(t *testing.T) {
	s := newProvidersServer(t)
	installTestAddon(t, s)
	show := catalog.ShowID(1396)
	report := map[string]any{"mediaId": show, "episode": 1001, "position": 4, "duration": 2800, "source": "stream",
		"provider": "stremio:test.addon", "quality": "720p", "release": "Breaking.Bad.S01E01.720p.WEB-DL-GRP"}
	do(s, "POST", "/api/playback/progress", local, jsonBody(t, report), nil)
	report["position"] = 30
	do(s, "POST", "/api/playback/progress", local, jsonBody(t, report), nil)
	deadline := time.Now().Add(5 * time.Second)
	for s.app.Stream.Versions(show) == nil {
		if time.Now().After(deadline) {
			t.Fatal("the version wasn't remembered")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if v := s.app.Stream.Versions(show); v.Provider != "stremio:test.addon" || v.Quality != "720p" || v.Group != "GRP" {
		t.Fatalf("version: %+v", v)
	}
	var res struct {
		Streams   []stream.Stream `json:"streams"`
		Preferred *stream.Version `json:"preferred"`
	}
	decodeBody(t, do(s, "GET", "/api/providers/streams?mediaId="+strconv.Itoa(show)+"&episode=1002", local, "", nil), &res)
	if res.Preferred == nil || len(res.Streams) != 1 || !res.Streams[0].Preferred {
		t.Fatalf("streams: %+v", res)
	}
}
