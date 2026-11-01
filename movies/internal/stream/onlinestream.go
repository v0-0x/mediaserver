// Package stream provides online streaming (the streaming providers, see
// package providers), the HLS/media proxy used by the in-app player, and
// local-file direct play / transcoding.
package stream

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/providers"
)

// DefaultTTL is how long the streams found for an episode are kept: long
// enough to pick one, play it and come back to the list, short enough for
// links that expire (many do within the hour).
const DefaultTTL = 5 * time.Minute

// MediaFunc looks a movie or show up by media id (Platform.MediaLite).
type MediaFunc func(ctx context.Context, id int) (*catalog.Media, error)

// Options are the settings the service reads on every request.
type Options struct {
	// PreferredQuality ranks streams ("1080p"... "" for the best first).
	PreferredQuality string
	// Timeout bounds each provider's answer (none when 0).
	Timeout time.Duration
	// DefaultProvider's streams are listed first when all providers are
	// asked ("" for none).
	DefaultProvider string
}

// Service finds the streams of movies and episodes: it asks the enabled
// providers all at once, ranks what they answer and keeps it for a few
// minutes, so the stream picked in the list is still there to play or
// download.
type Service struct {
	// Providers returns the enabled streaming providers, in the user's
	// order.
	Providers func() []providers.Provider
	// SubtitleProviders returns the enabled subtitle providers (Stremio
	// subtitle addons).
	SubtitleProviders func() []providers.SubtitleProvider
	// Health records how each provider's searches go (nil: not recorded).
	// Providers failing again and again are listed last, and left out of
	// the automatic picks (Best).
	Health *providers.Health
	// Versions returns the version of a show that played last (nil when
	// none): its next episodes are looked for in it first.
	Versions func(mediaID int) *Version
	// TTL is how long results are kept (DefaultTTL when 0).
	TTL time.Duration

	options func() Options
	now     func() time.Time
	// afterFunc runs a function later (time.AfterFunc; replaced in tests).
	afterFunc func(time.Duration, func())

	mu      sync.Mutex
	cache   map[string]*cached
	flights map[string]*flight
}

// cached is what the providers answered for an episode, unranked.
type cached struct {
	mediaID, episode int
	// providers are the ids of the providers asked.
	providers []string
	streams   []Stream
	errors    []providers.ProviderError
	subtitles []providers.Subtitle
	// answers are how each provider answered, in the order they did (see
	// StreamsLive).
	answers []answered
	at      time.Time
}

// answered is how a provider answered a search: its streams by id (the
// ones kept), its error, how long it took.
type answered struct {
	provider, name, err string
	ms                  int64
	ids                 []string
}

// NewService returns a service reading its options with opts (nil: none).
func NewService(opts func() Options) *Service {
	if opts == nil {
		opts = func() Options { return Options{} }
	}
	return &Service{
		options: opts, now: time.Now, afterFunc: func(d time.Duration, f func()) { time.AfterFunc(d, f) },
		cache: map[string]*cached{}, flights: map[string]*flight{},
	}
}

// Stream kinds: how a stream plays.
const (
	KindDirect   = "direct"
	KindTorrent  = "torrent"
	KindYouTube  = "youtube"
	KindExternal = "external"
)

// Stream is a provider's stream with an id that stays the same as long as
// the provider gives the same link (to play or download it later), and how
// it plays.
type Stream struct {
	providers.Stream
	ID   string `json:"id"`
	Kind string `json:"kind"`
	// Preferred is set on the streams most like the version of the show
	// that played last (see Result.Preferred).
	Preferred bool `json:"preferred,omitempty"`
}

// Match is the title an extension provider matched the movie or show with
// (it searches by title: the match may be wrong, and can be picked by hand).
type Match struct {
	Provider string  `json:"provider"`
	Name     string  `json:"name"`
	ID       string  `json:"id"`
	Title    string  `json:"title"`
	Score    float64 `json:"score"`
	Manual   bool    `json:"manual"`
}

