package catalog

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Turning TMDB's movies and shows into Media (see types.go for what each
// field holds).

// Image sizes.
const (
	posterXL    = "w780"
	posterL     = "w500"
	posterM     = "w342"
	backdrop    = "w1280"
	profileSize = "w185"
	logoSize    = "w185"
	stillSize   = "w300"
	iconSize    = "w92"
)

// Cast members shown as MAIN; the rest are SUPPORTING.
const mainCast = 6

// Limits on the lists of a title page.
const (
	maxCast            = 20
	maxRecommendations = 20
	maxSynonyms        = 10
)

// mapping holds what turning TMDB data into Media needs besides the data:
// the genre names (lists only give genre ids), the region (certification,
// where to watch) and today's date.
type mapping struct {
	c      *Client
	genres map[string]map[int]string // by kind
	region string
	now    time.Time
}

// kindOf is the kind of a TMDB item: what it says it is in a mixed list,
// else what was asked for.
func kindOf(it *tmdbItem, kind string) string {
	switch it.MediaType {
	case "tv":
		return TypeTV
	case "movie":
		return TypeMovie
	}
	return kind
}

// media maps a movie or show, in a list or with its whole page.
func (mp *mapping) media(it *tmdbItem, kind string) *Media {
	if it == nil || it.ID <= 0 {
		return nil
	}
	kind = kindOf(it, kind)
	c := mp.c
	m := &Media{TMDBID: it.ID, Type: kind, Format: kind, IsAdult: it.Adult, Genres: []string{}, Synonyms: []string{}}
	tv := kind == TypeTV
	if tv {
		m.ID = ShowID(it.ID)
		m.Title.English, m.Title.Native = it.Name, it.OriginalName
		m.StartDate = parseDate(it.FirstAirDate)
		m.SiteURL = "https://www.themoviedb.org/tv/" + strconv.Itoa(it.ID)
		if it.Type == "Miniseries" {
			m.Format = "MINISERIES"
		}
	} else {
		m.ID = MovieID(it.ID)
		m.Title.English, m.Title.Native = it.Title, it.OriginalTitle
		m.StartDate = parseDate(it.ReleaseDate)
		m.SiteURL = "https://www.themoviedb.org/movie/" + strconv.Itoa(it.ID)
		one := 1
		m.Episodes = &one
	}
	if isLatin(m.Title.Native) {
		m.Title.Romaji = m.Title.Native
	}
	m.Title.UserPreferred = m.Title.English
	if m.Title.UserPreferred == "" {
		m.Title.UserPreferred = m.Title.Native
	}
	if m.StartDate.Year != nil {
		y := *m.StartDate.Year
		m.SeasonYear = &y
	}

	m.Description = it.Overview
	m.Tagline = it.Tagline
	m.OriginalLanguage = it.OriginalLanguage
	m.CoverImage = CoverImage{
		ExtraLarge: c.Image(posterXL, it.PosterPath),
		Large:      c.Image(posterL, it.PosterPath),
		Medium:     c.Image(posterM, it.PosterPath),
	}
	m.BannerImage = c.Image(backdrop, it.BackdropPath)
	m.Popularity = int(math.Round(it.Popularity))
	m.VoteCount = it.VoteCount
	if it.VoteCount > 0 {
		score := int(math.Round(it.VoteAverage * 10))
		m.AverageScore = &score
	}
	if len(it.Genres) > 0 {
		for _, g := range it.Genres {
			m.Genres = append(m.Genres, g.Name)
		}
	} else {
		names := mp.genres[kind]
		for _, id := range it.GenreIDs {
			if n, ok := names[id]; ok {
				m.Genres = append(m.Genres, n)
			}
		}
	}
	if len(it.OriginCountry) > 0 {
		m.CountryOfOrigin = it.OriginCountry[0]
	} else if len(it.ProductionCountries) > 0 {
		m.CountryOfOrigin = it.ProductionCountries[0].ISO
	}
	m.Status = mp.status(it, kind)

	if tv {
		mp.showDetails(m, it)
	} else {
		mp.movieDetails(m, it)
	}
	mp.common(m, it)
	return m
}

// status maps TMDB's status. A title in a list comes without one: a movie
// is out once its release date has passed, a show isn't out before its
// first air date (whether it still airs isn't known).
func (mp *mapping) status(it *tmdbItem, kind string) string {
	switch it.Status {
	case "Released", "Ended":
		return "FINISHED"
	case "Returning Series":
		return "RELEASING"
	case "Canceled", "Cancelled":
		return "CANCELLED"
	case "":
		date := it.ReleaseDate
		if kind == TypeTV {
			date = it.FirstAirDate
		}
		d, err := time.Parse(time.DateOnly, date)
		if err != nil || d.After(mp.now) {
			return "NOT_YET_RELEASED"
		}
		if kind == TypeTV {
			return ""
		}
		return "FINISHED"
	}
	return "NOT_YET_RELEASED" // Rumored, Planned, In Production, Pilot...
}

