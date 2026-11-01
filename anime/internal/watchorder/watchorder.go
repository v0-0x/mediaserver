// Package watchorder finds the order to watch a franchise in: its seasons,
// movies, OVAs, specials and recaps, as AniList links them. Two orders: the
// story's (by AniList's prequel and sequel links, release dates where they
// say nothing) and the release order (by dates). You can change the story
// order; your changes are kept on this Aniko (Edit).
package watchorder

import (
	"container/heap"
	"context"
	"errors"
	"sort"
	"strconv"

	"github.com/v0-0x/mediaserver/anime/internal/anilist"
)

// Kinds of entry.
const (
	Main  = "main"  // a season, movie or OVA of the story
	Side  = "side"  // a side story, after the anime it belongs to
	Recap = "recap" // a recap or compilation of what came before
)

// Entry is an anime in an order.
type Entry struct {
	Media *anilist.Media `json:"media"`
	Kind  string         `json:"kind"`
	// Story order: no prequel/sequel link places it, so its release date
	// does.
	ByDate bool `json:"byDate,omitempty"`
	// Story order: AniList's links contradict each other here; its release
	// date decided.
	Conflict bool `json:"conflict,omitempty"`
	// Hidden by you (Edit).
	Hidden bool `json:"hidden,omitempty"`
	// Extras: how it's related (SPIN_OFF, ALTERNATIVE).
	Relation string `json:"relation,omitempty"`
}

// Order is a franchise's watch orders.
type Order struct {
	// The franchise's smallest AniList ID, the same from any of its
	// anime: what an Edit is kept under.
	Key     int     `json:"key"`
	Story   []Entry `json:"story"`
	Release []Entry `json:"release"`
	// Spin-offs and other versions: listed apart, not walked from.
	Extras []Entry `json:"extras"`
	// The franchise was cut at Max anime, or AniList stopped answering.
	Partial bool `json:"partial,omitempty"`
	// The story order is yours (Edit).
	Edited bool `json:"edited,omitempty"`
}

// Edit is your own story order and what you hid, kept on this Aniko.
type Edit struct {
	Order  []int `json:"order"`
	Hidden []int `json:"hidden"`
}

// EditKey is where a franchise's Edit is kept (the kv table).
func EditKey(key int) string { return "watch-order:" + strconv.Itoa(key) }

// Fetch gets anime with their links (anilist.Platform.Links).
type Fetch func(ctx context.Context, ids []int) (map[int]*anilist.Linked, error)

// Max is the most anime a franchise is walked to (a long-running one has
// dozens of movies and specials).
const Max = 120

// maxExtras caps the spin-offs and other versions listed.
const maxExtras = 40

// follow: the links that stay within the story, walked.
var follow = map[string]bool{"PREQUEL": true, "SEQUEL": true, "PARENT": true, "SIDE_STORY": true, "SUMMARY": true, "COMPILATION": true, "CONTAINS": true}

// aside: listed apart, not walked from (that way lies every Gundam).
var aside = map[string]bool{"SPIN_OFF": true, "ALTERNATIVE": true}

// ErrNotFound: AniList doesn't know the anime.
var ErrNotFound = errors.New("AniList doesn't know this anime")

