package api

import (
	"net/http"
	"sort"

	"github.com/v0-0x/mediaserver/anime/internal/anikohub"
	"github.com/v0-0x/mediaserver/anime/internal/library"
	"github.com/v0-0x/mediaserver/anime/internal/util"
)

// Library tools › Clean up: how full the library's drive is, the episodes
// watched (to put away), the episodes there twice, and Aniko's trash.

type cleanupFile struct {
	Path       string `json:"path"`
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	MediaID    int    `json:"mediaId"`
	Episode    int    `json:"episode"`
	Title      string `json:"title"`
	Resolution string `json:"resolution,omitempty"`
	// WatchedAt: when it was watched to the end here, or (watched on the
	// AniList list only) when the list entry last changed; -1 when unknown.
	WatchedAt int64 `json:"watchedAt,omitempty"`
}

type cleanupView struct {
	// The drive the library folder is on (nil when it can't be told).
	Disk         *diskSpace          `json:"disk"`
	LibraryBytes int64               `json:"libraryBytes"`
	Watched      []cleanupFile       `json:"watched"`
	Duplicates   [][]cleanupFile     `json:"duplicates"`
	Trash        []library.TrashItem `json:"trash"`
	TrashBytes   int64               `json:"trashBytes"`
}

type diskSpace struct {
	Total uint64 `json:"total"`
	Free  uint64 `json:"free"`
}

func (s *Server) cleanup(r *http.Request) (any, error) {
	ctx := r.Context()
	cfg := s.app.Settings.Get()
	all, err := s.app.Files.All()
	if err != nil {
		return nil, err
	}
	view := &cleanupView{Watched: []cleanupFile{}, Duplicates: [][]cleanupFile{}, Trash: s.app.Trash.Items()}
	// The main library folder's drive (~ in it means the home folder).
	if dirs := cfg.LibraryDirs(); cfg.Library.Dir != "" && len(dirs) > 0 {
		if total, free, err := util.DiskSpace(dirs[0]); err == nil && total > 0 {
			view.Disk = &diskSpace{Total: total, Free: free}
		}
	}
	for _, it := range view.Trash {
		view.TrashBytes += it.Size
	}

	// Which episodes were watched: to the end here (the history), or as far
	// as the AniList list says (all of them once it's completed, or watched
	// again).
	type listed struct {
		progress int
		all      bool
		at       int64
	}
	lists := map[int]listed{}
	if coll, err := s.app.Platform.Collection(ctx, "ANIME", false); err == nil {
		for _, e := range coll.Entries() {
			lists[e.MediaID] = listed{progress: e.Progress, all: e.Status == "COMPLETED" || e.Status == "REPEATING" || e.Repeat > 0, at: e.UpdatedAt}
		}
	}
	var ids []int
	seen := map[int]bool{}
	var files []*library.LocalFile
	for _, f := range all {
		if f.Ignored {
			continue
		}
		view.LibraryBytes += f.Size
		if f.MediaID <= 0 || f.Kind != "main" || f.Episode <= 0 {
			continue
		}
		files = append(files, f)
		if !seen[f.MediaID] {
			seen[f.MediaID] = true
			ids = append(ids, f.MediaID)
		}
	}
	titles := map[int]string{}
	if medias, err := s.app.Platform.MediaBatch(ctx, ids); err == nil {
		for id, m := range medias {
			titles[id] = m.PreferredTitle()
		}
	}
	item := func(f *library.LocalFile) cleanupFile {
		return cleanupFile{Path: f.Path, Name: f.Name, Size: f.Size, MediaID: f.MediaID, Episode: f.Episode, Title: titles[f.MediaID], Resolution: f.Parsed.Resolution}
	}
	byEpisode := map[[2]int][]*library.LocalFile{}
	for _, f := range files {
		k := [2]int{f.MediaID, f.Episode}
		byEpisode[k] = append(byEpisode[k], f)
		c := item(f)
		if h := s.app.History.Get(f.MediaID, f.Episode); h != nil && h.Duration > 0 && h.Position/h.Duration >= cfg.Playback.CompletionThreshold {
			c.WatchedAt = h.UpdatedAt
		} else if l, ok := lists[f.MediaID]; ok && (l.all || l.progress >= f.Episode) {
			c.WatchedAt = l.at
			if c.WatchedAt == 0 {
				c.WatchedAt = -1 // watched, when unknown
			}
		} else {
			continue
		}
		view.Watched = append(view.Watched, c)
	}
	for _, fs := range byEpisode {
		if len(fs) < 2 {
			continue
		}
		// One file reached two ways (a library folder linked into another,
		// hard links) isn't two copies: putting "the other" away would take
		// the only one.
		var group []cleanupFile
		for i, f := range fs {
			same := false
			for _, o := range fs[:i] {
				if library.SameFile(o.Path, f.Path) {
					same = true
					break
				}
			}
			if !same {
				group = append(group, item(f))
			}
		}
		if len(group) < 2 {
			continue
		}
		// The one to keep first: the biggest (then by name, so it's the same
		// one each time).
		sort.Slice(group, func(i, j int) bool {
			if group[i].Size != group[j].Size {
				return group[i].Size > group[j].Size
			}
			return group[i].Path < group[j].Path
		})
		view.Duplicates = append(view.Duplicates, group)
	}
	sort.Slice(view.Watched, func(i, j int) bool {
		a, b := view.Watched[i], view.Watched[j]
		if a.Title != b.Title {
			return a.Title < b.Title
		}
		return a.Episode < b.Episode
	})
	sort.Slice(view.Duplicates, func(i, j int) bool {
		a, b := view.Duplicates[i][0], view.Duplicates[j][0]
		if a.Title != b.Title {
			return a.Title < b.Title
		}
		return a.Episode < b.Episode
	})
	return view, nil
}

