package catalog

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

// FindByTVDB returns the title TheTVDB knows by that id, which Plex,
// Jellyfin and Emby put in folder names ("The Wire {tvdb-79126}"): a show,
// unless kind is TypeMovie (TheTVDB numbers its movies apart from its
// shows, so one id may be both).
func (p *Platform) FindByTVDB(ctx context.Context, tvdbID int, kind string) (*Media, error) {
	if tvdbID <= 0 {
		return nil, ErrNotFound
	}
	if kind != TypeMovie {
		kind = TypeTV
	}
	key := fmt.Sprintf("find:tvdb:%s:%d", kind, tvdbID)
	var id int
	if p.db.GetCache(key, &id) {
		if id == 0 {
			return nil, ErrNotFound
		}
		return p.MediaLite(ctx, id)
	}
	var res tmdbFind
	q := url.Values{"external_source": {"tvdb_id"}}
	if err := p.client.get(ctx, "/find/"+strconv.Itoa(tvdbID), q, &res); err != nil {
		return nil, err
	}
	mp := p.mapper(ctx, false)
	var m *Media
	switch {
	case kind == TypeMovie && len(res.MovieResults) > 0:
		m = mp.media(&res.MovieResults[0], TypeMovie)
	case len(res.TVResults) > 0:
		m = mp.media(&res.TVResults[0], TypeTV)
	case len(res.MovieResults) > 0:
		m = mp.media(&res.MovieResults[0], TypeMovie)
	}
	if m == nil {
		p.db.SetCache(key, 0, 24*time.Hour)
		return nil, ErrNotFound
	}
	p.db.SetCache(key, m.ID, findTTL)
	if full, err := p.MediaLite(ctx, m.ID); err == nil {
		return full, nil
	}
	return m, nil
}