// Build walks the franchise of the anime root, a level at a time (one
// request per 50 anime), and orders it.
func Build(ctx context.Context, root int, fetch Fetch) (*Order, error) {
	nodes := map[int]*anilist.Linked{}
	seen := map[int]bool{root: true}
	frontier := []int{root}
	partial := false
	for len(frontier) > 0 {
		got, err := fetch(ctx, frontier)
		for _, id := range frontier {
			if l := got[id]; l != nil && l.Media != nil {
				nodes[id] = l
			}
		}
		if err != nil {
			if nodes[root] == nil {
				return nil, err
			}
			partial = true
			break
		}
		var next []int
		for _, id := range frontier {
			l := nodes[id]
			if l == nil {
				continue
			}
			for _, k := range l.Links {
				if follow[k.Type] && !seen[k.ID] {
					seen[k.ID] = true
					next = append(next, k.ID)
				}
			}
		}
		if room := Max - len(nodes); len(next) > room {
			next, partial = next[:max(room, 0)], true
		}
		frontier = next
	}
	if nodes[root] == nil {
		return nil, ErrNotFound
	}
	o := Compute(nodes)
	o.Partial = partial

	// Spin-offs and other versions, by their release dates.
	relation := map[int]string{}
	var ids []int
	for _, id := range sortedIDs(nodes) {
		for _, k := range nodes[id].Links {
			if aside[k.Type] && nodes[k.ID] == nil && relation[k.ID] == "" && len(ids) < maxExtras {
				relation[k.ID] = k.Type
				ids = append(ids, k.ID)
			}
		}
	}
	if len(ids) > 0 {
		got, _ := fetch(ctx, ids)
		for _, id := range ids {
			if l := got[id]; l != nil && l.Media != nil {
				o.Extras = append(o.Extras, Entry{Media: l.Media, Kind: Main, Relation: relation[id]})
			}
		}
		sort.SliceStable(o.Extras, func(i, j int) bool { return before(o.Extras[i].Media, o.Extras[j].Media) })
	}
	return o, nil
}

// Compute orders a franchise's anime (each with its links; links to anime
// outside it are left out).
func Compute(nodes map[int]*anilist.Linked) *Order {
	o := &Order{Story: []Entry{}, Release: []Entry{}, Extras: []Entry{}}
	ids := sortedIDs(nodes)
	if len(ids) == 0 {
		return o
	}
	o.Key = ids[0]

	// What each one is: a recap, a side story or part of the story.
	story := map[int]bool{} // has prequel/sequel links within the franchise
	recapped, sided := map[int]bool{}, map[int]bool{}
	for _, id := range ids {
		for _, k := range nodes[id].Links {
			if nodes[k.ID] == nil || k.ID == id {
				continue
			}
			switch k.Type {
			case "PREQUEL", "SEQUEL":
				story[id], story[k.ID] = true, true
			case "SUMMARY", "COMPILATION":
				recapped[k.ID] = true
			case "SIDE_STORY":
				sided[k.ID] = true
			case "PARENT":
				sided[id] = true
			}
		}
	}
	kind := map[int]string{}
	for _, id := range ids {
		switch {
		case story[id]:
			kind[id] = Main
		case recapped[id]:
			kind[id] = Recap
		case sided[id]:
			kind[id] = Side
		default:
			kind[id] = Main
		}
	}

	// What comes before what in the story.
	after := map[int]map[int]bool{} // after[a][b]: a before b
	indeg := map[int]int{}
	linked := map[int]bool{}
	edge := func(a, b int) {
		if a == b {
			return
		}
		if after[a] == nil {
			after[a] = map[int]bool{}
		}
		if !after[a][b] {
			after[a][b] = true
			indeg[b]++
			linked[a], linked[b] = true, true
		}
	}
	for _, id := range ids {
		for _, k := range nodes[id].Links {
			if nodes[k.ID] == nil {
				continue
			}
			switch k.Type {
			case "SEQUEL":
				edge(id, k.ID)
			case "PREQUEL":
				edge(k.ID, id)
			case "SIDE_STORY":
				// Its own prequel/sequel links place it, if it has them.
				if kind[k.ID] == Side {
					edge(id, k.ID)
				}
			case "PARENT":
				if kind[id] == Side {
					edge(k.ID, id)
				}
			case "SUMMARY", "COMPILATION":
				if kind[k.ID] == Recap {
					edge(id, k.ID)
				}
			}
		}
	}

	// Kahn's: of what's free to go next, the earliest released. Where the
	// links go round in a circle, the earliest released of the rest goes
	// next, marked.
	ready := &byRelease{nodes: nodes}
	for _, id := range ids {
		if indeg[id] == 0 {
			heap.Push(ready, id)
		}
	}
	done := map[int]bool{}
	conflict := map[int]bool{}
	for len(done) < len(ids) {
		if ready.Len() == 0 {
			var pick int
			for _, id := range ids {
				if !done[id] && (pick == 0 || before(nodes[id].Media, nodes[pick].Media)) {
					pick = id
				}
			}
			conflict[pick] = true
			indeg[pick] = 0
			heap.Push(ready, pick)
		}
		id := heap.Pop(ready).(int)
		if done[id] {
			continue
		}
		done[id] = true
		o.Story = append(o.Story, Entry{Media: nodes[id].Media, Kind: kind[id], ByDate: !linked[id] && len(ids) > 1, Conflict: conflict[id]})
		for b := range after[id] {
			if done[b] {
				continue
			}
			if indeg[b]--; indeg[b] == 0 {
				heap.Push(ready, b)
			}
		}
	}

	for _, id := range ids {
		o.Release = append(o.Release, Entry{Media: nodes[id].Media, Kind: kind[id]})
	}
	sort.SliceStable(o.Release, func(i, j int) bool { return before(o.Release[i].Media, o.Release[j].Media) })
	return o
}

