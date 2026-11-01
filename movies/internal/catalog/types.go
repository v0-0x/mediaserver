// Package catalog is KumoBox's movie and show catalog: metadata from TMDB
// (The Movie Database) and the user's list, kept in the local database and
// synced with Letterboxd (see package letterboxd).
//
// The shapes keep the field names Kumo's AniList types had (title,
// coverImage, bannerImage, episodes, nextAiringEpisode...), so the web UI
// and the code shared with Kumo read them the same way; see ids.go for how
// movies, shows and episodes are numbered.
package catalog

import (
	"sort"
	"strings"
)

type FuzzyDate struct {
	Year  *int `json:"year"`
	Month *int `json:"month"`
	Day   *int `json:"day"`
}

// Title holds a media's names. English is the title in the metadata
// language (Settings › Metadata, English by default), Native the original
// title, Romaji the original title when it's written in Latin letters (used
// to match file and release names). UserPreferred is what the app shows.
type Title struct {
	Romaji        string `json:"romaji"`
	English       string `json:"english"`
	Native        string `json:"native"`
	UserPreferred string `json:"userPreferred"`
}

// CoverImage is the poster in three sizes (TMDB w780, w500 and w342).
type CoverImage struct {
	ExtraLarge string `json:"extraLarge"`
	Large      string `json:"large"`
	Medium     string `json:"medium"`
	Color      string `json:"color"`
}

// AiringEpisode is an episode of a show and when it airs. Episode is its key
// (see ids.go). TMDB only has air dates: AiringAt is that day at 00:00 UTC.
type AiringEpisode struct {
	ID              int    `json:"id,omitempty"`
	AiringAt        int    `json:"airingAt"`
	TimeUntilAiring int    `json:"timeUntilAiring"`
	Episode         int    `json:"episode"`
	Title           string `json:"title,omitempty"`
	Media           *Media `json:"media,omitempty"`
}

// Studio is a production company (movies) or a network (shows).
type Studio struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Logo string `json:"logo,omitempty"`
}

// Tag is a TMDB keyword.
type Tag struct {
	Name           string `json:"name"`
	Rank           int    `json:"rank"`
	IsMediaSpoiler bool   `json:"isMediaSpoiler"`
}

// Trailer is a YouTube trailer (Site "youtube", ID the video id).
type Trailer struct {
	ID        string `json:"id"`
	Site      string `json:"site"`
	Thumbnail string `json:"thumbnail"`
}

type Person struct {
	ID   int `json:"id"`
	Name struct {
		Full string `json:"full"`
	} `json:"name"`
	Image struct {
		Large string `json:"large"`
	} `json:"image"`
}

// CharacterEdge is one cast member: Node is the character (named after the
// role, with the actor's photo), VoiceActors holds the actor. Role is MAIN
// for the top-billed cast, SUPPORTING for the others.
type CharacterEdge struct {
	Role        string   `json:"role"`
	Node        Person   `json:"node"`
	VoiceActors []Person `json:"voiceActors"`
}

// CrewMember is a director, creator or writer.
type CrewMember struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Job   string `json:"job"`
	Image string `json:"image,omitempty"`
}

// RelationEdge links a movie to the other movies of its collection
// (RelationType PREQUEL or SEQUEL by release date, or OTHER).
type RelationEdge struct {
	RelationType string `json:"relationType"`
	Node         *Media `json:"node"`
}

type ListEntryLite struct {
	ID       int     `json:"id"`
	Status   string  `json:"status"`
	Progress int     `json:"progress"`
	Score    float64 `json:"score"`
	Repeat   int     `json:"repeat"`
}

// Season is one season of a show, as listed with the show (its episodes
// come with SeasonDetail). Number 0 holds the specials.
type Season struct {
	Number       int    `json:"seasonNumber"`
	Name         string `json:"name"`
	EpisodeCount int    `json:"episodeCount"`
	AirDate      string `json:"airDate,omitempty"` // YYYY-MM-DD
	Poster       string `json:"poster,omitempty"`
}