// Result is the streams of a movie or an episode, best first.
type Result struct {
	MediaID int `json:"mediaId"`
	Episode int `json:"episode"`
	// Request is what the providers were asked.
	Request   providers.Request         `json:"request"`
	Streams   []Stream                  `json:"streams"`
	Errors    []providers.ProviderError `json:"errors"`
	Subtitles []providers.Subtitle      `json:"subtitles"`
	// Providers are the ids of the providers asked.
	Providers []string `json:"providers"`
	Matches   []Match  `json:"matches,omitempty"`
	// Preferred is the version of the show that played last: its direct
	// links come first.
	Preferred *Version `json:"preferred,omitempty"`
	// FetchedAt is when the providers answered (unix seconds).
	FetchedAt int64 `json:"fetchedAt"`
}

// Direct returns the direct links of the result, best first.
func (r *Result) Direct() []Stream {
	var out []Stream
	for _, s := range r.Streams {
		if s.Kind == KindDirect {
			out = append(out, s)
		}
	}
	return out
}

// ErrNoProviders is returned when no enabled provider has streams of the
// requested kind of title.
var ErrNoProviders = errors.New("no streaming provider is enabled: turn on the Internet Archive or add Stremio addons, Nuvio providers or extensions (Providers & extensions)")

// RequestFor is what providers are asked for a movie, or for the episode
// key of a show.
func RequestFor(m *catalog.Media, episode int) providers.Request {
	r := providers.Request{
		MediaID: m.ID, TMDBID: catalog.TMDBID(m.ID), IMDbID: strings.TrimSpace(m.IMDbID),
		Type: providers.TypeMovie, Title: m.EnglishOrRomaji(), Year: m.Year(),
	}
	for _, t := range []string{m.Title.Native, m.Title.Romaji} {
		if t = strings.TrimSpace(t); t != "" && !strings.EqualFold(t, r.Title) {
			r.OriginalTitle = t
			break
		}
	}
	if m.IsShow() {
		r.Type = providers.TypeTV
		r.Season, r.Episode = catalog.SplitKey(episode)
	}
	return r
}

// checkEpisode fills in a movie's episode and refuses a show's missing one.
func checkEpisode(m *catalog.Media, episode int) (int, error) {
	if m == nil {
		return 0, errors.New("unknown movie or show")
	}
	if !m.IsShow() {
		return catalog.MovieEpisode, nil
	}
	if _, e := catalog.SplitKey(episode); episode <= 0 || e <= 0 {
		return 0, errors.New("which episode? (episode is a key: season*1000 + episode)")
	}
	return episode, nil
}

// StreamID is a stream's id: the same link (torrent file, video, page) from
// the same provider always gets the same one.
func StreamID(s providers.Stream) string {
	sum := sha1.Sum([]byte(s.Provider + "\n" + identity(s)))
	return hex.EncodeToString(sum[:8])
}

// identity is what tells a stream apart from the provider's other ones.
func identity(s providers.Stream) string {
	switch {
	case s.Direct():
		return "url:" + s.URL
	case s.InfoHash != "":
		return "bt:" + strings.ToLower(s.InfoHash) + ":" + strconv.Itoa(s.FileIdx)
	case s.Magnet != "":
		return "magnet:" + s.Magnet
	case s.YouTubeID != "":
		return "yt:" + s.YouTubeID
	}
	return "ext:" + s.ExternalURL
}

// KindOf is how a stream plays: KindDirect, KindTorrent, KindYouTube or
// KindExternal.
func KindOf(s providers.Stream) string {
	switch {
	case s.Direct():
		return KindDirect
	case s.InfoHash != "" || s.Magnet != "":
		return KindTorrent
	case s.YouTubeID != "":
		return KindYouTube
	}
	return KindExternal
}

// Wrap gives a provider's stream its id and kind.
func Wrap(s providers.Stream) Stream {
	return Stream{Stream: s, ID: StreamID(s), Kind: KindOf(s)}
}

// Invalidate forgets every result (providers were added, removed or
// reconfigured).
func (s *Service) Invalidate() {
	s.mu.Lock()
	s.cache = map[string]*cached{}
	s.mu.Unlock()
}

func (s *Service) ttl() time.Duration {
	if s.TTL > 0 {
		return s.TTL
	}
	return DefaultTTL
}

