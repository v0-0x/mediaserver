package watchorder

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/v0-0x/mediaserver/anime/internal/anilist"
)

func date(y, m, d int) anilist.FuzzyDate {
	var fd anilist.FuzzyDate
	if y > 0 {
		fd.Year = &y
	}
	if m > 0 {
		fd.Month = &m
	}
	if d > 0 {
		fd.Day = &d
	}
	return fd
}

type graph map[int]*anilist.Linked

func (g graph) add(id int, format string, d anilist.FuzzyDate, links ...anilist.Link) {
	g[id] = &anilist.Linked{Media: &anilist.Media{ID: id, Format: format, StartDate: d}, Links: links}
}

func link(id int, typ string) anilist.Link { return anilist.Link{ID: id, Type: typ} }

func ids(es []Entry) []int {
	out := []int{}
	for _, e := range es {
		out = append(out, e.Media.ID)
	}
	return out
}

func find(es []Entry, id int) Entry {
	for _, e := range es {
		if e.Media.ID == id {
			return e
		}
	}
	return Entry{}
}

// A show whose prequel movie came out after its first season (Jujutsu
// Kaisen 0), with a side-story OVA and a recap.
func jjk() graph {
	g := graph{}
	g.add(10, "TV", date(2020, 10, 3), link(20, "SEQUEL"), link(5, "PREQUEL"), link(30, "SIDE_STORY"), link(40, "SUMMARY"), link(900, "SPIN_OFF"))
	g.add(20, "TV", date(2023, 7, 6), link(10, "PREQUEL"))
	g.add(5, "MOVIE", date(2021, 12, 24), link(10, "SEQUEL"))
	g.add(30, "OVA", date(2021, 3, 1), link(10, "PARENT"))
	g.add(40, "SPECIAL", date(2021, 2, 1))
	return g
}

func TestStoryAndReleaseOrder(t *testing.T) {
	o := Compute(jjk())
	// The prequel movie first though released later; then the season,
	// then what follows it by release date.
	if got, want := ids(o.Story), []int{5, 10, 40, 30, 20}; !reflect.DeepEqual(got, want) {
		t.Errorf("story = %v, want %v", got, want)
	}
	if got, want := ids(o.Release), []int{10, 40, 30, 5, 20}; !reflect.DeepEqual(got, want) {
		t.Errorf("release = %v, want %v", got, want)
	}
	if o.Key != 5 {
		t.Errorf("key = %d, want the smallest ID 5", o.Key)
	}
	if k := find(o.Story, 40).Kind; k != Recap {
		t.Errorf("the summary is %q, want recap", k)
	}
	if k := find(o.Story, 30).Kind; k != Side {
		t.Errorf("the OVA is %q, want side", k)
	}
	if k := find(o.Story, 5).Kind; k != Main {
		t.Errorf("the prequel movie is %q, want main", k)
	}
	for _, e := range o.Story {
		if e.ByDate || e.Conflict {
			t.Errorf("%d: byDate %v, conflict %v; every one is linked, none conflicts", e.Media.ID, e.ByDate, e.Conflict)
		}
	}
}

// Links that contradict each other (each says the other comes first):
// the earlier release goes first, marked, and nothing is lost.
func TestContradictingLinks(t *testing.T) {
	g := graph{}
	g.add(1, "TV", date(2009, 7, 3), link(2, "SEQUEL"), link(2, "PREQUEL"))
	g.add(2, "MOVIE", date(2016, 1, 8), link(1, "SEQUEL"), link(3, "SEQUEL"))
	g.add(3, "TV", date(2012, 7, 7), link(2, "PREQUEL"))
	o := Compute(g)
	if got := ids(o.Story); len(got) != 3 {
		t.Fatalf("story = %v, want all three", got)
	}
	if !o.Story[0].Conflict || o.Story[0].Media.ID != 1 {
		t.Errorf("first = %d (conflict %v), want 1 marked", o.Story[0].Media.ID, o.Story[0].Conflict)
	}
	if got, want := ids(o.Story), []int{1, 2, 3}; !reflect.DeepEqual(got, want) {
		t.Errorf("story = %v, want %v", got, want)
	}
}

// An anime only contained in another one, with no story link: placed by
// its release date, and says so.
func TestPlacedByDate(t *testing.T) {
	g := graph{}
	g.add(1, "TV", date(2015, 4, 1), link(2, "SEQUEL"), link(3, "CONTAINS"))
	g.add(2, "TV", date(2018, 4, 1), link(1, "PREQUEL"))
	g.add(3, "SPECIAL", date(2016, 1, 1))
	o := Compute(g)
	if got, want := ids(o.Story), []int{1, 3, 2}; !reflect.DeepEqual(got, want) {
		t.Errorf("story = %v, want %v", got, want)
	}
	if !find(o.Story, 3).ByDate || find(o.Story, 1).ByDate {
		t.Errorf("byDate: special %v, season %v; want only the special", find(o.Story, 3).ByDate, find(o.Story, 1).ByDate)
	}
}