// Episode is one episode of a show, from its season's details.
type Episode struct {
	Key      int     `json:"key"`
	Season   int     `json:"season"`
	Number   int     `json:"number"`
	Title    string  `json:"title"`
	Overview string  `json:"overview,omitempty"`
	Still    string  `json:"still,omitempty"`
	AirDate  string  `json:"airDate,omitempty"` // YYYY-MM-DD
	Runtime  int     `json:"runtime,omitempty"` // minutes
	Rating   float64 `json:"rating,omitempty"`  // 0-10
}

// SeasonDetail is a season with its episodes.
type SeasonDetail struct {
	MediaID  int        `json:"mediaId"`
	Number   int        `json:"seasonNumber"`
	Name     string     `json:"name"`
	Overview string     `json:"overview,omitempty"`
	Poster   string     `json:"poster,omitempty"`
	AirDate  string     `json:"airDate,omitempty"`
	Episodes []*Episode `json:"episodes"`
}

// WatchProvider is a streaming service, store or channel that has a title
// (TMDB's "where to watch", from JustWatch).
type WatchProvider struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Logo string `json:"logo"`
}

// WatchProviders lists where a title can be watched in one country.
type WatchProviders struct {
	Region   string          `json:"region"`
	Link     string          `json:"link,omitempty"`
	Flatrate []WatchProvider `json:"flatrate,omitempty"`
	Free     []WatchProvider `json:"free,omitempty"`
	Ads      []WatchProvider `json:"ads,omitempty"`
	Rent     []WatchProvider `json:"rent,omitempty"`
	Buy      []WatchProvider `json:"buy,omitempty"`
}

// CollectionRef is the TMDB collection (franchise) a movie belongs to.
type CollectionRef struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Poster   string `json:"poster,omitempty"`
	Backdrop string `json:"backdrop,omitempty"`
}

// Media is a movie or a show.
type Media struct {
	// ID is the KumoBox media id (see ids.go), TMDBID the TMDB one.
	ID     int    `json:"id"`
	TMDBID int    `json:"tmdbId"`
	IMDbID string `json:"imdbId,omitempty"`
	TVDBID int    `json:"tvdbId,omitempty"`
	// Type is MOVIE or TV; Format the same, or MINISERIES for a TV
	// miniseries.
	Type   string `json:"type"`
	Format string `json:"format"`
	// Status is FINISHED (released / ended), RELEASING (a show still
	// airing), NOT_YET_RELEASED or CANCELLED.
	Status     string `json:"status"`
	Season     string `json:"season"`
	SeasonYear *int   `json:"seasonYear"`
	// Episodes is how many episodes a show has (specials not counted), 1 for
	// a movie. Duration is the runtime in minutes (per episode for shows).
	Episodes         *int       `json:"episodes"`
	Duration         *int       `json:"duration"`
	IsAdult          bool       `json:"isAdult"`
	Title            Title      `json:"title"`
	Synonyms         []string   `json:"synonyms"`
	CoverImage       CoverImage `json:"coverImage"`
	BannerImage      string     `json:"bannerImage"`
	Genres           []string   `json:"genres"`
	AverageScore     *int       `json:"averageScore"` // TMDB rating x 10
	MeanScore        *int       `json:"meanScore"`
	Popularity       int        `json:"popularity"`
	VoteCount        int        `json:"voteCount,omitempty"`
	StartDate        FuzzyDate  `json:"startDate"`
	EndDate          FuzzyDate  `json:"endDate"`
	Tagline          string     `json:"tagline,omitempty"`
	OriginalLanguage string     `json:"originalLanguage,omitempty"`
	Certification    string     `json:"certification,omitempty"`
	// Seasons are a show's seasons, specials (number 0) included.
	Seasons []Season `json:"seasons,omitempty"`
	// LatestEpisode is the key of the last episode of a show that aired,
	// NextAiringEpisode the next one to air (nil when none is announced).
	LatestEpisode     int            `json:"latestEpisode,omitempty"`
	NextAiringEpisode *AiringEpisode `json:"nextAiringEpisode"`
	Description       string         `json:"description,omitempty"`
	CountryOfOrigin   string         `json:"countryOfOrigin,omitempty"`
	SiteURL           string         `json:"siteUrl,omitempty"`
	// BelongsTo is the collection (franchise) of a movie.
	BelongsTo *CollectionRef `json:"belongsTo,omitempty"`

	// Detail-only fields
	Trailer *Trailer `json:"trailer,omitempty"`
	Studios *struct {
		Nodes []Studio `json:"nodes"`
	} `json:"studios,omitempty"`
	Tags      []Tag `json:"tags,omitempty"`
	Relations *struct {
		Edges []RelationEdge `json:"edges"`
	} `json:"relations,omitempty"`
	Recommendations *struct {
		Nodes []struct {
			MediaRecommendation *Media `json:"mediaRecommendation"`
		} `json:"nodes"`
	} `json:"recommendations,omitempty"`
	Characters *struct {
		Edges []CharacterEdge `json:"edges"`
	} `json:"characters,omitempty"`
	Crew           []CrewMember    `json:"crew,omitempty"`
	WatchProviders *WatchProviders `json:"watchProviders,omitempty"`
	MediaListEntry *ListEntryLite  `json:"mediaListEntry,omitempty"`
}

