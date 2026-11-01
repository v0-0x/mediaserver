package app

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/library"
)

// Every movie and show with files, this computer's or shared with it by
// another KumoBox, gets all its data downloaded once: its TMDB page, its
// seasons' episode titles and synopses, its artwork and episode stills. Its
// page then opens at once, also offline. One title a second when they have
// to be fetched, which leaves room under TMDB's rate limit for the rest of
// the app.

const (
	// Between two titles fetched from TMDB.
	prefetchEvery = time.Second
	// Done again after this long: new episodes, new artwork.
	prefetchedFor = 30 * 24 * time.Hour
)

type prefetcher struct {
	mu     sync.Mutex
	queue  []int
	queued map[int]bool
	wake   chan struct{}
}

func newPrefetcher() *prefetcher {
	return &prefetcher{queued: map[int]bool{}, wake: make(chan struct{}, 1)}
}

func prefetchedKey(id int) string { return fmt.Sprintf("prefetched:%d", id) }

// PrefetchMedia downloads the data of these titles, those that don't have
// it yet, in the background.
func (a *App) PrefetchMedia(ids ...int) {
	p := a.prefetch
	added := false
	p.mu.Lock()
	for _, id := range ids {
		var done bool
		if id <= 0 || p.queued[id] || a.DB.GetCache(prefetchedKey(id), &done) {
			continue
		}
		p.queued[id] = true
		p.queue = append(p.queue, id)
		added = true
	}
	p.mu.Unlock()
	if added {
		select {
		case p.wake <- struct{}{}:
		default:
		}
	}
}

// prefetchLibrary queues every title with files: after a scan, when the
// libraries shared with this KumoBox change, at startup. Nothing without a
// TMDB key.
func (a *App) prefetchLibrary() {
	if !a.Platform.Configured() {
		return
	}
	files, _ := a.Files.All()
	files = append(files, a.Share.RemoteFiles()...)
	seen := map[int]bool{}
	var ids []int
	for _, f := range files {
		if f.MediaID > 0 && !f.Ignored && !seen[f.MediaID] {
			seen[f.MediaID] = true
			ids = append(ids, f.MediaID)
		}
	}
	a.PrefetchMedia(ids...)
}

// runPrefetch downloads the queued titles' data, one at a time, until
// KumoBox stops.
func (a *App) runPrefetch(ctx context.Context) {
	p := a.prefetch
	for {
		p.mu.Lock()
		id := 0
		if len(p.queue) > 0 {
			id, p.queue = p.queue[0], p.queue[1:]
		}
		p.mu.Unlock()
		if id == 0 {
			select {
			case <-ctx.Done():
				return
			case <-p.wake:
			}
			continue
		}
		fetched := a.prefetchOne(ctx, id)
		p.mu.Lock()
		delete(p.queued, id)
		p.mu.Unlock()
		wait := 50 * time.Millisecond
		if fetched {
			wait = prefetchEvery
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// prefetchOne downloads a title's data, and reports whether it had to ask
// TMDB. Not marked done when a part couldn't be had (TMDB not answering):
// it's tried again next time.
func (a *App) prefetchOne(ctx context.Context, id int) (fetched bool) {
	fetched = !a.Platform.HasDetails(id)
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	media, err := a.Platform.Media(ctx, id, false)
	if err != nil {
		return fetched
	}
	// The episodes of the seasons it has files of (and of the one being
	// watched), one season at a time: not every season of a long show.
	files, _ := a.Files.ByMedia(id)
	for _, f := range a.Share.RemoteFiles() {
		if f.MediaID == id {
			files = append(files, f)
		}
	}
	progress := 0
	if media.MediaListEntry != nil {
		progress = media.MediaListEntry.Progress
	}
	complete := true
	for _, n := range library.SeasonsWanted(media, files, progress) {
		if _, err := a.Platform.Season(ctx, id, n); err != nil {
			complete = false
		}
	}
	e, err := a.Library.Entry(ctx, id, false)
	if err != nil || e == nil || e.Media == nil {
		return fetched
	}
	a.PrefetchEntryArt(e)
	if complete && e.MetadataNote == "" {
		a.DB.SetCache(prefetchedKey(id), true, prefetchedFor)
	}
	return fetched
}