// providersFor returns the enabled providers having titles of mediaType,
// only the one whose id is only when it's set.
func (s *Service) providersFor(mediaType, only string) ([]providers.Provider, error) {
	var all []providers.Provider
	if s.Providers != nil {
		all = s.Providers()
	}
	var out []providers.Provider
	for _, p := range all {
		if p == nil {
			continue
		}
		if only != "" {
			if p.ID() != only {
				continue
			}
			if !p.Supports(mediaType) {
				what := "movies"
				if mediaType == providers.TypeTV {
					what = "shows"
				}
				return nil, fmt.Errorf("%s has no %s", p.Name(), what)
			}
			return []providers.Provider{p}, nil
		}
		if p.Supports(mediaType) {
			out = append(out, p)
		}
	}
	if only != "" {
		return nil, fmt.Errorf("streaming provider %q is not installed or is disabled", only)
	}
	if len(out) == 0 {
		return nil, ErrNoProviders
	}
	return out, nil
}

// demotedLast moves what belongs to demoted providers (see
// providers.Health) after the rest, keeping both groups' order.
func demotedLast[T any](xs []T, provider func(T) string, h *providers.Health) []T {
	if h == nil {
		return xs
	}
	var ok, demoted []T
	for _, x := range xs {
		if h.Demoted(provider(x)) {
			demoted = append(demoted, x)
		} else {
			ok = append(ok, x)
		}
	}
	if len(demoted) == 0 {
		return xs
	}
	return append(ok, demoted...)
}