// IsShow reports whether m is a show.
func (m *Media) IsShow() bool { return m != nil && (m.Type == TypeTV || IsShow(m.ID)) }

// AllTitles returns every known title of the media (deduplicated).
func (m *Media) AllTitles() []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range append([]string{m.Title.English, m.Title.Romaji, m.Title.UserPreferred, m.Title.Native}, m.Synonyms...) {
		t = strings.TrimSpace(t)
		if t == "" || seen[strings.ToLower(t)] {
			continue
		}
		seen[strings.ToLower(t)] = true
		out = append(out, t)
	}
	return out
}

// PreferredTitle returns the title the app shows.
func (m *Media) PreferredTitle() string {
	for _, t := range []string{m.Title.UserPreferred, m.Title.English, m.Title.Romaji, m.Title.Native} {
		if t != "" {
			return t
		}
	}
	return ""
}

// EnglishOrRomaji prefers the localized title (used for search providers),
// then the original one.
func (m *Media) EnglishOrRomaji() string {
	if m.Title.English != "" {
		return m.Title.English
	}
	return m.PreferredTitle()
}

// Year returns the release (first air) year or 0.
func (m *Media) Year() int {
	if m.SeasonYear != nil {
		return *m.SeasonYear
	}
	if m.StartDate.Year != nil {
		return *m.StartDate.Year
	}
	return 0
}

// Lite returns a copy without the heavy detail fields, suitable for lists.
// Seasons stay: the episode helpers below need them.
func (m *Media) Lite() *Media {
	if m == nil {
		return nil
	}
	c := *m
	c.Description = ""
	c.Trailer = nil
	c.Studios = nil
	c.Tags = nil
	c.Relations = nil
	c.Recommendations = nil
	c.Characters = nil
	c.Crew = nil
	c.WatchProviders = nil
	c.MediaListEntry = nil
	return &c
}

// ---------------------------------------------------------------------------
// Episodes of a show, from its season list. Specials (season 0) are left out
// of the order: they are never "next", nor the last episode.

// EpisodeKeys returns the keys of all regular episodes, in order. A movie
// has the one key MovieEpisode.
func (m *Media) EpisodeKeys() []int {
	if m == nil {
		return nil
	}
	if !m.IsShow() {
		return []int{MovieEpisode}
	}
	seasons := append([]Season(nil), m.Seasons...)
	sort.Slice(seasons, func(i, j int) bool { return seasons[i].Number < seasons[j].Number })
	var out []int
	for _, s := range seasons {
		if s.Number <= 0 {
			continue
		}
		for e := 1; e <= s.EpisodeCount; e++ {
			out = append(out, EpisodeKey(s.Number, e))
		}
	}
	return out
}