// Apply puts your Edit on an Order: the story in your order (anime added
// since go where Aniko would put them, after the one before them), and
// what you hid marked hidden in both orders.
func Apply(o *Order, e Edit) {
	if len(e.Order) == 0 && len(e.Hidden) == 0 {
		return
	}
	o.Edited = true
	byID := map[int]Entry{}
	for _, en := range o.Story {
		byID[en.Media.ID] = en
	}
	var out []int
	placed := map[int]bool{}
	for _, id := range e.Order {
		if _, ok := byID[id]; ok && !placed[id] {
			out = append(out, id)
			placed[id] = true
		}
	}
	for i, en := range o.Story {
		id := en.Media.ID
		if placed[id] {
			continue
		}
		at := 0 // the first one: at the start
		if i > 0 {
			prev := o.Story[i-1].Media.ID
			for j, x := range out {
				if x == prev {
					at = j + 1
					break
				}
			}
		}
		out = append(out[:at], append([]int{id}, out[at:]...)...)
		placed[id] = true
	}
	hidden := map[int]bool{}
	for _, id := range e.Hidden {
		hidden[id] = true
	}
	story := make([]Entry, 0, len(out))
	for _, id := range out {
		en := byID[id]
		en.Hidden = hidden[id]
		story = append(story, en)
	}
	o.Story = story
	for i := range o.Release {
		o.Release[i].Hidden = hidden[o.Release[i].Media.ID]
	}
}

// before: a was released before b; unknown dates last, then by ID.
func before(a, b *anilist.Media) bool {
	da, db := dateKey(a.StartDate), dateKey(b.StartDate)
	if da != db {
		return da < db
	}
	return a.ID < b.ID
}

// dateKey sorts dates: a month or day not known yet goes after the known
// ones of its year or month; no year at all, after everything.
func dateKey(d anilist.FuzzyDate) int {
	if d.Year == nil {
		return 1 << 30
	}
	m, day := 13, 32
	if d.Month != nil {
		m = *d.Month
		if d.Day != nil {
			day = *d.Day
		}
	}
	return *d.Year*10000 + m*100 + day
}

func sortedIDs(nodes map[int]*anilist.Linked) []int {
	ids := make([]int, 0, len(nodes))
	for id := range nodes {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

// byRelease is a heap of anime IDs, the earliest released first.
type byRelease struct {
	ids   []int
	nodes map[int]*anilist.Linked
}

func (h *byRelease) Len() int { return len(h.ids) }
func (h *byRelease) Less(i, j int) bool {
	return before(h.nodes[h.ids[i]].Media, h.nodes[h.ids[j]].Media)
}
func (h *byRelease) Swap(i, j int) { h.ids[i], h.ids[j] = h.ids[j], h.ids[i] }
func (h *byRelease) Push(x any)    { h.ids = append(h.ids, x.(int)) }
func (h *byRelease) Pop() any {
	n := len(h.ids)
	x := h.ids[n-1]
	h.ids = h.ids[:n-1]
	return x
}