// healthy leaves out the demoted providers, unless that leaves none.
func (s *Service) healthy(ps []providers.Provider) []providers.Provider {
	var out []providers.Provider
	for _, p := range ps {
		if !s.Health.Demoted(p.ID()) {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return ps
	}
	return out
}

// Streams asks the enabled providers (only the one whose id is provider,
// when it's set) for the streams of a movie or of a show's episode (key),
// or returns what they answered a moment ago unless refresh is set.
// Streams are ranked best first (see providers.Rank), the default
// provider's first when all are asked, and those of a show's version that
// played last before them (see Result.Preferred).
func (s *Service) Streams(ctx context.Context, media *catalog.Media, episode int, provider string, refresh bool) (*Result, error) {
	return s.streams(ctx, media, episode, provider, refresh, false, nil)
}

// streams is Streams. auto is set for the automatic picks, which leave the
// demoted providers out (when no provider is named) and take what the
// others answered a moment ago when it's still there, whichever other
// providers were asked with them. p receives the search's progress (nil:
// none).
func (s *Service) streams(ctx context.Context, media *catalog.Media, episode int, provider string, refresh, auto bool, p *Progress) (*Result, error) {
	episode, err := checkEpisode(media, episode)
	if err != nil {
		return nil, err
	}
	req := RequestFor(media, episode)
	ps, err := s.providersFor(req.Type, provider)
	if err != nil {
		return nil, err
	}
	if auto && provider == "" {
		ps = s.healthy(ps)
	}
	ids := make([]string, len(ps))
	for i, p := range ps {
		ids[i] = p.ID()
	}
	// The links all providers found a moment ago do for an automatic pick
	// among the healthy ones.
	if auto && !refresh && provider == "" {
		if c := s.covering(media.ID, episode, ids); c != nil {
			return s.result(media, req, c, ps, provider), nil
		}
	}
	var each func(Answer)
	if p != nil {
		if p.Asking != nil {
			p.Asking(s.asked(ps))
		}
		each = p.Answer
	}
	key := fmt.Sprintf("%d:%d:%s", media.ID, episode, strings.Join(ids, ","))
	c, err := s.lookup(ctx, key, refresh, func(ctx context.Context, report func(Answer)) *cached {
		return s.search(ctx, ps, req, episode, report)
	}, each)
	if err != nil {
		return nil, err
	}
	return s.result(media, req, c, ps, provider), nil
}

// covering returns what the providers ids answered a moment ago for an
// episode (asked with others or not), only theirs; nil when there's
// nothing fresh.
func (s *Service) covering(mediaID, episode int, ids []string) *cached {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	var hit *cached
	for _, c := range s.cache {
		if c.mediaID != mediaID || c.episode != episode || now.Sub(c.at) >= s.ttl() || !hasAll(c.providers, ids) {
			continue
		}
		if hit == nil || c.at.After(hit.at) {
			hit = c
		}
	}
	if hit == nil {
		return nil
	}
	return hit.only(ids)
}

func hasAll(set, ids []string) bool {
	in := make(map[string]bool, len(set))
	for _, x := range set {
		in[x] = true
	}
	for _, id := range ids {
		if !in[id] {
			return false
		}
	}
	return true
}

// only is what the providers ids answered, out of c.
func (c *cached) only(ids []string) *cached {
	keep := make(map[string]bool, len(ids))
	for _, id := range ids {
		keep[id] = true
	}
	out := *c
	out.providers = ids
	out.streams, out.errors, out.answers = nil, nil, nil
	for _, a := range c.answers {
		if keep[a.provider] {
			out.answers = append(out.answers, a)
		}
	}
	// A stream's provider may be named by the stream itself: what was
	// kept of each answer decides.
	mine := map[string]bool{}
	for _, a := range out.answers {
		for _, id := range a.ids {
			mine[id] = true
		}
	}
	for _, st := range c.streams {
		if mine[st.ID] {
			out.streams = append(out.streams, st)
		}
	}
	for _, e := range c.errors {
		if keep[e.Provider] {
			out.errors = append(out.errors, e)
		}
	}
	return &out
}

// asked describes the providers a search asks, the demoted ones last (as
// their streams come).
func (s *Service) asked(ps []providers.Provider) []Asked {
	out := make([]Asked, 0, len(ps))
	for _, p := range ps {
		out = append(out, Asked{Provider: p.ID(), Name: p.Name(), Demoted: s.Health.Demoted(p.ID())})
	}
	return demotedLast(out, func(a Asked) string { return a.Provider }, s.Health)
}

// result ranks what the providers ps answered (c) for req.
func (s *Service) result(media *catalog.Media, req providers.Request, c *cached, ps []providers.Provider, provider string) *Result {
	opts := s.options()
	res := &Result{
		MediaID: media.ID, Episode: c.episode, Request: req, Providers: []string{},
		Errors: append([]providers.ProviderError{}, c.errors...), Subtitles: append([]providers.Subtitle{}, c.subtitles...),
		FetchedAt: c.at.Unix(),
	}
	res.Streams = rank(c.streams, opts.PreferredQuality)
	if provider == "" && opts.DefaultProvider != "" {
		res.Streams = providerFirst(res.Streams, opts.DefaultProvider)
	}
	if provider == "" && media.IsShow() && s.Versions != nil {
		if v := s.Versions(media.ID); v != nil {
			res.Streams, res.Preferred = preferVersion(res.Streams, v), v
		}
	}
	res.Streams = demotedLast(res.Streams, func(st Stream) string { return st.Provider }, s.Health)
	for _, p := range ps {
		res.Providers = append(res.Providers, p.ID())
		if mp, ok := p.(Matcher); ok {
			if m := mp.CurrentMatch(media.ID); m != nil {
				res.Matches = append(res.Matches, *m)
			}
		}
	}
	return res
}

// lookup returns the cached result for key, or runs fn (once, however many
// requests ask at the same time) and caches what it found. each (when not
// nil) gets every provider's answer: as it comes during a search (the ones
// that came before too, for a request joining a search under way), all at
// once for a cached result.
func (s *Service) lookup(ctx context.Context, key string, refresh bool, fn func(context.Context, func(Answer)) *cached, each func(Answer)) (*cached, error) {
	s.mu.Lock()
	if c := s.cache[key]; c != nil && !refresh && s.now().Sub(c.at) < s.ttl() {
		s.mu.Unlock()
		if each != nil {
			for _, a := range c.replay() {
				each(a)
			}
		}
		return c, nil
	}
	f := s.flights[key]
	if f == nil {
		f = newFlight()
		s.flights[key] = f
		// The search goes on when the request that started it goes away:
		// another one may be waiting for it, and the result is kept.
		go func() {
			res := fn(context.WithoutCancel(ctx), f.add)
			s.mu.Lock()
			f.res = res
			delete(s.flights, key)
			// A total failure (nothing found, errors only) isn't kept: the
			// network may be back on the next try.
			if len(res.streams) > 0 || len(res.errors) == 0 {
				s.cache[key] = res
				s.pruneLocked()
				// Dropped once expired, also when no other search comes:
				// the streams of a few titles from many providers are
				// megabytes.
				s.afterFunc(s.ttl(), func() {
					s.mu.Lock()
					s.pruneLocked()
					s.mu.Unlock()
				})
			}
			s.mu.Unlock()
			close(f.done)
		}()
	}
	s.mu.Unlock()
	return f.wait(ctx, each)
}

// pruneLocked drops expired results.
func (s *Service) pruneLocked() {
	now := s.now()
	for k, c := range s.cache {
		if now.Sub(c.at) >= s.ttl() {
			delete(s.cache, k)
		}
	}
}

// search asks the providers (and the subtitle providers) at once, handing
// report (when not nil) each provider's answer as it comes.
func (s *Service) search(ctx context.Context, ps []providers.Provider, req providers.Request, episode int, report func(Answer)) *cached {
	timeout := s.options().Timeout
	if timeout <= 0 {
		// Nobody may be waiting for this search any more (see lookup):
		// it must end some day.
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
	}
	var subs []providers.Subtitle
	var wg sync.WaitGroup
	if s.SubtitleProviders != nil {
		if sps := s.SubtitleProviders(); len(sps) > 0 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				// Subtitle addons that fail don't matter much: no error
				// shows for them.
				subs, _ = providers.AggregateSubtitles(ctx, sps, req, timeout)
			}()
		}
	}
	c := &cached{mediaID: req.MediaID, episode: episode}
	for _, p := range ps {
		c.providers = append(c.providers, p.ID())
	}
	// Called one answer at a time.
	streams, errs := providers.AggregateEach(ctx, ps, req, timeout, func(a providers.Answer) {
		ans := s.answerOf(a)
		ids := make([]string, len(ans.Streams))
		for i, st := range ans.Streams {
			ids[i] = st.ID
		}
		c.answers = append(c.answers, answered{provider: ans.Provider, name: ans.Name, err: ans.Error, ms: ans.Ms, ids: ids})
		if report != nil {
			report(ans)
		}
	})
	wg.Wait()
	c.errors, c.subtitles, c.at = errs, subs, s.now()
	for _, st := range streams {
		c.streams = append(c.streams, Wrap(st))
	}
	if c.subtitles == nil {
		c.subtitles = []providers.Subtitle{}
	}
	return c
}