func (mp *mapping) movieDetails(m *Media, it *tmdbItem) {
	if it.Runtime > 0 {
		d := it.Runtime
		m.Duration = &d
	}
	m.IMDbID = it.IMDbID
	if col := it.BelongsToCollection; col != nil && col.ID > 0 {
		m.BelongsTo = &CollectionRef{ID: col.ID, Name: col.Name, Poster: mp.c.Image(posterM, col.PosterPath), Backdrop: mp.c.Image(backdrop, col.BackdropPath)}
	}
	if len(it.ProductionCompanies) > 0 {
		m.Studios = studios(mp.c, it.ProductionCompanies)
	}
	if it.ReleaseDates != nil {
		m.Certification = movieCertification(it, mp.region)
	}
	if it.Credits != nil {
		for _, cr := range it.Credits.Crew {
			switch cr.Job {
			case "Director":
				m.Crew = addCrew(mp.c, m.Crew, cr, "Director")
			case "Screenplay", "Writer":
				m.Crew = addCrew(mp.c, m.Crew, cr, "Writer")
			}
		}
	}
}

func (mp *mapping) showDetails(m *Media, it *tmdbItem) {
	if it.NumberOfEpisodes > 0 {
		n := it.NumberOfEpisodes
		m.Episodes = &n
	}
	switch {
	case len(it.EpisodeRunTime) > 0 && it.EpisodeRunTime[0] > 0:
		d := it.EpisodeRunTime[0]
		m.Duration = &d
	case it.LastEpisodeToAir != nil && it.LastEpisodeToAir.Runtime > 0:
		d := it.LastEpisodeToAir.Runtime
		m.Duration = &d
	}
	for _, s := range it.Seasons {
		m.Seasons = append(m.Seasons, Season{
			Number: s.SeasonNumber, Name: s.Name, EpisodeCount: s.EpisodeCount, AirDate: s.AirDate,
			Poster: mp.c.Image(posterM, s.PosterPath),
		})
	}
	sort.SliceStable(m.Seasons, func(i, j int) bool { return m.Seasons[i].Number < m.Seasons[j].Number })
	if e := it.LastEpisodeToAir; e != nil && e.SeasonNumber > 0 && e.EpisodeNumber > 0 {
		m.LatestEpisode = EpisodeKey(e.SeasonNumber, e.EpisodeNumber)
	}
	if e := it.NextEpisodeToAir; e != nil && e.EpisodeNumber > 0 {
		if day, err := time.Parse(time.DateOnly, e.AirDate); err == nil {
			at := int(day.Unix())
			m.NextAiringEpisode = &AiringEpisode{
				ID: e.ID, AiringAt: at, TimeUntilAiring: at - int(mp.now.Unix()),
				Episode: EpisodeKey(e.SeasonNumber, e.EpisodeNumber), Title: e.Name,
			}
		}
	}
	if m.Status == "FINISHED" || m.Status == "CANCELLED" {
		m.EndDate = parseDate(it.LastAirDate)
	}
	if len(it.Networks) > 0 {
		m.Studios = studios(mp.c, it.Networks)
	}
	if it.ContentRatings != nil {
		m.Certification = showCertification(it, mp.region)
	}
	for _, cr := range it.CreatedBy {
		m.Crew = addCrew(mp.c, m.Crew, cr, "Creator")
	}
	if it.Credits != nil {
		for _, cr := range it.Credits.Crew {
			if cr.Job == "Director" {
				m.Crew = addCrew(mp.c, m.Crew, cr, "Director")
			}
		}
	}
}

