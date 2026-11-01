package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/anime/internal/anilist"
	"github.com/v0-0x/mediaserver/anime/internal/backup"
	"github.com/v0-0x/mediaserver/anime/internal/watchorder"
)

// The watch order through the API, from AniList's links as Aniko keeps
// them (no AniList here): Aniko's order, yours saved and applied from
// another anime of the same franchise, a backup keeping it, and reset.
func TestWatchOrderRoutes(t *testing.T) {
	s := newTestServer(t)
	local := "127.0.0.1:5000"
	year := func(y, m int) anilist.FuzzyDate { return anilist.FuzzyDate{Year: &y, Month: &m} }
	keep := func(id int, d anilist.FuzzyDate, links ...anilist.Link) {
		l := anilist.Linked{Media: &anilist.Media{ID: id, Type: "ANIME", Format: "TV", StartDate: d, Title: anilist.Title{Romaji: "Show " + strconv.Itoa(id)}}, Links: links}
		s.app.DB.SetCache("links:"+strconv.Itoa(id), l, time.Hour)
	}
	keep(101, year(2013, 4), anilist.Link{ID: 102, Type: "SEQUEL"}, anilist.Link{ID: 100, Type: "PREQUEL"})
	keep(102, year(2017, 4), anilist.Link{ID: 101, Type: "PREQUEL"})
	keep(100, year(2019, 1), anilist.Link{ID: 101, Type: "SEQUEL"})

	get := func(id int) watchorder.Order {
		t.Helper()
		w := do(s, "GET", "/api/anime/"+strconv.Itoa(id)+"/watch-order", local, "", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("watch order: %d %s", w.Code, w.Body.String())
		}
		var o watchorder.Order
		if err := json.Unmarshal(w.Body.Bytes(), &o); err != nil {
			t.Fatal(err)
		}
		return o
	}
	story := func(o watchorder.Order) (out []int) {
		for _, e := range o.Story {
			out = append(out, e.Media.ID)
		}
		return out
	}
	o := get(102)
	if got := story(o); len(got) != 3 || got[0] != 100 || got[1] != 101 || got[2] != 102 || o.Key != 100 || o.Edited {
		t.Fatalf("Aniko's order = %v (key %d, edited %v), want [100 101 102], key 100", got, o.Key, o.Edited)
	}

	if w := do(s, "PUT", "/api/watch-order/100", local, `{"order":[101,102,100],"hidden":[100]}`, nil); w.Code != http.StatusOK {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	o = get(101) // another anime of the franchise: the same edit
	if got := story(o); got[0] != 101 || got[1] != 102 || got[2] != 100 || !o.Edited || !o.Story[2].Hidden {
		t.Fatalf("your order = %v (edited %v, hidden %v), want [101 102 100] with 100 hidden", got, o.Edited, o.Story[2].Hidden)
	}

	f, err := backup.Make(s.app.DB, s.app.Settings.Get(), false, "test", "PC")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.KV["watch-order:100"]; !ok {
		t.Error("a backup leaves out your watch order")
	}

	if w := do(s, "DELETE", "/api/watch-order/100", local, "", nil); w.Code != http.StatusOK {
		t.Fatalf("reset: %d %s", w.Code, w.Body.String())
	}
	if o := get(102); o.Edited || story(o)[0] != 100 {
		t.Fatalf("after reset: %v (edited %v)", story(o), o.Edited)
	}

	for _, bad := range []string{`{"order":"x"}`, `{"order":[` + func() string {
		b := "1"
		for i := 0; i < 1000; i++ {
			b += ",1"
		}
		return b
	}() + `]}`} {
		if w := do(s, "PUT", "/api/watch-order/100", local, bad, nil); w.Code != http.StatusBadRequest {
			t.Errorf("bad edit: %d, want 400", w.Code)
		}
	}
	if w := do(s, "PUT", "/api/watch-order/0", local, `{}`, nil); w.Code != http.StatusBadRequest {
		t.Errorf("key 0: %d, want 400", w.Code)
	}
}