func (s *Server) cleanupRoutes() {
	m := s.mux
	m.HandleFunc("GET /api/library/cleanup", h(s.cleanup))
	m.HandleFunc("POST /api/library/trash", h(libraryEdited(func(r *http.Request) (any, error) {
		if !isTrusted(r) {
			return nil, forbidden("only this computer can remove its files")
		}
		if s.app.HubStore != nil {
			return nil, badRequest("the hub has no trash: it deletes files (Library › Anime), and its audit log can download them again")
		}
		var body struct {
			Paths []string `json:"paths"`
		}
		if err := decode(r, &body); err != nil {
			return nil, err
		}
		if len(body.Paths) == 0 {
			return nil, badRequest("no files")
		}
		moved, failed := s.app.Trash.Put(body.Paths)
		if len(moved) > 0 {
			s.app.Hub.Publish("library-updated", nil)
		}
		return map[string]any{"moved": len(moved), "failed": failed}, nil
	})))
	m.HandleFunc("POST /api/library/trash/restore", h(libraryEdited(func(r *http.Request) (any, error) {
		if !isTrusted(r) {
			return nil, forbidden("only this computer can put its files back")
		}
		var body struct {
			IDs []string `json:"ids"`
		}
		if err := decode(r, &body); err != nil {
			return nil, err
		}
		restored, failed := s.app.Trash.Restore(body.IDs)
		if len(restored) > 0 {
			s.app.Hub.Publish("library-updated", nil)
		}
		return map[string]any{"restored": len(restored), "failed": failed}, nil
	})))
	// Deleted for good: the ones given, or all of them.
	m.HandleFunc("POST /api/library/trash/empty", h(libraryEdited(func(r *http.Request) (any, error) {
		if !isTrusted(r) {
			return nil, forbidden("only this computer can delete its files")
		}
		var body struct {
			IDs []string `json:"ids"`
		}
		if err := decode(r, &body); err != nil {
			return nil, err
		}
		freed, err := s.app.Trash.Empty(body.IDs)
		return map[string]any{"freed": freed}, err
	})))
	// A hub's dashboard deletes for good (no trash): its audit log keeps
	// what went, to download it again.
	m.HandleFunc("POST /api/library/delete", h(libraryEdited(func(r *http.Request) (any, error) {
		if s.app.HubStore == nil {
			return nil, notFound("only an Aniko Hub deletes files: here, Library tools › Clean up puts them in the trash")
		}
		if !isTrusted(r) {
			return nil, forbidden("sign in to the hub first")
		}
		var body struct {
			Paths []string `json:"paths"`
		}
		if err := decode(r, &body); err != nil {
			return nil, err
		}
		if len(body.Paths) == 0 {
			return nil, badRequest("no files")
		}
		removed, failed := s.app.Trash.Delete(body.Paths)
		var ids []int64
		if len(removed) > 0 {
			by, ip := auditBy(r)
			ids = s.app.LogDeleted(anikohub.Event{By: by, IP: ip, Text: "deleted"}, removed)
			s.app.Hub.Publish("library-updated", nil)
			s.app.Hub.Publish("hub-updated", nil)
		}
		var freed int64
		for _, r := range removed {
			freed += r.Size
		}
		return map[string]any{"deleted": len(removed), "freed": freed, "failed": failed, "events": ids}, nil
	})))
}