// rank sorts streams best first (providers.Rank) without touching the input.
func rank(streams []Stream, preferred string) []Stream {
	plain := make([]providers.Stream, len(streams))
	byID := make(map[string]Stream, len(streams))
	for i, s := range streams {
		plain[i] = s.Stream
		byID[s.ID] = s
	}
	ranked := providers.Rank(plain, preferred)
	out := make([]Stream, 0, len(ranked))
	for _, p := range ranked {
		out = append(out, byID[StreamID(p)])
	}
	return out
}

// providerFirst moves the streams of one provider before the others,
// keeping both groups' order.
func providerFirst(streams []Stream, provider string) []Stream {
	out := make([]Stream, 0, len(streams))
	for _, s := range streams {
		if s.Provider == provider {
			out = append(out, s)
		}
	}
	for _, s := range streams {
		if s.Provider != provider {
			out = append(out, s)
		}
	}
	return out
}

// ErrStreamGone is returned for a stream id the providers no longer give.
var ErrStreamGone = errors.New("this source is no longer available: look for sources again")

// Find returns the stream whose id is id among the streams of a movie or
// episode (found a moment ago, or asked for again), with the result it
// belongs to.
func (s *Service) Find(ctx context.Context, media *catalog.Media, episode int, id string) (Stream, *Result, error) {
	episode, err := checkEpisode(media, episode)
	if err != nil {
		return Stream{}, nil, err
	}
	// Results from a moment ago first, whichever providers were asked.
	s.mu.Lock()
	var hit *cached
	var found Stream
	now := s.now()
	for _, c := range s.cache {
		if c.mediaID != media.ID || c.episode != episode || now.Sub(c.at) >= s.ttl() {
			continue
		}
		for _, st := range c.streams {
			if st.ID == id && (hit == nil || c.at.After(hit.at)) {
				hit, found = c, st
			}
		}
	}
	s.mu.Unlock()
	if hit != nil {
		return found, s.result(media, RequestFor(media, episode), hit, nil, ""), nil
	}
	res, err := s.Streams(ctx, media, episode, "", false)
	if err != nil {
		return Stream{}, nil, err
	}
	for _, st := range res.Streams {
		if st.ID == id {
			return st, res, nil
		}
	}
	return Stream{}, res, ErrStreamGone
}

