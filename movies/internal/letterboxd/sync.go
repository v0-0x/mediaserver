package letterboxd

import (
	"context"
	"errors"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/config"
	"github.com/v0-0x/mediaserver/movies/internal/db"
	"github.com/v0-0x/mediaserver/movies/internal/events"
)

// Catalog is what Sync needs of catalog.Platform.
//
// Diary(ctx, 0, n) must list the entries of every media (Export with all
// set uses it); the limits passed are always large, so either reading of
// "0" as a limit works.
type Catalog interface {
	SetViewer(v *catalog.Viewer) error
	Viewer() *catalog.Viewer
	Collection(ctx context.Context, mediaType string, refresh bool) (*catalog.Collection, error)
	MediaLite(ctx context.Context, id int) (*catalog.Media, error)
	FindMovie(ctx context.Context, title string, year int) (*catalog.Media, error)
	UpdateEntry(ctx context.Context, u catalog.EntryUpdate) error
	SetLetterboxdState(ctx context.Context, mediaID int, uri string, syncedAt int64) error
	AddDiary(ctx context.Context, e catalog.DiaryEntry) (*catalog.DiaryEntry, bool, error)
	Diary(ctx context.Context, mediaID int, limit int) ([]*catalog.DiaryEntry, error)
	PendingDiary(ctx context.Context) ([]*catalog.DiaryEntry, error)
	MarkDiaryExported(ctx context.Context, ids []int64, at int64) error
}

// Sync keeps the Moviko list and diary in step with a Letterboxd account.
// Fill in the exported fields; the zero values of the others are fine.
type Sync struct {
	Catalog  Catalog
	Settings *config.Store
	DB       *db.DB
	Hub      *events.Hub
	HTTP     *http.Client

	// BaseURL is the Letterboxd website, APIBaseURL its API (defaults:
	// $MOVIKO_LETTERBOXD_URL / $MOVIKO_LETTERBOXD_API_URL, then
	// DefaultBaseURL / DefaultAPIBaseURL).
	BaseURL    string
	APIBaseURL string

	// NotFound is the error Catalog.FindMovie returns (wrapped or not) when
	// no film matches. Other errors of FindMovie count as no match too, but
	// a sync where every lookup failed that way fails with the error.
	NotFound error

	once    sync.Once
	sem     chan struct{} // one sync (pull, import, push) at a time
	kick    chan struct{} // asks the background loop to push
	syncing atomic.Bool

	// for tests
	now        func() time.Time
	firstDelay time.Duration
	tick       time.Duration
}

// Result sums up a sync. Pull and Import count films: Added were put on the
// list, Updated changed (or got diary entries), Skipped had nothing new;
// Diary counts the diary entries added. Push counts diary entries: Added
// were sent, Skipped aren't on Letterboxd. Unmatched names the films that
// couldn't be found ("Title (Year)").
type Result struct {
	Added     int      `json:"added"`
	Updated   int      `json:"updated"`
	Skipped   int      `json:"skipped"`
	Diary     int      `json:"diary"`
	Unmatched []string `json:"unmatched"`
}

func (r *Result) changed() bool { return r != nil && r.Added+r.Updated+r.Diary > 0 }