// TotalEpisodes returns the number of regular episodes (1 for a movie), 0
// when unknown.
func (m *Media) TotalEpisodes() int {
	if m == nil {
		return 0
	}
	if !m.IsShow() {
		return 1
	}
	if n := len(m.EpisodeKeys()); n > 0 {
		return n
	}
	if m.Episodes != nil {
		return *m.Episodes
	}
	return 0
}

// FirstEpisode is the key of the first regular episode (S01E01 when the
// seasons aren't known), or MovieEpisode for a movie.
func (m *Media) FirstEpisode() int {
	if keys := m.EpisodeKeys(); len(keys) > 0 {
		return keys[0]
	}
	return EpisodeKey(1, 1)
}

// LastEpisode is the key of the last regular episode known (aired or not),
// 0 when unknown.
func (m *Media) LastEpisode() int {
	keys := m.EpisodeKeys()
	if len(keys) == 0 {
		return 0
	}
	return keys[len(keys)-1]
}

// AiredEpisodes returns how many regular episodes have aired (1 for a
// released movie).
func (m *Media) AiredEpisodes() int {
	if m == nil || m.Status == "NOT_YET_RELEASED" {
		return 0
	}
	if !m.IsShow() {
		return 1
	}
	if m.LatestEpisode > 0 {
		return m.EpisodeIndex(m.LatestEpisode)
	}
	if m.Status == "FINISHED" || m.Status == "CANCELLED" {
		return m.TotalEpisodes()
	}
	return 0
}

// LatestAired is the key of the last regular episode that aired, 0 if none.
func (m *Media) LatestAired() int {
	if m == nil || m.Status == "NOT_YET_RELEASED" {
		return 0
	}
	if !m.IsShow() {
		return MovieEpisode
	}
	if m.LatestEpisode > 0 && m.LatestEpisode >= keySeason {
		return m.LatestEpisode
	}
	if m.Status == "FINISHED" || m.Status == "CANCELLED" {
		return m.LastEpisode()
	}
	return 0
}

// NextEpisode is the key of the regular episode after key (the first one
// for key 0), 0 when there is none. For a key between known episodes (or a
// special) it's the first regular episode after it.
func (m *Media) NextEpisode(key int) int {
	if m != nil && !m.IsShow() {
		if key < MovieEpisode {
			return MovieEpisode
		}
		return 0
	}
	keys := m.EpisodeKeys()
	i := sort.SearchInts(keys, key+1)
	if i < len(keys) {
		return keys[i]
	}
	if len(keys) == 0 && key == 0 {
		return EpisodeKey(1, 1)
	}
	return 0
}

// PrevEpisode is the key of the regular episode before key, 0 if none.
func (m *Media) PrevEpisode(key int) int {
	if m != nil && !m.IsShow() {
		return 0
	}
	keys := m.EpisodeKeys()
	i := sort.SearchInts(keys, key)
	if i > 0 {
		return keys[i-1]
	}
	return 0
}

// EpisodeIndex is how many regular episodes come up to and including key
// (S02E03 of a show with 10 episodes in season 1 is 13): the count shown as
// "13 / 24". It is 0 before the first episode.
func (m *Media) EpisodeIndex(key int) int {
	if m != nil && !m.IsShow() {
		if key >= MovieEpisode {
			return 1
		}
		return 0
	}
	keys := m.EpisodeKeys()
	if len(keys) == 0 {
		_, e := SplitKey(key)
		return e
	}
	return sort.SearchInts(keys, key+1)
}

// KeyAtIndex is the key of the n-th regular episode (1-based), 0 when out of
// range: the inverse of EpisodeIndex, for files and releases numbered
// absolutely (episode 13 of a show with 10 episodes in season 1 is S02E03).
func (m *Media) KeyAtIndex(n int) int {
	if m != nil && !m.IsShow() {
		if n == 1 {
			return MovieEpisode
		}
		return 0
	}
	keys := m.EpisodeKeys()
	if n < 1 || n > len(keys) {
		return 0
	}
	return keys[n-1]
}

