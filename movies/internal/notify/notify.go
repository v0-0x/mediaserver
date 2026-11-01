// Package notify tells about movies and shows as things happen: a new
// episode of a show being watched aired, a movie or an episode is ready in
// the library (downloaded, or found by a scan: a torrent that finished). The app shows them as a toast, or as a
// notification of the system when it isn't in front; the desktop app shows
// them in the background too (it listens to the events when its window is
// closed).
package notify

import (
	"context"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/config"
	"github.com/v0-0x/mediaserver/movies/internal/db"
	"github.com/v0-0x/mediaserver/movies/internal/events"
	"github.com/v0-0x/mediaserver/movies/internal/library"
)

// Event is the event type alerts are sent as.
const Event = "notify"

// Alert is one: "aired" (an episode is out), "ready" (in the library).
type Alert struct {
	Kind    string `json:"kind"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	MediaID int    `json:"mediaId,omitempty"`
	Episode int    `json:"episode,omitempty"`
	// Path: the file of a ready episode (one), to play it.
	Path  string `json:"path,omitempty"`
	Image string `json:"image,omitempty"`
}

// The list, as the airing checks need it.
type Lister interface {
	Collection(ctx context.Context, typ string, refresh bool) (*catalog.Collection, error)
	MediaLite(ctx context.Context, id int) (*catalog.Media, error)
}

type Service struct {
	settings *config.Store
	hub      *events.Hub
	db       *db.DB
	list     Lister
	mu       sync.Mutex
	// now is the time; poll, how often a file still being written is looked
	// at again (tests set them).
	now  func() time.Time
	poll time.Duration
}

func New(settings *config.Store, hub *events.Hub, d *db.DB, list Lister) *Service {
	return &Service{settings: settings, hub: hub, db: d, list: list, now: time.Now, poll: 30 * time.Second}
}

// settled: a file unchanged this long is written (a download done).
const settled = 90 * time.Second

func (s *Service) send(a Alert) {
	s.hub.Publish(Event, a)
}

// ---------------------------------------------------------------------------
// Aired

// airedKey: for each show, the key of its newest episode out the last time
// it was looked at (only an episode out since is told about).
const airedKey = "notify:aired"

// Run checks for aired episodes until ctx ends: every checkEvery, with the
// list asked for again (new airing times) every refreshEvery.
func (s *Service) Run(ctx context.Context) {
	const checkEvery, refreshEvery = 10 * time.Minute, time.Hour
	// The list as the app has it at first (it asks for it as it starts).
	refreshed := s.now()
	for {
		refresh := s.now().Sub(refreshed) >= refreshEvery
		if refresh {
			refreshed = s.now()
		}
		cctx, cancel := context.WithTimeout(ctx, time.Minute)
		if err := s.CheckAired(cctx, refresh); err != nil && ctx.Err() == nil {
			log.Printf("new episodes: %v", err)
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-time.After(checkEvery):
		}
	}
}

// CheckAired tells about the episodes out since last time, of the shows
// being watched (not those already watched: seen elsewhere as they came
// out). A show seen the first time is only noted.
func (s *Service) CheckAired(ctx context.Context, refresh bool) error {
	if !s.settings.Get().Notify.NewEpisodes {
		return nil
	}
	coll, err := s.list.Collection(ctx, "", refresh)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[int]int{}
	_, _ = s.db.GetKV(airedKey, &seen)
	now := s.now().Unix()
	changed := false
	var out []Alert
	for _, e := range coll.Entries() {
		if e.Status != "CURRENT" && e.Status != "REPEATING" || e.Media == nil || !e.Media.IsShow() {
			continue
		}
		aired := airedNow(e.Media, now)
		if aired <= 0 {
			continue
		}
		last, known := seen[e.MediaID]
		if known && aired == last {
			continue
		}
		seen[e.MediaID], changed = aired, true
		if !known || aired < last || e.Progress >= aired {
			continue
		}
		title := e.Media.PreferredTitle()
		a := Alert{Kind: "aired", MediaID: e.MediaID, Episode: aired, Image: e.Media.CoverImage.Large}
		if ls, le := catalog.SplitKey(last); aired == catalog.EpisodeKey(ls, le+1) {
			a.Title, a.Body = title, fmt.Sprintf("%s is out.", catalog.FormatKey(aired))
		} else {
			a.Title, a.Body = title, fmt.Sprintf("New episodes are out, up to %s.", catalog.FormatKey(aired))
		}
		out = append(out, a)
	}
	if changed {
		if err := s.db.SetKV(airedKey, seen); err != nil {
			return err
		}
	}
	for _, a := range out {
		s.send(a)
	}
	return nil
}

// airedNow is the key of a show's newest episode out at now: its next one
// once its air date has passed (the list may be from before), else the last
// one TMDB says aired (0: none).
func airedNow(m *catalog.Media, now int64) int {
	if n := m.NextAiringEpisode; n != nil && n.Episode > 0 && int64(n.AiringAt) <= now {
		return n.Episode
	}
	return m.LatestEpisode
}

// ---------------------------------------------------------------------------
// Ready

// Downloaded tells that a download finished (when that's on): false when it
// isn't, for the usual toast.
func (s *Service) Downloaded(mediaID, episode int, title, path string) bool {
	if !s.settings.Get().Notify.Downloads {
		return false
	}
	a := Alert{Kind: "ready", MediaID: mediaID, Episode: episode, Path: path, Title: title, Body: "It's downloaded."}
	if catalog.IsShow(mediaID) {
		a.Body = fmt.Sprintf("%s is downloaded.", catalog.FormatKey(episode))
	}
	if mediaID > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if m, err := s.list.MediaLite(ctx, mediaID); err == nil && m != nil {
			a.Title, a.Image = m.PreferredTitle(), m.CoverImage.Large
		}
		cancel()
	}
	s.send(a)
	return true
}

// NewFiles tells about the movies and episodes a scan found (a torrent
// finished, files copied in): each title's, or how many when there are lots (a new library
// folder). A file still being written (a torrent downloading into the
// library folder has its file there from the start) is told about once it
// stops changing: when it's done.
func (s *Service) NewFiles(files []*library.LocalFile) {
	if !s.settings.Get().Notify.Downloads {
		return
	}
	var done, writing []*library.LocalFile
	for _, f := range files {
		if s.now().Sub(time.Unix(f.ModTime, 0)) < settled {
			writing = append(writing, f)
		} else {
			done = append(done, f)
		}
	}
	if len(writing) > 0 {
		go s.whenWritten(writing)
	}
	s.tell(done)
}

// whenWritten looks at files being written now and then, and tells about
// each once it stopped changing (for up to a day). One gone meanwhile was
// renamed when done (to its own name: a scan finds that) or deleted.
func (s *Service) whenWritten(files []*library.LocalFile) {
	for start := s.now(); len(files) > 0 && s.now().Sub(start) < 24*time.Hour; {
		time.Sleep(s.poll)
		var done, still []*library.LocalFile
		for _, f := range files {
			st, err := os.Stat(f.Path)
			if err != nil {
				continue
			}
			if st.Size() == f.Size && st.ModTime().Unix() == f.ModTime && s.now().Sub(st.ModTime()) >= settled {
				done = append(done, f)
				continue
			}
			cp := *f
			cp.Size, cp.ModTime = st.Size(), st.ModTime().Unix()
			still = append(still, &cp)
		}
		if len(done) > 0 && s.settings.Get().Notify.Downloads {
			s.tell(done)
		}
		files = still
	}
}

func (s *Service) tell(files []*library.LocalFile) {
	byMedia := map[int][]*library.LocalFile{}
	var order []int
	for _, f := range files {
		if f.MediaID <= 0 || f.Kind != "main" || f.Episode <= 0 || f.Ignored {
			continue
		}
		if _, ok := byMedia[f.MediaID]; !ok {
			order = append(order, f.MediaID)
		}
		byMedia[f.MediaID] = append(byMedia[f.MediaID], f)
	}
	if len(order) == 0 {
		return
	}
	if len(order) > 3 {
		n := 0
		for _, fs := range byMedia {
			n += len(fs)
		}
		s.send(Alert{Kind: "ready", Title: "New in your library", Body: fmt.Sprintf("%d files of %d titles.", n, len(order))})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, id := range order {
		fs := byMedia[id]
		sort.Slice(fs, func(i, j int) bool { return fs[i].Episode < fs[j].Episode })
		a := Alert{Kind: "ready", MediaID: id, Title: "New in your library", Episode: fs[0].Episode}
		if m, err := s.list.MediaLite(ctx, id); err == nil && m != nil {
			a.Title, a.Image = m.PreferredTitle(), m.CoverImage.Large
		}
		switch {
		case !catalog.IsShow(id):
			a.Path = fs[0].Path
			a.Body = "It's in your library."
		case len(fs) == 1:
			a.Path = fs[0].Path
			a.Body = fmt.Sprintf("%s is in your library.", catalog.FormatKey(fs[0].Episode))
		default:
			eps := make([]int, len(fs))
			for i, f := range fs {
				eps[i] = f.Episode
			}
			a.Body = fmt.Sprintf("%s are in your library.", ranges(eps))
		}
		s.send(a)
	}
}

// ranges writes sorted episode keys with their runs joined:
// "S01E01–E03, S02E05".
func ranges(keys []int) string {
	var parts []string
	for i := 0; i < len(keys); {
		j := i
		for j+1 < len(keys) && keys[j+1] <= keys[j]+1 {
			j++
		}
		if keys[i] == keys[j] {
			parts = append(parts, catalog.FormatKey(keys[i]))
		} else {
			_, e := catalog.SplitKey(keys[j])
			parts = append(parts, fmt.Sprintf("%s–E%02d", catalog.FormatKey(keys[i]), e))
		}
		i = j + 1
	}
	return strings.Join(parts, ", ")
}