// Status is the state of the connection, for the UI.
type Status struct {
	Connected   bool   `json:"connected"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	ProfileURL  string `json:"profileUrl,omitempty"`
	// LastSync is when the diary was last read (unix seconds, 0: never),
	// LastError why the last sync failed ("" when it didn't).
	LastSync  int64  `json:"lastSync"`
	LastError string `json:"lastError,omitempty"`
	// Pending counts the diary entries logged in Moviko that Letterboxd
	// doesn't have yet.
	Pending int  `json:"pending"`
	Syncing bool `json:"syncing"`
	// APIAvailable: API client credentials are set; APIConnected: a member
	// signed in with them.
	APIAvailable bool   `json:"apiAvailable"`
	APIConnected bool   `json:"apiConnected"`
	APIMember    string `json:"apiMember,omitempty"`
}

// StatusEvent is published (with a Status) when the sync state changes.
const StatusEvent = "letterboxd-status"

// Keys of Sync's state in the kv table.
const (
	kvState  = "letterboxd:state"
	kvToken  = "letterboxd:token"
	kvMember = "letterboxd:member"
)

type syncState struct {
	LastSync    int64  `json:"lastSync"`
	LastAttempt int64  `json:"lastAttempt"`
	LastError   string `json:"lastError"`
	DisplayName string `json:"displayName"`
}

var defaultHTTP = &http.Client{Timeout: 30 * time.Second}

func (s *Sync) init() {
	s.once.Do(func() {
		s.sem = make(chan struct{}, 1)
		s.kick = make(chan struct{}, 1)
	})
}

// lock waits for the other syncs to end (or ctx to).
func (s *Sync) lock(ctx context.Context) (func(), error) {
	s.init()
	select {
	case s.sem <- struct{}{}:
		s.syncing.Store(true)
		return func() {
			s.syncing.Store(false)
			<-s.sem
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *Sync) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *Sync) settings() config.LetterboxdSettings {
	if s.Settings == nil {
		return config.Defaults().Letterboxd
	}
	return s.Settings.Get().Letterboxd
}

func (s *Sync) saveSettings(fn func(*config.LetterboxdSettings)) error {
	if s.Settings == nil {
		return errors.New("letterboxd: no settings store")
	}
	st := s.Settings.Get()
	fn(&st.Letterboxd)
	_, err := s.Settings.Save(st)
	return err
}

func (s *Sync) httpClient() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return defaultHTTP
}

func (s *Sync) baseURL() string {
	return firstNonEmpty(s.BaseURL, os.Getenv("MOVIKO_LETTERBOXD_URL"), DefaultBaseURL)
}

func (s *Sync) apiBaseURL() string {
	return firstNonEmpty(s.APIBaseURL, os.Getenv("MOVIKO_LETTERBOXD_API_URL"), DefaultAPIBaseURL)
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s = strings.TrimSpace(s); s != "" {
			return s
		}
	}
	return ""
}

// kv helpers: the state is a convenience, so a missing database only loses it.

func (s *Sync) getKV(key string, out any) bool {
	if s.DB == nil {
		return false
	}
	ok, err := s.DB.GetKV(key, out)
	return ok && err == nil
}

func (s *Sync) setKV(key string, v any) error {
	if s.DB == nil {
		return nil
	}
	return s.DB.SetKV(key, v)
}

func (s *Sync) deleteKV(key string) {
	if s.DB != nil {
		_ = s.DB.DeleteKV(key)
	}
}

func (s *Sync) state() syncState {
	var st syncState
	s.getKV(kvState, &st)
	return st
}

// record notes the end of a diary read.
func (s *Sync) record(err error, displayName string) {
	st := s.state()
	now := s.clock().Unix()
	st.LastAttempt = now
	if err != nil {
		st.LastError = err.Error()
	} else {
		st.LastSync = now
		st.LastError = ""
	}
	if displayName != "" {
		st.DisplayName = displayName
	}
	_ = s.setKV(kvState, st)
}

// recordError notes a failed sync other than a diary read (push, import).
func (s *Sync) recordError(err error) {
	st := s.state()
	if err != nil {
		st.LastError = err.Error()
	} else {
		st.LastError = ""
	}
	_ = s.setKV(kvState, st)
}

func (s *Sync) publish(typ string, payload any) {
	if s.Hub != nil {
		s.Hub.Publish(typ, payload)
	}
}

// changed tells the UI the list changed.
func (s *Sync) changed(res *Result) {
	if res.changed() {
		s.publish(events.CollectionUpdate, map[string]any{"source": "letterboxd"})
	}
}

func (s *Sync) publishStatus() {
	if s.Hub != nil {
		s.Hub.Publish(StatusEvent, s.Status(context.Background()))
	}
}

// viewer is the catalog.Viewer of a member.
func viewer(username, displayName, avatar string) *catalog.Viewer {
	v := &catalog.Viewer{
		ID:         ViewerID(username),
		Name:       firstNonEmpty(displayName, username),
		Letterboxd: ProfileURL(DefaultBaseURL, username),
	}
	v.Avatar.Large = avatar
	v.Avatar.Medium = avatar
	v.MediaListOptions.ScoreFormat = "POINT_5_HALF"
	return v
}

// Connect connects the Letterboxd member username (a profile link works
// too): it checks that their diary feed exists (ErrNoSuchMember otherwise),
// saves the username, makes them the catalog's viewer and reads the diary.
// The member stays connected when that first read fails; its error is
// returned with the viewer, and kept in Status.
func (s *Sync) Connect(ctx context.Context, username string) (*catalog.Viewer, *Result, error) {
	username, err := ParseUsername(username)
	if err != nil {
		return nil, nil, err
	}
	feed, err := FetchFeed(ctx, s.httpClient(), s.baseURL(), username)
	if err != nil {
		return nil, nil, err
	}
	if strings.EqualFold(feed.Username, username) {
		username = feed.Username // Letterboxd's spelling
	}
	prev := s.settings().Username
	if err := s.saveSettings(func(l *config.LetterboxdSettings) { l.Username = username }); err != nil {
		return nil, nil, err
	}
	if !strings.EqualFold(prev, username) {
		s.deleteKV(kvState)
	}
	v := viewer(username, feed.DisplayName, s.apiAvatar(username))
	if err := s.Catalog.SetViewer(v); err != nil {
		return nil, nil, err
	}
	release, err := s.lock(ctx)
	if err != nil {
		return v, nil, err
	}
	res, err := s.pullFeed(ctx, feed)
	s.record(err, feed.DisplayName)
	release()
	s.changed(res)
	s.publishStatus()
	return v, res, err
}

// apiAvatar is the avatar of the member signed in to the API, if it's
// username.
func (s *Sync) apiAvatar(username string) string {
	var m Member
	if s.getKV(kvMember, &m) && strings.EqualFold(m.Username, username) {
		return m.Avatar
	}
	return ""
}

// Disconnect forgets the account: the username, the viewer, the API sign-in
// and the sync state. What was imported stays on the list.
func (s *Sync) Disconnect() error {
	if err := s.saveSettings(func(l *config.LetterboxdSettings) { l.Username = "" }); err != nil {
		return err
	}
	err := s.Catalog.SetViewer(nil)
	s.deleteKV(kvToken)
	s.deleteKV(kvMember)
	s.deleteKV(kvState)
	s.publishStatus()
	return err
}

// Status returns the state of the connection.
func (s *Sync) Status(ctx context.Context) Status {
	set := s.settings()
	st := s.state()
	out := Status{
		Connected:    set.Username != "",
		Username:     set.Username,
		DisplayName:  st.DisplayName,
		LastSync:     st.LastSync,
		LastError:    st.LastError,
		Syncing:      s.syncing.Load(),
		APIAvailable: set.APIClientID != "" && set.APIClientSecret != "",
	}
	if out.Connected {
		out.ProfileURL = ProfileURL(DefaultBaseURL, set.Username)
		if v := s.Catalog.Viewer(); v != nil && v.ID == ViewerID(set.Username) && v.Name != "" {
			out.DisplayName = v.Name
		}
		if out.DisplayName == "" {
			out.DisplayName = set.Username
		}
	}
	var tok Token
	if s.getKV(kvToken, &tok) && tok.AccessToken != "" {
		out.APIConnected = true
		var m Member
		if s.getKV(kvMember, &m) {
			out.APIMember = m.Username
		}
	}
	if pending, err := s.Catalog.PendingDiary(ctx); err == nil {
		out.Pending = len(pending)
	}
	return out
}

// Start runs the automatic sync in the background until ctx ends: shortly
// after start, then every Settings.Letterboxd.SyncMinutes, it reads the
// diary (when AutoSync is on and an account is connected) and sends what's
// pending (with API access). It returns at once.
func (s *Sync) Start(ctx context.Context) {
	s.init()
	go s.loop(ctx)
}

func (s *Sync) loop(ctx context.Context) {
	first, tick := s.firstDelay, s.tick
	if first <= 0 {
		first = 20 * time.Second
	}
	if tick <= 0 {
		tick = time.Minute
	}
	timer := time.NewTimer(first)
	defer timer.Stop()
	var lastPull time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.kick:
			s.backgroundPush(ctx)
		case <-timer.C:
			set := s.settings()
			every := time.Duration(set.SyncMinutes) * time.Minute
			if every <= 0 {
				every = time.Hour
			}
			if set.AutoSync && set.Username != "" && (lastPull.IsZero() || s.clock().Sub(lastPull) >= every) {
				lastPull = s.clock()
				_, _ = s.Pull(ctx) // the error is kept in Status
				s.backgroundPush(ctx)
			}
			timer.Reset(tick)
		}
	}
}

// backgroundPush sends the pending diary entries when the API is set up.
func (s *Sync) backgroundPush(ctx context.Context) {
	var tok Token
	if !s.getKV(kvToken, &tok) || tok.AccessToken == "" {
		return
	}
	if pending, err := s.Catalog.PendingDiary(ctx); err != nil || len(pending) == 0 {
		return
	}
	_, _ = s.Push(ctx)
}

// requestPush asks the background loop (if running) to push soon.
func (s *Sync) requestPush() {
	s.init()
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

func sortedUnique(names map[string]bool) []string {
	out := make([]string, 0, len(names))
	for n := range names {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