// Best returns the best direct link for a movie or episode from provider
// (all providers when ""), with the result it belongs to: what plays when
// no stream was picked (the next episode, the Play button). See BestFor.
func (s *Service) Best(ctx context.Context, media *catalog.Media, episode int, provider string) (Stream, *Result, error) {
	return s.BestFor(ctx, media, episode, Pick{Provider: provider})
}

// Pick is what an automatic pick looks for.
type Pick struct {
	// Provider's links only ("" for any provider's).
	Provider string
	// Version's links first (nil: for a show, the version that played
	// last, when it's Provider's or Provider is "").
	Version *Version
	// Refresh asks the providers again rather than taking the links found
	// a moment ago (the player does when none of them could be read).
	Refresh bool
}

// BestFor returns the best direct link for a movie or episode, with the
// result it belongs to. Without a provider named, the providers failing
// again and again are left out (unless they're all that's left), so they
// don't hold the pick up until their timeout; the links found a moment ago
// are used when they're still there.
func (s *Service) BestFor(ctx context.Context, media *catalog.Media, episode int, pick Pick) (Stream, *Result, error) {
	res, err := s.streams(ctx, media, episode, pick.Provider, pick.Refresh, true, nil)
	if err != nil {
		return Stream{}, nil, err
	}
	v := pick.Version
	if v == nil && media.IsShow() && s.Versions != nil {
		if last := s.Versions(media.ID); last != nil && (pick.Provider == "" || pick.Provider == last.Provider) {
			v = last
		}
	}
	if direct := preferVersion(res.Direct(), v); len(direct) > 0 {
		return direct[0], res, nil
	}
	return Stream{}, res, noDirect(res)
}

// noDirect explains why there's nothing to play in the app or mpv.
func noDirect(res *Result) error {
	if len(res.Streams) > 0 {
		return errors.New("no direct link found (only torrents or web pages): pick one in Watch online")
	}
	if len(res.Errors) > 0 {
		msgs := make([]string, 0, len(res.Errors))
		for _, e := range res.Errors {
			msgs = append(msgs, e.Error())
		}
		return errors.New("no stream found: " + strings.Join(msgs, "; "))
	}
	return errors.New("no stream found")
}

// Resolve finds a fresh direct link for a download from provider ("" for
// any): the one with the quality closest to quality ("1080p", "best" or ""
// for the best one). Links expire, so a download queued a while ago asks the
// provider again rather than using the one listed then.
func (s *Service) Resolve(ctx context.Context, media *catalog.Media, episode int, provider, quality string) (Stream, *Result, error) {
	res, err := s.Streams(ctx, media, episode, provider, true)
	if err != nil {
		return Stream{}, nil, err
	}
	direct := res.Direct()
	if len(direct) == 0 {
		return Stream{}, res, noDirect(res)
	}
	return closestQuality(direct, quality), res, nil
}

// closestQuality picks the stream whose quality is closest to want (the
// higher one of two equally close), the first one for "best" or "".
// Streams of unknown quality only come when there's nothing else.
func closestQuality(streams []Stream, want string) Stream {
	target := qualityNumber(providers.QualityFromText(want))
	if target == 0 {
		return streams[0]
	}
	best, bestDist, bestQ := -1, 0, 0
	for i, s := range streams {
		q := qualityNumber(s.Quality)
		if q == 0 {
			continue
		}
		d := q - target
		if d < 0 {
			d = -d
		}
		if best < 0 || d < bestDist || (d == bestDist && q > bestQ) {
			best, bestDist, bestQ = i, d, q
		}
	}
	if best < 0 {
		return streams[0]
	}
	return streams[best]
}

func qualityNumber(q string) int {
	n, _ := strconv.Atoi(strings.TrimSuffix(strings.ToLower(q), "p"))
	return n
}

// Matcher is implemented by providers that match titles by searching (the
// extension providers), which the user can correct by hand.
type Matcher interface {
	// CurrentMatch is the title used for a movie or show (nil when none was
	// looked for yet).
	CurrentMatch(mediaID int) *Match
	// SearchTitles searches the provider (to pick a title by hand).
	SearchTitles(ctx context.Context, query string, mediaID int) ([]SearchResult, error)
	// SetMatch uses the provider's title id for a movie or show from now
	// on.
	SetMatch(mediaID int, id, title string) error
}

// SearchResult is a title found by a manual search.
type SearchResult struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	SubOrDub string `json:"subOrDub,omitempty"`
}

