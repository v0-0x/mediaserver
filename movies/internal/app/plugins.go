package app

import (
	"context"
	"strings"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/extensions"
)

// pluginServices exposes app data to plugins in the shapes marketplace
// plugins expect. Plugins written for Kumo's $anilist API keep working: its
// methods are answered from the catalog and the list ("anime" are the
// movies and shows). What only AniList could do (custom GraphQL queries,
// its token, manga) isn't available.
func (a *App) pluginServices() extensions.PluginHostServices {
	return extensions.PluginHostServices{
		GetAnime: func(ctx context.Context, id int) (any, error) { return a.Platform.MediaLite(ctx, id) },
		GetAnimeDetails: func(ctx context.Context, id int) (any, error) {
			return a.Platform.Media(ctx, id, false)
		},
		Collection: func(ctx context.Context, mediaType string, refresh bool) (any, error) {
			if strings.EqualFold(mediaType, "MANGA") {
				return map[string]any{"MediaListCollection": &catalog.Collection{Lists: []*catalog.List{}}}, nil
			}
			c, err := a.Platform.Collection(ctx, mediaType, refresh)
			if err != nil {
				return nil, err
			}
			return map[string]any{"MediaListCollection": c}, nil
		},
		UpdateEntry: func(ctx context.Context, mediaID int, status *string, score *float64, progress *int) error {
			return a.UpdateEntry(ctx, catalog.EntryUpdate{MediaID: mediaID, Status: status, Score: score, Progress: progress})
		},
		DeleteEntry: func(ctx context.Context, mediaID int) error { return a.Platform.DeleteEntry(ctx, mediaID) },
		ListAnime: func(ctx context.Context, page int, search string, perPage int) (any, error) {
			res, err := a.Platform.Search(ctx, catalog.SearchParams{Page: page, Search: search, PerPage: perPage})
			if err != nil {
				return nil, err
			}
			return map[string]any{"Page": res}, nil
		},
		Viewer: func() (string, string) {
			if v := a.Platform.Viewer(); v != nil {
				return v.Name, v.Avatar.Large
			}
			return "", ""
		},
		AnimeEntry: func(ctx context.Context, id int) (any, error) {
			e, err := a.Library.Entry(ctx, id, false)
			if err != nil {
				return nil, err
			}
			out := map[string]any{"mediaId": id, "media": e.Media, "episodes": e.Episodes}
			if e.ListEntry != nil {
				out["listData"] = map[string]any{"progress": e.ListEntry.Progress, "score": e.ListEntry.Score, "status": e.ListEntry.Status, "repeat": e.ListEntry.Repeat}
			}
			if e.NextEpisode != nil {
				out["nextEpisode"] = e.NextEpisode
			}
			files, _ := a.Files.ByMedia(id)
			out["localFiles"] = files
			return out, nil
		},
		LocalFiles: func() (any, error) { return a.Files.All() },
	}
}