// common maps the parts of a title page movies and shows share.
func (mp *mapping) common(m *Media, it *tmdbItem) {
	if ids := it.ExternalIDs; ids != nil {
		if ids.IMDbID != "" {
			m.IMDbID = ids.IMDbID
		}
		m.TVDBID = ids.TVDBID
	}
	if it.AlternativeTitles != nil {
		m.Synonyms = synonyms(m, append(it.AlternativeTitles.Titles, it.AlternativeTitles.Results...))
	}
	if it.Videos != nil {
		m.Trailer = trailer(it.Videos.Results)
	}
	if kw := it.Keywords; kw != nil {
		for _, k := range append(kw.Keywords, kw.Results...) {
			m.Tags = append(m.Tags, Tag{Name: k.Name})
		}
	}
	if it.Credits != nil && len(it.Credits.Cast) > 0 {
		cast := append([]tmdbCast(nil), it.Credits.Cast...)
		sort.SliceStable(cast, func(i, j int) bool { return cast[i].Order < cast[j].Order })
		m.Characters = &struct {
			Edges []CharacterEdge `json:"edges"`
		}{}
		for i, cm := range cast[:min(len(cast), maxCast)] {
			role := "SUPPORTING"
			if i < mainCast {
				role = "MAIN"
			}
			var character, actor Person
			character.ID = cm.ID
			character.Name.Full = cm.Character
			character.Image.Large = mp.c.Image(profileSize, cm.ProfilePath)
			actor.ID = cm.ID
			actor.Name.Full = cm.Name
			actor.Image.Large = character.Image.Large
			m.Characters.Edges = append(m.Characters.Edges, CharacterEdge{Role: role, Node: character, VoiceActors: []Person{actor}})
		}
	}
	recs := it.Recommendations
	if recs == nil || len(recs.Results) == 0 {
		recs = it.Similar
	}
	if recs != nil && len(recs.Results) > 0 {
		m.Recommendations = &struct {
			Nodes []struct {
				MediaRecommendation *Media `json:"mediaRecommendation"`
			} `json:"nodes"`
		}{}
		for i := range recs.Results {
			if len(m.Recommendations.Nodes) >= maxRecommendations {
				break
			}
			r := mp.media(&recs.Results[i], m.Type)
			if r == nil || r.ID == m.ID {
				continue
			}
			m.Recommendations.Nodes = append(m.Recommendations.Nodes, struct {
				MediaRecommendation *Media `json:"mediaRecommendation"`
			}{r.Lite()})
		}
	}
	if wp := it.WatchProviders; wp != nil && mp.region != "" {
		if r, ok := wp.Results[mp.region]; ok {
			m.WatchProviders = &WatchProviders{
				Region: mp.region, Link: r.Link,
				Flatrate: providers(mp.c, r.Flatrate), Free: providers(mp.c, r.Free), Ads: providers(mp.c, r.Ads),
				Rent: providers(mp.c, r.Rent), Buy: providers(mp.c, r.Buy),
			}
		}
	}
}

func studios(c *Client, list []tmdbCompany) *struct {
	Nodes []Studio `json:"nodes"`
} {
	out := &struct {
		Nodes []Studio `json:"nodes"`
	}{}
	for _, co := range list {
		out.Nodes = append(out.Nodes, Studio{ID: co.ID, Name: co.Name, Logo: c.Image(logoSize, co.LogoPath)})
	}
	return out
}

// addCrew adds a crew member with a job, once.
func addCrew(c *Client, list []CrewMember, cr tmdbCrew, job string) []CrewMember {
	for _, x := range list {
		if x.ID == cr.ID && x.Job == job {
			return list
		}
	}
	return append(list, CrewMember{ID: cr.ID, Name: cr.Name, Job: job, Image: c.Image(profileSize, cr.ProfilePath)})
}

func providers(c *Client, list []tmdbProvider) []WatchProvider {
	if len(list) == 0 {
		return nil
	}
	list = append([]tmdbProvider(nil), list...)
	sort.SliceStable(list, func(i, j int) bool { return list[i].Priority < list[j].Priority })
	out := make([]WatchProvider, 0, len(list))
	for _, p := range list {
		out = append(out, WatchProvider{ID: p.ID, Name: p.Name, Logo: c.Image(iconSize, p.LogoPath)})
	}
	return out
}

// trailer is the first YouTube trailer, an official one if there is one.
func trailer(videos []tmdbVideo) *Trailer {
	var best *tmdbVideo
	for i := range videos {
		v := &videos[i]
		if !strings.EqualFold(v.Site, "YouTube") || v.Type != "Trailer" || v.Key == "" {
			continue
		}
		if best == nil || v.Official && !best.Official {
			best = v
		}
	}
	if best == nil {
		return nil
	}
	return &Trailer{ID: best.Key, Site: "youtube", Thumbnail: "https://i.ytimg.com/vi/" + best.Key + "/hqdefault.jpg"}
}

// movieCertification is the movie's rating in the region (PG-13, 12...),
// else in the US.
func movieCertification(it *tmdbItem, region string) string {
	for _, want := range []string{region, "US"} {
		for _, r := range it.ReleaseDates.Results {
			if r.ISO != want {
				continue
			}
			dates := append([]tmdbReleaseDate(nil), r.ReleaseDates...)
			// The theatrical release's rating first, then the others.
			sort.SliceStable(dates, func(i, j int) bool { return certRank(dates[i].Type) < certRank(dates[j].Type) })
			for _, d := range dates {
				if c := strings.TrimSpace(d.Certification); c != "" {
					return c
				}
			}
		}
	}
	return ""
}

