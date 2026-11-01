package mediaserver

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/v0-0x/mediaserver/movies/internal/providers"
)

// Jellyfin and Emby
//
// Movies and shows are listed with /Items (Recursive, Fields=ProviderIds,
// the user's own with /Users/{id}/Items) and matched on their provider ids
// (ProviderIds.Tmdb, else Imdb). A show's episodes are
// /Shows/{id}/Episodes?season=N. Each media source (a version of the
// movie or episode: another file) is a link to the file as it is:
// /Videos/{id}/stream?static=true&MediaSourceId=...

type jfItems struct {
	Items []jfItem `json:"Items"`
}

type jfItem struct {
	ID                string            `json:"Id"`
	Name              string            `json:"Name"`
	Type              string            `json:"Type"`
	ProductionYear    int               `json:"ProductionYear"`
	ProviderIDs       map[string]string `json:"ProviderIds"`
	IndexNumber       *int              `json:"IndexNumber"`
	IndexNumberEnd    *int              `json:"IndexNumberEnd"`
	ParentIndexNumber *int              `json:"ParentIndexNumber"`
	MediaSources      []jfMediaSource   `json:"MediaSources"`
}

type jfMediaSource struct {
	ID           string          `json:"Id"`
	Name         string          `json:"Name"`
	Path         string          `json:"Path"`
	Container    string          `json:"Container"`
	Size         int64           `json:"Size"`
	Bitrate      int64           `json:"Bitrate"`
	MediaStreams []jfMediaStream `json:"MediaStreams"`
}

type jfMediaStream struct {
	Type     string `json:"Type"`
	Codec    string `json:"Codec"`
	Height   int    `json:"Height"`
	Width    int    `json:"Width"`
	Channels int    `json:"Channels"`
	Language string `json:"Language"`
	// VideoRange is "SDR" or "HDR".
	VideoRange string `json:"VideoRange"`
}

