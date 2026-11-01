package mediaserver

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"

	"github.com/v0-0x/mediaserver/movies/internal/providers"
)

// Plex
//
// The movie and show libraries (/library/sections) are listed with their
// external ids (includeGuids=1) and matched on the tmdb:// guid, else the
// imdb:// one. A show's episodes are its season's children
// (/library/metadata/{show}/children, then the season's). Each media part
// (a file) is a link to the file as it is: its key, /library/parts/...

type plexContainer struct {
	MediaContainer struct {
		FriendlyName string          `json:"friendlyName"`
		Version      string          `json:"version"`
		Directory    []plexDirectory `json:"Directory"`
		Metadata     []plexMetadata  `json:"Metadata"`
	} `json:"MediaContainer"`
}

type plexDirectory struct {
	Key   string `json:"key"`
	Type  string `json:"type"`
	Title string `json:"title"`
}

type plexMetadata struct {
	RatingKey   string      `json:"ratingKey"`
	Title       string      `json:"title"`
	Year        int         `json:"year"`
	Index       int         `json:"index"`
	ParentIndex int         `json:"parentIndex"`
	GUIDs       []plexGUID  `json:"Guid"`
	Media       []plexMedia `json:"Media"`
}

type plexGUID struct {
	ID string `json:"id"`
}

type plexMedia struct {
	VideoResolution string     `json:"videoResolution"`
	Height          int        `json:"height"`
	Width           int        `json:"width"`
	VideoCodec      string     `json:"videoCodec"`
	AudioCodec      string     `json:"audioCodec"`
	AudioChannels   int        `json:"audioChannels"`
	Container       string     `json:"container"`
	Part            []plexPart `json:"Part"`
	// VideoDynamicRange is "HDR" for HDR videos (newer servers).
	VideoDynamicRange string `json:"videoDynamicRange"`
}

type plexPart struct {
	Key       string `json:"key"`
	File      string `json:"file"`
	Size      int64  `json:"size"`
	Container string `json:"container"`
}

// guid is the item's id at a metadata provider ("tmdb", "imdb").
func (m plexMetadata) guid(scheme string) string {
	for _, g := range m.GUIDs {
		if rest, ok := strings.CutPrefix(g.ID, scheme+"://"); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// plexFind returns the movies or shows (sectionType "movie" or "show")
// whose TMDB id is tmdbID, else whose IMDb id is imdbID, in every library
// of that type.
func (p *Provider) plexFind(ctx context.Context, sectionType string, tmdbID int, imdbID string) ([]plexMetadata, error) {
	var sections plexContainer
	if err := p.get(ctx, "/library/sections", nil, itemLimit, &sections); err != nil {
		return nil, err
	}
	itemType := "1" // movie
	if sectionType == "show" {
		itemType = "2"
	}
	tmdb := strconv.Itoa(tmdbID)
	var byTMDB, byIMDb []plexMetadata
	for _, d := range sections.MediaContainer.Directory {
		if d.Type != sectionType {
			continue
		}
		q := url.Values{}
		q.Set("type", itemType)
		q.Set("includeGuids", "1")
		var res plexContainer
		if err := p.get(ctx, "/library/sections/"+url.PathEscape(d.Key)+"/all", q, listLimit, &res); err != nil {
			return nil, err
		}
		for _, m := range res.MediaContainer.Metadata {
			switch {
			case tmdbID > 0 && m.guid("tmdb") == tmdb:
				byTMDB = append(byTMDB, m)
			case imdbID != "" && strings.EqualFold(m.guid("imdb"), imdbID):
				byIMDb = append(byIMDb, m)
			}
		}
	}
	if len(byTMDB) > 0 {
		return byTMDB, nil
	}
	return byIMDb, nil
}

func (p *Provider) plexStreams(ctx context.Context, r providers.Request) ([]providers.Stream, error) {
	if !r.IsTV() {
		movies, err := p.plexFind(ctx, "movie", r.TMDBID, r.IMDbID)
		if err != nil {
			return nil, err
		}
		return p.plexParts(movies), nil
	}
	shows, err := p.plexFind(ctx, "show", r.TMDBID, r.IMDbID)
	if err != nil {
		return nil, err
	}
	var out []providers.Stream
	for _, show := range shows {
		var seasons plexContainer
		if err := p.get(ctx, "/library/metadata/"+url.PathEscape(show.RatingKey)+"/children", nil, itemLimit, &seasons); err != nil {
			return nil, err
		}
		for _, season := range seasons.MediaContainer.Metadata {
			if season.Index != r.Season {
				continue
			}
			var eps plexContainer
			if err := p.get(ctx, "/library/metadata/"+url.PathEscape(season.RatingKey)+"/children", nil, itemLimit, &eps); err != nil {
				return nil, err
			}
			var match []plexMetadata
			for _, e := range eps.MediaContainer.Metadata {
				if e.Index == r.Episode {
					match = append(match, e)
				}
			}
			out = append(out, p.plexParts(match)...)
		}
	}
	return out, nil
}

// plexParts are the links to the files of items.
func (p *Provider) plexParts(items []plexMetadata) []providers.Stream {
	var out []providers.Stream
	for _, it := range items {
		for _, m := range it.Media {
			for _, part := range m.Part {
				if !strings.HasPrefix(part.Key, "/library/parts/") {
					continue
				}
				st := providers.Stream{URL: p.link(part.Key, nil), Size: part.Size, Filename: fileName(part.File)}
				container := part.Container
				if container == "" {
					container = m.Container
				}
				f := fileInfo{
					name: st.Filename, container: container, height: m.Height, width: m.Width, resolution: m.VideoResolution,
					videoCodec: m.VideoCodec, hdr: strings.EqualFold(m.VideoDynamicRange, "HDR"), audioCodec: m.AudioCodec, channels: m.AudioChannels,
				}
				if f.name == "" {
					f.name = it.Title
				}
				f.fill(&st)
				out = append(out, st)
			}
		}
	}
	return out
}

// plexCheck reads the server's name and version with the token, and
// counts its movie and show libraries.
func (p *Provider) plexCheck(ctx context.Context) (Info, error) {
	var root plexContainer
	if err := p.get(ctx, "/", nil, itemLimit, &root); err != nil {
		return Info{}, err
	}
	info := Info{Name: root.MediaContainer.FriendlyName, Version: root.MediaContainer.Version}
	var sections plexContainer
	if err := p.get(ctx, "/library/sections", nil, itemLimit, &sections); err != nil {
		return info, err
	}
	for _, d := range sections.MediaContainer.Directory {
		if d.Type == "movie" || d.Type == "show" {
			info.Libraries++
		}
	}
	if info.Libraries == 0 {
		return info, errors.New("the server has no movie or show library")
	}
	return info, nil
}