// SeasonEpisodeCount is the number of episodes of season s, 0 if unknown.
func (m *Media) SeasonEpisodeCount(s int) int {
	if m == nil {
		return 0
	}
	for _, x := range m.Seasons {
		if x.Number == s {
			return x.EpisodeCount
		}
	}
	return 0
}

// ---------------------------------------------------------------------------
// The user's list

// List statuses. The UI names them Watching, Rewatching, Watchlist, Paused,
// Watched and Dropped.
var Statuses = []string{"CURRENT", "REPEATING", "PLANNING", "PAUSED", "COMPLETED", "DROPPED"}

// ListEntry is a media in the user's list. Progress is the key of the last
// episode watched (MovieEpisode for a watched movie). Score is 0-100
// (Letterboxd's half stars are 10 points each). Notes is the review.
type ListEntry struct {
	ID          int       `json:"id"`
	MediaID     int       `json:"mediaId"`
	Status      string    `json:"status"`
	Progress    int       `json:"progress"`
	Score       float64   `json:"score"`
	Repeat      int       `json:"repeat"`
	Notes       string    `json:"notes"`
	Private     bool      `json:"private"`
	Liked       bool      `json:"liked"`
	StartedAt   FuzzyDate `json:"startedAt"`
	CompletedAt FuzzyDate `json:"completedAt"`
	UpdatedAt   int64     `json:"updatedAt"`
	// Letterboxd is set on movies whose state came from (or went to)
	// Letterboxd: the film's Letterboxd page and when it was last synced.
	LetterboxdURI    string `json:"letterboxdUri,omitempty"`
	LetterboxdSynced int64  `json:"letterboxdSynced,omitempty"`
	Media            *Media `json:"media"`
}

type List struct {
	Name         string       `json:"name"`
	Status       string       `json:"status"`
	IsCustomList bool         `json:"isCustomList"`
	Entries      []*ListEntry `json:"entries"`
}

// Collection is a user's list. The Platform shares one Collection between
// all its readers, so it is never modified in place.
type Collection struct {
	Lists []*List `json:"lists"`
}

// Entries returns all non-custom-list entries, deduplicated by media id.
func (c *Collection) Entries() []*ListEntry {
	if c == nil {
		return nil
	}
	seen := map[int]bool{}
	var out []*ListEntry
	for _, l := range c.Lists {
		if l.IsCustomList {
			continue
		}
		for _, e := range l.Entries {
			if e.Media == nil || seen[e.MediaID] {
				continue
			}
			seen[e.MediaID] = true
			out = append(out, e)
		}
	}
	return out
}

// Find returns the entry for a media id: the one Entries has. It looks
// through the lists without building Entries' slice, which for a list of
// a thousand titles looked up one by one (a page of the diary, a batch of
// titles) made hundreds of megabytes of garbage.
func (c *Collection) Find(mediaID int) *ListEntry {
	if c == nil {
		return nil
	}
	for _, l := range c.Lists {
		if l.IsCustomList {
			continue
		}
		for _, e := range l.Entries {
			if e.MediaID == mediaID && e.Media != nil {
				return e
			}
		}
	}
	return nil
}

// Viewer is the connected Letterboxd member (nil when none is connected).
// ID is derived from the username, so KumoBoxes connected to the same
// Letterboxd account recognise each other (library sharing).
type Viewer struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Avatar struct {
		Large  string `json:"large"`
		Medium string `json:"medium"`
	} `json:"avatar"`
	BannerImage string `json:"bannerImage"`
	Options     struct {
		DisplayAdultContent bool `json:"displayAdultContent"`
	} `json:"options"`
	MediaListOptions struct {
		ScoreFormat string `json:"scoreFormat"`
	} `json:"mediaListOptions"`
	// Letterboxd is the member's profile page.
	Letterboxd string `json:"letterboxd,omitempty"`
}

