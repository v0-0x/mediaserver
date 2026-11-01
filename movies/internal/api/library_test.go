package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/library"
)

const libraryTestKey = "0123456789abcdef0123456789abcdef"

// newLibraryServer is a server whose TMDB knows a movie (Heat, 949) and a
// show of seasons of 25, 12 and 22 episodes (Attack on Titan, 1429).
func newLibraryServer(t *testing.T) *Server {
	t.Helper()
	tmdb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/movie/949":
			fmt.Fprint(w, `{"id":949,"title":"Heat","release_date":"1995-12-15","status":"Released"}`)
		case "/tv/1429":
			fmt.Fprint(w, `{"id":1429,"name":"Attack on Titan","first_air_date":"2013-04-07","status":"Ended","seasons":[
				{"season_number":1,"episode_count":25},{"season_number":2,"episode_count":12},{"season_number":3,"episode_count":22}]}`)
		case "/genre/movie/list", "/genre/tv/list":
			fmt.Fprint(w, `{"genres":[]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"status_code":34}`)
		}
	}))
	t.Cleanup(tmdb.Close)
	t.Setenv("KUMOBOX_TMDB_URL", tmdb.URL)
	s := newTestServer(t)
	cfg := s.app.Settings.Get()
	cfg.TMDB.APIKey = libraryTestKey
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	return s
}

func saveFiles(t *testing.T, s *Server, root string, names ...string) []string {
	t.Helper()
	var paths []string
	for _, n := range names {
		p := filepath.Join(root, filepath.FromSlash(n))
		f := &library.LocalFile{Path: p, Dir: filepath.Dir(p), Name: filepath.Base(p), Parsed: library.Parse(p, []string{root})}
		f.Kind = f.Parsed.Kind
		if err := s.app.Files.Save(f); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	return paths
}

func TestLibraryMatchByHand(t *testing.T) {
	s := newLibraryServer(t)
	root := t.TempDir() // files only in the index: no scan touches them
	const local = "127.0.0.1:5000"
	post := func(path string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		return do(s, "POST", path, local, string(raw), nil)
	}
	keys := func(paths []string) string {
		var out []string
		for _, p := range paths {
			f, err := s.app.Files.Get(p)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, fmt.Sprintf("%d/%d/%s/%v", f.MediaID, f.Episode, f.Kind, f.Locked))
		}
		return strings.Join(out, " ")
	}
	aot := catalog.ShowID(1429)

	// Numbers counted from the first episode.
	files := saveFiles(t, s, root, "AoT/AoT - 26.mkv", "AoT/AoT - 38.mkv")
	if w := post("/api/library/match", map[string]any{"paths": files, "mediaId": aot, "absolute": true}); w.Code != 200 {
		t.Fatalf("match: %d %s", w.Code, w.Body)
	}
	if got, want := keys(files), fmt.Sprintf("%d/2001/main/true %d/3001/main/true", aot, aot); got != want {
		t.Errorf("absolute: %s, want %s", got, want)
	}
	// Episodes of a season.
	files = saveFiles(t, s, root, "AoT S3/AoT - 01.mkv", "AoT S3/AoT - 02.mkv")
	if w := post("/api/library/match", map[string]any{"paths": files, "mediaId": aot, "season": 3}); w.Code != 200 {
		t.Fatalf("match: %d %s", w.Code, w.Body)
	}
	if got, want := keys(files), fmt.Sprintf("%d/3001/main/true %d/3002/main/true", aot, aot); got != want {
		t.Errorf("season: %s, want %s", got, want)
	}
	// A movie in two parts, with its trailer.
	files = saveFiles(t, s, root, "Heat (1995)/Heat - CD1.avi", "Heat (1995)/Heat - CD2.avi", "Heat (1995)/Heat-trailer.mkv")
	if w := post("/api/library/match", map[string]any{"paths": files, "mediaId": 949}); w.Code != 200 {
		t.Fatalf("match: %d %s", w.Code, w.Body)
	}
	if got := keys(files); got != "949/1/main/true 949/1/part/true 949/0/extra/true" {
		t.Errorf("movie: %s", got)
	}

	// Fixing one file: its episode, its kind.
	patch := func(body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		return do(s, "PATCH", "/api/library/file", local, string(raw), nil)
	}
	aotFile := saveFiles(t, s, root, "AoT/AoT - 99.mkv")[0]
	if w := post("/api/library/match", map[string]any{"paths": []string{aotFile}, "mediaId": aot}); w.Code != 200 {
		t.Fatal(w.Body)
	}
	if w := patch(map[string]any{"path": aotFile, "episode": 2, "locked": true}); w.Code != 200 {
		t.Fatalf("patch: %d %s", w.Code, w.Body)
	}
	if got, want := keys([]string{aotFile}), fmt.Sprintf("%d/2/special/true", aot); got != want {
		t.Errorf("special: %s, want %s", got, want)
	}
	if w := patch(map[string]any{"path": aotFile, "kind": "nc"}); w.Code != 200 {
		t.Fatalf("patch: %d %s", w.Code, w.Body)
	}
	if got, want := keys([]string{aotFile}), fmt.Sprintf("%d/0/extra/true", aot); got != want {
		t.Errorf("extra: %s, want %s", got, want)
	}
	if w := patch(map[string]any{"path": aotFile, "kind": "bonus"}); w.Code != http.StatusBadRequest {
		t.Errorf("invalid kind: %d", w.Code)
	}

	// The Local library page.
	w := do(s, "GET", "/api/library/titles", local, "", nil)
	var titles []*library.LibraryTitle
	if err := json.Unmarshal(w.Body.Bytes(), &titles); err != nil || w.Code != 200 {
		t.Fatalf("titles: %d %s", w.Code, w.Body)
	}
	byID := map[int]*library.LibraryTitle{}
	for _, x := range titles {
		byID[x.Media.ID] = x
	}
	if x := byID[aot]; x == nil || x.Ranges != "S02E01, S03E01–E02" {
		t.Errorf("show: %+v", x)
	}
	if x := byID[949]; x == nil || x.Extras != 2 || x.Next == nil || !strings.HasSuffix(x.Next.Path, "CD1.avi") {
		t.Errorf("movie: %+v", x)
	}
}