// providerID is an item's id at a metadata provider ("Tmdb", "Imdb"),
// whatever the case of the key.
func (it jfItem) providerID(name string) string {
	for k, v := range it.ProviderIDs {
		if strings.EqualFold(k, name) {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// itemsPath is where the user's items (or all of them) are listed.
func (p *Provider) itemsPath() string {
	if p.cfg.UserID != "" {
		return "/Users/" + url.PathEscape(p.cfg.UserID) + "/Items"
	}
	return "/Items"
}

// jellyfinFind returns the movies or series (itemType) whose TMDB id is
// tmdbID, else whose IMDb id is imdbID.
func (p *Provider) jellyfinFind(ctx context.Context, itemType string, tmdbID int, imdbID string) ([]jfItem, error) {
	q := url.Values{}
	q.Set("Recursive", "true")
	q.Set("IncludeItemTypes", itemType)
	q.Set("Fields", "ProviderIds")
	var res jfItems
	if err := p.get(ctx, p.itemsPath(), q, listLimit, &res); err != nil {
		return nil, err
	}
	return matchItems(res.Items, tmdbID, imdbID), nil
}

// matchItems keeps the items with the TMDB id, else those with the IMDb
// id.
func matchItems(items []jfItem, tmdbID int, imdbID string) []jfItem {
	var byTMDB, byIMDb []jfItem
	tmdb := strconv.Itoa(tmdbID)
	for _, it := range items {
		switch {
		case tmdbID > 0 && it.providerID("Tmdb") == tmdb:
			byTMDB = append(byTMDB, it)
		case imdbID != "" && strings.EqualFold(it.providerID("Imdb"), imdbID):
			byIMDb = append(byIMDb, it)
		}
	}
	if len(byTMDB) > 0 {
		return byTMDB
	}
	return byIMDb
}

func (p *Provider) jellyfinStreams(ctx context.Context, r providers.Request) ([]providers.Stream, error) {
	if !r.IsTV() {
		movies, err := p.jellyfinFind(ctx, "Movie", r.TMDBID, r.IMDbID)
		if err != nil || len(movies) == 0 {
			return nil, err
		}
		ids := make([]string, len(movies))
		for i, m := range movies {
			ids[i] = m.ID
		}
		// Their files (media sources), asked for only now: listing them for
		// a whole library would be megabytes.
		q := url.Values{}
		q.Set("Ids", strings.Join(ids, ","))
		q.Set("Fields", "MediaSources,ProviderIds")
		var res jfItems
		if err := p.get(ctx, p.itemsPath(), q, itemLimit, &res); err != nil {
			return nil, err
		}
		return p.jellyfinSources(res.Items), nil
	}

	series, err := p.jellyfinFind(ctx, "Series", r.TMDBID, r.IMDbID)
	if err != nil || len(series) == 0 {
		return nil, err
	}
	var out []providers.Stream
	for _, s := range series {
		q := url.Values{}
		q.Set("season", strconv.Itoa(r.Season))
		q.Set("Fields", "MediaSources,ProviderIds")
		if p.cfg.UserID != "" {
			q.Set("userId", p.cfg.UserID)
		}
		var res jfItems
		if err := p.get(ctx, "/Shows/"+url.PathEscape(s.ID)+"/Episodes", q, itemLimit, &res); err != nil {
			return nil, err
		}
		var eps []jfItem
		for _, e := range res.Items {
			if e.ParentIndexNumber != nil && *e.ParentIndexNumber != r.Season {
				continue
			}
			if e.IndexNumber == nil {
				continue
			}
			first, last := *e.IndexNumber, *e.IndexNumber
			if e.IndexNumberEnd != nil && *e.IndexNumberEnd > last {
				last = *e.IndexNumberEnd // a file holding several episodes
			}
			if r.Episode >= first && r.Episode <= last {
				eps = append(eps, e)
			}
		}
		out = append(out, p.jellyfinSources(eps)...)
	}
	return out, nil
}

// jellyfinSources are the links to the files of items.
func (p *Provider) jellyfinSources(items []jfItem) []providers.Stream {
	var out []providers.Stream
	for _, it := range items {
		for _, ms := range it.MediaSources {
			q := url.Values{}
			q.Set("static", "true")
			if ms.ID != "" {
				q.Set("MediaSourceId", ms.ID)
			}
			st := providers.Stream{
				URL:      p.link("/Videos/"+url.PathEscape(it.ID)+"/stream", q),
				Size:     ms.Size,
				Filename: fileName(ms.Path),
			}
			var video, audio *jfMediaStream
			for i := range ms.MediaStreams {
				s := &ms.MediaStreams[i]
				switch {
				case strings.EqualFold(s.Type, "Video") && video == nil:
					video = s
				case strings.EqualFold(s.Type, "Audio") && audio == nil:
					audio = s
				}
			}
			f := fileInfo{container: ms.Container, name: st.Filename}
			if f.name == "" {
				f.name = ms.Name
			}
			if video != nil {
				f.height, f.width, f.videoCodec, f.hdr = video.Height, video.Width, video.Codec, strings.EqualFold(video.VideoRange, "HDR")
			}
			if audio != nil {
				f.audioCodec, f.channels = audio.Codec, audio.Channels
			}
			f.fill(&st)
			out = append(out, st)
		}
	}
	return out
}

// jellyfinCheck reads the server's name and version with the token, and
// the user whose libraries are searched.
func (p *Provider) jellyfinCheck(ctx context.Context) (Info, error) {
	var sys struct {
		ServerName string `json:"ServerName"`
		Version    string `json:"Version"`
	}
	if err := p.get(ctx, "/System/Info", nil, itemLimit, &sys); err != nil {
		return Info{}, err
	}
	info := Info{Name: sys.ServerName, Version: sys.Version}
	var user struct {
		ID   string `json:"Id"`
		Name string `json:"Name"`
	}
	if p.cfg.UserID != "" {
		if err := p.get(ctx, "/Users/"+url.PathEscape(p.cfg.UserID), nil, itemLimit, &user); err != nil {
			return info, fmt.Errorf("user %s: %w", p.cfg.UserID, err)
		}
	} else if err := p.get(ctx, "/Users/Me", nil, itemLimit, &user); err != nil {
		// An API key has no user of its own: it sees every library.
		user.ID, user.Name = "", ""
	}
	info.UserID, info.UserName = user.ID, user.Name
	return info, nil
}
