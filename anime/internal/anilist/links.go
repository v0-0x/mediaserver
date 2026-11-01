package anilist

import (
	"context"
	"strconv"
	"time"
)

// Link is one of an anime's relations as the watch order reads them: the
// other anime and how it relates (relationType version 2: PREQUEL, SEQUEL,
// PARENT, SIDE_STORY, SUMMARY, ALTERNATIVE, SPIN_OFF...).
type Link struct {
	ID   int    `json:"id"`
	Type string `json:"type"`
}

// Linked is an anime with its links to other anime (to manga and the
// like: left out).
type Linked struct {
	Media *Media `json:"media"`
	Links []Link `json:"links"`
}

const qLinks = `query ($ids: [Int]) {
	Page(perPage: 50) {
		media(id_in: $ids, type: ANIME) {
			` + mediaFields + `
			relations { edges { relationType(version: 2) node { id type } } }
		}
	}
}`

// Links fetches anime with their links, 50 to a request, and keeps them a
// week (refresh: asked again). One AniList doesn't know: missing.
func (p *Platform) Links(ctx context.Context, ids []int, refresh bool) (map[int]*Linked, error) {
	out := map[int]*Linked{}
	var missing []int
	for _, id := range ids {
		var l Linked
		if !refresh && p.db.GetCache(linksKey(id), &l) && l.Media != nil {
			out[id] = &l
		} else {
			missing = append(missing, id)
		}
	}
	for len(missing) > 0 {
		chunk := missing
		if len(chunk) > 50 {
			chunk = chunk[:50]
		}
		missing = missing[len(chunk):]
		var res struct {
			Page struct {
				Media []struct {
					Media
					Relations struct {
						Edges []struct {
							RelationType string `json:"relationType"`
							Node         struct {
								ID   int    `json:"id"`
								Type string `json:"type"`
							} `json:"node"`
						} `json:"edges"`
					} `json:"relations"`
				} `json:"media"`
			} `json:"Page"`
		}
		if err := p.client.QueryAnon(ctx, qLinks, map[string]any{"ids": chunk}, &res); err != nil {
			return out, err
		}
		for _, m := range res.Page.Media {
			l := &Linked{Media: m.Media.Lite(), Links: []Link{}}
			for _, e := range m.Relations.Edges {
				if e.Node.Type == "ANIME" && e.Node.ID > 0 {
					l.Links = append(l.Links, Link{ID: e.Node.ID, Type: e.RelationType})
				}
			}
			out[m.ID] = l
			p.db.SetCache(linksKey(m.ID), l, 7*24*time.Hour)
		}
	}
	return out, nil
}

func linksKey(id int) string { return "links:" + strconv.Itoa(id) }
