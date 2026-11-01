package parental

import (
	"context"
	"encoding/json"
	"testing"
)

func TestReason(t *testing.T) {
	r := &Rules{NoAdult: true, Genres: []string{"Horror"}, Blocked: []int{5}}
	cases := []struct {
		m    Media
		want string
	}{
		{Media{ID: 1, Genres: []string{"Action"}}, ""},
		{Media{ID: 2, Adult: true}, "18+"},
		{Media{ID: 3, Genres: []string{"horror", "Drama"}}, "horror"},
		{Media{ID: 4, Genres: []string{"Hentai"}}, "18+"},
		{Media{ID: 6, Genres: []string{"Comedy", "ecchi"}}, "18+"},
		{Media{ID: 7, Genres: []string{"Romance"}, Mature: true}, "18+"},
		{Media{ID: 5}, "blocked"},
	}
	for _, c := range cases {
		if got := r.Reason(c.m); got != c.want {
			t.Errorf("%+v: %q, want %q", c.m, got, c.want)
		}
	}
	only := &Rules{OnlyAllowed: true, Allowed: []int{7}, NoAdult: true}
	if only.Reason(Media{ID: 7, Adult: true}) != "" || only.Reason(Media{ID: 8}) != "not allowed" {
		t.Error("an allow-list: only what's on it, whatever it is")
	}
	if (&Rules{}).Active() || (*Rules)(nil).Reason(Media{ID: 1, Adult: true}) != "" {
		t.Error("no rules block nothing")
	}
}

func TestMergeAndPIN(t *testing.T) {
	m := Merge([]Rules{
		{NoAdult: true, Blocked: []int{3}},
		{Genres: []string{"Ecchi"}, OnlyAllowed: true, Allowed: []int{1, 2, 3}},
		{OnlyAllowed: true, Allowed: []int{2, 3, 4}},
		{},
	})
	if m == nil || !m.NoAdult || len(m.Genres) != 1 || len(m.Blocked) != 1 || !m.OnlyAllowed || len(m.Allowed) != 2 || m.Allowed[0] != 2 {
		t.Fatalf("merged: %+v", m)
	}
	if Merge([]Rules{{}, {PIN: "x"}}) != nil {
		t.Error("nothing restricted, nothing merged")
	}
	h := HashPIN("4821")
	if !CheckPIN(h, "4821") || CheckPIN(h, "4822") || CheckPIN("", "4821") || HashPIN("4821") == h {
		t.Errorf("pin %q", h)
	}
}

// A blocked anime goes from every list it's in, and its page is blocked.
func TestFilter(t *testing.T) {
	f := Filter{
		Rules: &Rules{NoAdult: true, Genres: []string{"Horror"}},
		Lookup: func(_ context.Context, ids []int) map[int]Media {
			out := map[int]Media{}
			for _, id := range ids {
				switch id {
				case 3:
					out[id] = Media{ID: 3, Genres: []string{"Romance"}, Mature: true, Known: true}
				case 30:
					out[id] = Media{ID: 30, Adult: true, Known: true}
				case 40:
					out[id] = Media{ID: 40, Genres: []string{"Horror"}, Known: true}
				default:
					out[id] = Media{ID: id, Genres: []string{"Comedy"}, Known: true}
				}
			}
			return out
		},
	}
	var v any
	_ = json.Unmarshal([]byte(`{
		"lists": [{"name": "Watching", "items": [
			{"id": 900, "mediaId": 1, "media": {"id": 1, "isAdult": false, "genres": ["Comedy"]}},
			{"id": 901, "mediaId": 2, "media": {"id": 2, "isAdult": true, "genres": ["Romance"]}},
			{"id": 902, "mediaId": 3, "media": {"id": 3, "isAdult": false, "genres": ["Romance"]}}
		]}],
		"files": [{"path": "/a.mkv", "mediaId": 30}, {"path": "/b.mkv", "mediaId": 31}, {"path": "/c.mkv", "mediaId": 0}],
		"relations": [{"relationType": "SEQUEL", "node": {"id": 40, "isAdult": false}}, {"relationType": "PREQUEL", "node": {"id": 41, "isAdult": false}}],
		"recent": [{"mediaId": 40, "episode": 3}]
	}`), &v)
	out, why := f.Apply(context.Background(), v)
	if why != "" {
		t.Fatalf("blocked whole: %s", why)
	}
	got, _ := json.Marshal(out)
	want := `{"files":[{"mediaId":31,"path":"/b.mkv"},{"mediaId":0,"path":"/c.mkv"}],"lists":[{"items":[{"id":900,"media":{"genres":["Comedy"],"id":1,"isAdult":false},"mediaId":1}],"name":"Watching"}],"recent":[],"relations":[{"node":{"id":41,"isAdult":false},"relationType":"PREQUEL"}]}`
	if string(got) != want {
		t.Fatalf("filtered:\n%s\nwant\n%s", got, want)
	}

	// An anime's page.
	_ = json.Unmarshal([]byte(`{"media": {"id": 2, "isAdult": true, "genres": []}, "listEntry": null}`), &v)
	if _, why := f.Apply(context.Background(), v); why != "18+" {
		t.Fatalf("a blocked anime's page: %q", why)
	}
	// No rules: as it was.
	if _, why := (Filter{}).Apply(context.Background(), v); why != "" {
		t.Fatal("no rules blocked something")
	}
}