func TestDates(t *testing.T) {
	a := &anilist.Media{ID: 1, StartDate: date(2020, 0, 0)}  // 2020, month not known
	b := &anilist.Media{ID: 2, StartDate: date(2020, 12, 1)} // a known date that year
	c := &anilist.Media{ID: 3}                               // no date at all
	if !before(b, a) || !before(a, c) || before(c, b) {
		t.Error("known dates go before a year alone, which goes before no date")
	}
}

// The walk: a level at a time, links outside the story listed apart and
// not walked, manga never asked about.
func TestBuild(t *testing.T) {
	g := jjk()
	g.add(900, "TV", date(2024, 1, 1), link(901, "SEQUEL"))
	g.add(901, "TV", date(2025, 1, 1))
	var asked [][]int
	fetch := func(ctx context.Context, ids []int) (map[int]*anilist.Linked, error) {
		asked = append(asked, append([]int(nil), ids...))
		out := map[int]*anilist.Linked{}
		for _, id := range ids {
			if l := g[id]; l != nil {
				out[id] = l
			}
		}
		return out, nil
	}
	o, err := Build(context.Background(), 20, fetch)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ids(o.Story), []int{5, 10, 40, 30, 20}; !reflect.DeepEqual(got, want) {
		t.Errorf("story from season 2 = %v, want %v", got, want)
	}
	if got := ids(o.Extras); !reflect.DeepEqual(got, []int{900}) || o.Extras[0].Relation != "SPIN_OFF" {
		t.Errorf("extras = %v (%q), want the spin-off alone, not its sequel", got, o.Extras[0].Relation)
	}
	// Season 2, then season 1, then the rest of the story, then the
	// spin-off: four requests, none for the spin-off's sequel.
	if len(asked) != 4 {
		t.Errorf("asked %v, want 4 requests", asked)
	}
	for _, a := range asked {
		for _, id := range a {
			if id == 901 {
				t.Error("the spin-off's sequel was walked to")
			}
		}
	}
	if o.Partial {
		t.Error("partial, though nothing was cut")
	}
}

// A franchise bigger than Max is cut there, and says so.
func TestBuildCap(t *testing.T) {
	g := graph{}
	for i := 1; i <= Max+30; i++ {
		var links []anilist.Link
		if i > 1 {
			links = append(links, link(i-1, "PREQUEL"))
		}
		links = append(links, link(i+1, "SEQUEL"))
		g.add(i, "MOVIE", date(1990+i/12, i%12+1, 1), links...)
	}
	fetch := func(ctx context.Context, ids []int) (map[int]*anilist.Linked, error) {
		out := map[int]*anilist.Linked{}
		for _, id := range ids {
			if l := g[id]; l != nil {
				out[id] = l
			}
		}
		return out, nil
	}
	o, err := Build(context.Background(), 1, fetch)
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Story) != Max || !o.Partial {
		t.Errorf("%d anime, partial %v; want %d and partial", len(o.Story), o.Partial, Max)
	}
}

// AniList failing: nothing for the anime itself is an error; a failure
// further out gives what was found, marked partial.
func TestBuildErrors(t *testing.T) {
	g := jjk()
	boom := errors.New("AniList is down")
	if _, err := Build(context.Background(), 10, func(ctx context.Context, ids []int) (map[int]*anilist.Linked, error) { return nil, boom }); !errors.Is(err, boom) {
		t.Errorf("err = %v, want AniList's", err)
	}
	calls := 0
	o, err := Build(context.Background(), 10, func(ctx context.Context, ids []int) (map[int]*anilist.Linked, error) {
		calls++
		if calls > 1 {
			return nil, boom
		}
		return map[int]*anilist.Linked{10: g[10]}, nil
	})
	if err != nil || !o.Partial || len(o.Story) != 1 {
		t.Errorf("got %v, %v; want season 1 alone, partial", o, err)
	}
	if _, err := Build(context.Background(), 77, func(ctx context.Context, ids []int) (map[int]*anilist.Linked, error) { return nil, nil }); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown anime: err = %v", err)
	}
}

// Your order: kept, with an anime added since put after the one before
// it, unknown IDs dropped, and what you hid marked in both orders.
func TestApply(t *testing.T) {
	o := Compute(jjk())
	Apply(o, Edit{Order: []int{10, 20, 5, 999, 40}, Hidden: []int{40}})
	if got, want := ids(o.Story), []int{10, 20, 5, 40, 30}; !reflect.DeepEqual(got, want) {
		t.Errorf("story = %v, want %v", got, want)
	}
	if !o.Edited || !find(o.Story, 40).Hidden || !find(o.Release, 40).Hidden || find(o.Story, 10).Hidden {
		t.Error("the recap should be hidden in both orders, the season not")
	}
	// Nothing saved: nothing changes.
	o2 := Compute(jjk())
	Apply(o2, Edit{})
	if o2.Edited || !reflect.DeepEqual(ids(o2.Story), []int{5, 10, 40, 30, 20}) {
		t.Error("an empty edit changed the order")
	}
	// An anime new since the edit, whose place comes first: at the start.
	o3 := Compute(jjk())
	Apply(o3, Edit{Order: []int{20, 10, 40, 30}})
	if got, want := ids(o3.Story), []int{5, 20, 10, 40, 30}; !reflect.DeepEqual(got, want) {
		t.Errorf("story = %v, want %v", got, want)
	}
}