// certRank orders TMDB's release types: theatrical (3), limited (2),
// digital (4), physical (5), TV (6), premiere (1).
func certRank(t int) int {
	switch t {
	case 3:
		return 0
	case 2:
		return 1
	case 4:
		return 2
	case 5:
		return 3
	case 6:
		return 4
	}
	return 5
}

// showCertification is the show's rating in the region (TV-MA, 16...), else
// in the US.
func showCertification(it *tmdbItem, region string) string {
	for _, want := range []string{region, "US"} {
		for _, r := range it.ContentRatings.Results {
			if r.ISO == want && strings.TrimSpace(r.Rating) != "" {
				return strings.TrimSpace(r.Rating)
			}
		}
	}
	return ""
}

// synonyms are a title's other names written in Latin letters (what file
// and release names use), without the ones it already has.
func synonyms(m *Media, alt []tmdbAltTitle) []string {
	seen := map[string]bool{}
	for _, t := range []string{m.Title.English, m.Title.Native} {
		seen[strings.ToLower(strings.TrimSpace(t))] = true
	}
	out := []string{}
	for _, a := range alt {
		t := strings.TrimSpace(a.Title)
		if t == "" || !isLatin(t) || seen[strings.ToLower(t)] {
			continue
		}
		seen[strings.ToLower(t)] = true
		out = append(out, t)
		if len(out) == maxSynonyms {
			break
		}
	}
	return out
}

// isLatin reports whether s has letters, all of them Latin.
func isLatin(s string) bool {
	letters := 0
	for _, r := range s {
		if unicode.IsLetter(r) {
			if !unicode.Is(unicode.Latin, r) {
				return false
			}
			letters++
		}
	}
	return letters > 0
}

// parseDate reads a TMDB date (YYYY-MM-DD; partial dates and "" too).
func parseDate(s string) FuzzyDate {
	var d FuzzyDate
	parts := strings.Split(strings.TrimSpace(s), "-")
	vals := make([]*int, 0, 3)
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n <= 0 {
			break
		}
		vals = append(vals, &n)
	}
	if len(vals) > 0 {
		d.Year = vals[0]
	}
	if len(vals) > 1 {
		d.Month = vals[1]
	}
	if len(vals) > 2 {
		d.Day = vals[2]
	}
	return d
}

// formatDate writes a date the way TMDB and the database do: YYYY-MM-DD,
// YYYY-MM or YYYY when only those are known, "" when nothing is.
func formatDate(d FuzzyDate) string {
	if d.Year == nil || *d.Year <= 0 {
		return ""
	}
	s := strconv.Itoa(*d.Year)
	if d.Month == nil || *d.Month <= 0 {
		return s
	}
	s += "-" + twoDigits(*d.Month)
	if d.Day == nil || *d.Day <= 0 {
		return s
	}
	return s + "-" + twoDigits(*d.Day)
}

func twoDigits(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// episode maps an episode of a season's details.
func (mp *mapping) episode(e tmdbEpisode) *Episode {
	return &Episode{
		Key: EpisodeKey(e.SeasonNumber, e.EpisodeNumber), Season: e.SeasonNumber, Number: e.EpisodeNumber,
		Title: e.Name, Overview: e.Overview, Still: mp.c.Image(stillSize, e.StillPath), AirDate: e.AirDate,
		Runtime: e.Runtime, Rating: e.VoteAverage,
	}
}

// seasonDetail maps a season with its episodes.
func (mp *mapping) seasonDetail(mediaID int, s *tmdbSeasonDetail) *SeasonDetail {
	out := &SeasonDetail{
		MediaID: mediaID, Number: s.SeasonNumber, Name: s.Name, Overview: s.Overview,
		Poster: mp.c.Image(posterM, s.PosterPath), AirDate: s.AirDate, Episodes: []*Episode{},
	}
	for _, e := range s.Episodes {
		if e.EpisodeNumber <= 0 {
			continue
		}
		if e.SeasonNumber == 0 && s.SeasonNumber != 0 {
			e.SeasonNumber = s.SeasonNumber
		}
		out.Episodes = append(out.Episodes, mp.episode(e))
	}
	sort.SliceStable(out.Episodes, func(i, j int) bool { return out.Episodes[i].Key < out.Episodes[j].Key })
	return out
}