// Matcher returns the enabled provider whose id is id when it matches
// titles by searching.
func (s *Service) Matcher(id string) (Matcher, error) {
	if s.Providers != nil {
		for _, p := range s.Providers() {
			if p != nil && p.ID() == id {
				if m, ok := p.(Matcher); ok {
					return m, nil
				}
				return nil, fmt.Errorf("%s finds titles by their ids: there's nothing to match by hand", p.Name())
			}
		}
	}
	return nil, fmt.Errorf("streaming provider %q is not installed or is disabled", id)
}

// SetMatch picks a provider's title for a movie or show by hand, and
// forgets the streams found with the previous one.
func (s *Service) SetMatch(provider string, mediaID int, id, title string) error {
	m, err := s.Matcher(provider)
	if err != nil {
		return err
	}
	if err := m.SetMatch(mediaID, id, title); err != nil {
		return err
	}
	s.mu.Lock()
	for k, c := range s.cache {
		if c.mediaID == mediaID {
			delete(s.cache, k)
		}
	}
	s.mu.Unlock()
	return nil
}

// ---------------------------------------------------------------------------
// Episode numbers
//
// Streaming sites number a show's episodes from its first one (1, 2, … 24);
// KumoBox by key (see catalog); a movie is episode 1.

// ProviderEpisode is a provider's number for the episode key of media.
func ProviderEpisode(media *catalog.Media, key int) float64 {
	if !media.IsShow() {
		return 1
	}
	if n := media.EpisodeIndex(key); n > 0 {
		return float64(n)
	}
	_, e := catalog.SplitKey(key)
	return float64(e)
}

// EpisodeKeyOf is the key of a provider's episode n of media.
func EpisodeKeyOf(media *catalog.Media, n float64) int {
	if !media.IsShow() {
		return catalog.MovieEpisode
	}
	if k := media.KeyAtIndex(int(n)); k > 0 {
		return k
	}
	return catalog.EpisodeKey(1, max(int(n), 1))
}

// SortSubtitles puts the subtitles in the preferred languages (a list like
// "eng,en"; English when empty) first, keeping their order otherwise, and
// keeps at most limit of them (all when limit <= 0).
func SortSubtitles(subs []providers.Subtitle, preferred string, limit int) []providers.Subtitle {
	langs := map[string]bool{}
	for _, l := range strings.Split(preferred, ",") {
		if l = normLang(l); l != "" {
			langs[l] = true
		}
	}
	if len(langs) == 0 {
		langs["eng"] = true
	}
	out := append([]providers.Subtitle(nil), subs...)
	sort.SliceStable(out, func(i, j int) bool {
		return langs[normLang(out[i].Language)] && !langs[normLang(out[j].Language)]
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// normLang maps a language code or name to its three-letter code when it's
// a common one.
func normLang(l string) string {
	l = strings.ToLower(strings.TrimSpace(l))
	if i := strings.IndexAny(l, "-_"); i > 0 {
		l = l[:i] // pt-BR
	}
	switch l {
	case "en", "english":
		return "eng"
	case "es", "spanish", "español":
		return "spa"
	case "fr", "fra", "french", "français":
		return "fre"
	case "de", "deu", "german", "deutsch":
		return "ger"
	case "it", "italian":
		return "ita"
	case "pt", "portuguese":
		return "por"
	case "ru", "russian":
		return "rus"
	case "ja", "jp", "japanese":
		return "jpn"
	case "zh", "zho", "chinese":
		return "chi"
	case "ko", "korean":
		return "kor"
	case "ar", "arabic":
		return "ara"
	case "nl", "dutch":
		return "dut"
	case "pl", "polish":
		return "pol"
	case "tr", "turkish":
		return "tur"
	}
	return l
}

// IsEnglish reports whether a subtitle's language is English.
func IsEnglish(lang string) bool { return normLang(lang) == "eng" }

// SplitReferrer takes the Referer out of a stream's headers (mpv and the
// downloaders have an option of their own for it).
func SplitReferrer(h map[string]string) (map[string]string, string) {
	out := map[string]string{}
	referrer := ""
	for k, v := range h {
		if strings.EqualFold(k, "referer") {
			referrer = v
			continue
		}
		out[k] = v
	}
	return out, referrer
}