type PageInfo struct {
	Total       int  `json:"total"`
	CurrentPage int  `json:"currentPage"`
	LastPage    int  `json:"lastPage"`
	HasNextPage bool `json:"hasNextPage"`
}

type MediaPage struct {
	PageInfo PageInfo `json:"pageInfo"`
	Media    []*Media `json:"media"`
}

// SearchParams are the filters of the Search page. Type is MOVIE, TV or
// empty for both. Genres are genre names. Sort takes POPULARITY_DESC,
// SCORE_DESC, START_DATE_DESC, START_DATE, TITLE or TRENDING_DESC.
// WatchProviders filters by TMDB watch provider ids (in Settings' region).
// IDs fetches those media ids instead of searching.
type SearchParams struct {
	Page           int      `json:"page"`
	PerPage        int      `json:"perPage"`
	Search         string   `json:"search"`
	Type           string   `json:"type"`
	Genres         []string `json:"genres"`
	Year           int      `json:"year"`
	Status         string   `json:"status"`
	Sort           []string `json:"sort"`
	IsAdult        *bool    `json:"isAdult"`
	IDs            []int    `json:"ids"`
	Language       string   `json:"language"` // original language, ISO 639-1
	MinScore       int      `json:"minScore"` // 0-100
	WatchProviders []int    `json:"watchProviders"`
}

// EntryUpdate is the payload to create/update a list entry.
type EntryUpdate struct {
	MediaID     int        `json:"mediaId"`
	Status      *string    `json:"status,omitempty"`
	Progress    *int       `json:"progress,omitempty"`
	Score       *float64   `json:"score,omitempty"` // 0-100
	Repeat      *int       `json:"repeat,omitempty"`
	Notes       *string    `json:"notes,omitempty"`
	Liked       *bool      `json:"liked,omitempty"`
	StartedAt   *FuzzyDate `json:"startedAt,omitempty"`
	CompletedAt *FuzzyDate `json:"completedAt,omitempty"`
}

// applyTo sets the fields the update carries on e.
func (u EntryUpdate) applyTo(e *ListEntry) {
	if u.Status != nil {
		e.Status = *u.Status
	}
	if u.Progress != nil {
		e.Progress = *u.Progress
	}
	if u.Score != nil {
		e.Score = *u.Score
	}
	if u.Repeat != nil {
		e.Repeat = *u.Repeat
	}
	if u.Notes != nil {
		e.Notes = *u.Notes
	}
	if u.Liked != nil {
		e.Liked = *u.Liked
	}
	if u.StartedAt != nil {
		e.StartedAt = *u.StartedAt
	}
	if u.CompletedAt != nil {
		e.CompletedAt = *u.CompletedAt
	}
}

// DiaryEntry is one viewing of a movie (or a show, finished): what
// Letterboxd calls a diary entry. KumoBox logs one when a movie is watched
// to the end, and imports Letterboxd's. Rating is 0-100 (0: none).
type DiaryEntry struct {
	ID          int64  `json:"id"`
	MediaID     int    `json:"mediaId"`
	WatchedDate string `json:"watchedDate"` // YYYY-MM-DD
	Rating      int    `json:"rating"`
	Rewatch     bool   `json:"rewatch"`
	Liked       bool   `json:"liked"`
	Review      string `json:"review,omitempty"`
	Tags        string `json:"tags,omitempty"`
	// Source is "kumobox" (logged here) or "letterboxd" (imported).
	Source string `json:"source"`
	// LetterboxdID identifies an imported entry (the RSS guid or the
	// diary URI), so importing twice doesn't duplicate it.
	LetterboxdID string `json:"letterboxdId,omitempty"`
	// ExportedAt is when a KumoBox entry went to Letterboxd (CSV export or
	// API), 0 while it's still to be sent.
	ExportedAt int64  `json:"exportedAt,omitempty"`
	CreatedAt  int64  `json:"createdAt"`
	Media      *Media `json:"media,omitempty"`
}
