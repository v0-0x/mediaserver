package app

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/v0-0x/mediaserver/anime/internal/anikohub"
	"github.com/v0-0x/mediaserver/anime/internal/downloads"
	"github.com/v0-0x/mediaserver/anime/internal/library"
)

// A hub's audit log (anikohub.Event): what the app itself puts in it.

// auditDownload notes a download onto the hub finishing or failing.
func (a *App) auditDownload(it downloads.Item) {
	e := anikohub.Event{Kind: "downloaded", By: "hub", MediaID: it.MediaID, Episode: it.Episode, Title: it.AnimeTitle, Text: "downloaded"}
	switch it.Status {
	case downloads.StatusCompleted:
		if it.Output != "" {
			f := anikohub.AuditFile{Name: filepath.Base(it.Output), Path: it.Output, Episode: it.Episode}
			if st, err := os.Stat(it.Output); err == nil {
				f.Size = st.Size()
			}
			e.Files, e.Size = []anikohub.AuditFile{f}, f.Size
		}
	case downloads.StatusFailed:
		e.Kind, e.Failed, e.Text, e.Detail = "download-failed", true, "couldn't download", it.Error
	default:
		return
	}
	a.HubStore.Log(e)
}

// LogDeleted notes files deleted from the hub (e says by whom), one event
// per anime, with what Download again needs; their IDs.
func (a *App) LogDeleted(e anikohub.Event, removed []library.Removed) []int64 {
	byMedia := map[int][]library.Removed{}
	var order []int
	for _, r := range removed {
		if byMedia[r.MediaID] == nil {
			order = append(order, r.MediaID)
		}
		byMedia[r.MediaID] = append(byMedia[r.MediaID], r)
	}
	ctx, cancel := context.WithTimeout(a.ctx, 3*time.Second)
	defer cancel()
	var ids []int64
	for _, id := range order {
		files := byMedia[id]
		ev := e
		ev.Kind, ev.MediaID, ev.Files, ev.Size = "delete", id, nil, 0
		for _, f := range files {
			ev.Files = append(ev.Files, anikohub.AuditFile{Name: f.Name, Path: f.Path, Size: f.Size, Episode: f.Episode})
			ev.Size += f.Size
		}
		if len(files) == 1 {
			ev.Episode = files[0].Episode
		}
		if id > 0 {
			if m, err := a.Platform.MediaLite(ctx, id); err == nil && m != nil {
				ev.Title = m.PreferredTitle()
			}
			ev.Again = a.againFor(id, files)
		}
		ids = append(ids, a.HubStore.Log(ev))
	}
	return ids
}

// dubName: a file name that says it's dubbed.
var dubName = regexp.MustCompile(`(?i)(^|[^a-z])(dub|dubbed)([^a-z]|$)`)

// againFor: how to download deleted files of an anime again: their
// episodes (not openings and endings, nor what has no number), dubbed or
// not and from where as the hub downloaded them, or by their names.
func (a *App) againFor(mediaID int, files []library.Removed) *anikohub.Again {
	seen := map[int]bool{}
	var eps []int
	paths := map[string]bool{}
	for _, f := range files {
		paths[f.Path] = true
		if f.Episode > 0 && f.Kind != "nc" && !seen[f.Episode] {
			seen[f.Episode] = true
			eps = append(eps, f.Episode)
		}
	}
	if len(eps) == 0 {
		return nil
	}
	sort.Ints(eps)
	ag := &anikohub.Again{MediaID: mediaID, Episodes: eps}
	for _, it := range a.Downloads.List() {
		if it.MediaID == mediaID && it.Status == downloads.StatusCompleted && paths[it.Output] {
			ag.Dub, ag.Provider, ag.Quality = it.Mode == "dub", it.Provider, it.Quality
			return ag
		}
	}
	for _, f := range files {
		if dubName.MatchString(f.Name) {
			ag.Dub = true
		}
	}
	return ag
}

// emptyHubTrash: a hub deletes files for good (since 2.0.34); what 2.0.32
// put in its trash goes now, into the audit log, from where it can be
// downloaded again.
func (a *App) emptyHubTrash() {
	before := a.Trash.Items()
	if len(before) == 0 {
		return
	}
	freed, err := a.Trash.Empty(nil)
	if err != nil {
		log.Printf("hub: emptying the old trash: %v", err)
	}
	left := map[string]bool{}
	for _, it := range a.Trash.Items() {
		left[it.ID] = true
	}
	var gone []library.Removed
	for _, it := range before {
		if !left[it.ID] {
			gone = append(gone, it.Removed())
		}
	}
	if len(gone) > 0 {
		a.LogDeleted(anikohub.Event{By: "hub", Text: "deleted what was in its trash (the hub has none now)"}, gone)
		log.Printf("hub: emptied its old trash: %d file(s), %d MB", len(gone), freed>>20)
	}
}

// pruneAudit forgets what's past keeping in the audit log, now and daily.
func (a *App) pruneAudit() {
	for {
		a.HubStore.PruneAudit(time.Now())
		select {
		case <-a.ctx.Done():
			return
		case <-time.After(24 * time.Hour):
		}
	}
}
