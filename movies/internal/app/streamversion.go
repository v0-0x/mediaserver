package app

import (
	"fmt"
	"log"
	"sync"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/db"
	"github.com/v0-0x/mediaserver/movies/internal/player"
	"github.com/v0-0x/mediaserver/movies/internal/stream"
)

// The version of a show last played
//
// Auto play next keeps the provider and version of the episode that just
// played (playback.go). The next episodes played by hand should too: the
// provider and version of the last stream of a show that worked (played
// past the first seconds, in mpv or the in-app player) are remembered, and
// the stream service looks for them first (the Play button, the Watch
// online list). Kept in the database, by show.

// versionKey is where a show's version is kept (kv table).
func versionKey(mediaID int) string { return fmt.Sprintf("stream-version:%d", mediaID) }

// workedFrom is how far into an episode (seconds) a stream counts as
// working.
const workedFrom = 10

// streamVersions remembers the version of each show that played last.
type streamVersions struct {
	db *db.DB

	mu sync.Mutex
	// known are the versions read or saved (nil: the show has none).
	known map[int]*stream.Version
}

func newStreamVersions(d *db.DB) *streamVersions {
	return &streamVersions{db: d, known: map[int]*stream.Version{}}
}

// Get returns the version of a show that played last, nil when none did.
func (v *streamVersions) Get(mediaID int) *stream.Version {
	v.mu.Lock()
	defer v.mu.Unlock()
	if ver, ok := v.known[mediaID]; ok {
		return copyVersion(ver)
	}
	var ver stream.Version
	ok, err := v.db.GetKV(versionKey(mediaID), &ver)
	if err != nil || !ok || ver.Provider == "" {
		v.known[mediaID] = nil
		return nil
	}
	v.known[mediaID] = &ver
	return copyVersion(&ver)
}

// Remember keeps the version of a show that played (saved only when it
// changed: it's told every few seconds while a stream plays).
func (v *streamVersions) Remember(mediaID int, ver stream.Version) {
	if old := v.Get(mediaID); old != nil && old.Same(ver) {
		return
	}
	v.mu.Lock()
	v.known[mediaID] = &ver
	v.mu.Unlock()
	if err := v.db.SetKV(versionKey(mediaID), ver); err != nil {
		log.Printf("providers: could not remember the version of %d: %v", mediaID, err)
	}
}

func copyVersion(v *stream.Version) *stream.Version {
	if v == nil {
		return nil
	}
	c := *v
	return &c
}

// rememberVersion is a player status hook: a stream of a show's episode
// that played past its first seconds worked, its provider and version are
// remembered for the show.
func (a *App) rememberVersion(s *player.Session) {
	if s == nil || s.Source != "stream" || s.Provider == "" || !catalog.IsShow(s.MediaID) ||
		!s.Active || s.Duration <= 0 || s.Position < workedFrom {
		return
	}
	a.versions.Remember(s.MediaID, sessionVersion(s))
}

// sessionVersion is the version of the stream a session plays.
func sessionVersion(s *player.Session) stream.Version {
	return stream.NewVersion(s.Provider, s.Quality, s.Release, s.Dub)
}
