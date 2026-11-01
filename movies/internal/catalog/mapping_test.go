package catalog

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/config"
)

func img(size, path string) string { return testImages + "/" + size + path }

func TestMovieDetails(t *testing.T) {
	f := newFakeTMDB(t, nil)
	p := newTestPlatform(t, f)
	m, err := p.Media(context.Background(), 155, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		got  any
		want any
	}{
		{"id", m.ID, 155},
		{"tmdb id", m.TMDBID, 155},
		{"imdb", m.IMDbID, "tt0468569"},
		{"type", m.Type, TypeMovie},
		{"format", m.Format, TypeMovie},
		{"status", m.Status, "FINISHED"},
		{"title", m.Title, Title{Romaji: "The Dark Knight", English: "The Dark Knight", Native: "The Dark Knight", UserPreferred: "The Dark Knight"}},
		{"synonyms", m.Synonyms, []string{"Batman: The Dark Knight", "The Dark Knight : Le Chevalier noir"}},
		{"cover", m.CoverImage, CoverImage{
			ExtraLarge: img("w780", "/qJ2tW6WMUDux911r6m7haRef0WH.jpg"),
			Large:      img("w500", "/qJ2tW6WMUDux911r6m7haRef0WH.jpg"),
			Medium:     img("w342", "/qJ2tW6WMUDux911r6m7haRef0WH.jpg"),
		}},
		{"banner", m.BannerImage, img("w1280", "/dqK9Hag1054tghRQSqLSfrkvQnA.jpg")},
		{"genres", m.Genres, []string{"Drama", "Action", "Crime", "Thriller"}},
		{"episodes", *m.Episodes, 1},
		{"duration", *m.Duration, 152},
		{"score", *m.AverageScore, 85},
		{"popularity", m.Popularity, 123},
		{"votes", m.VoteCount, 33124},
		{"start", formatDate(m.StartDate), "2008-07-16"},
		{"end", formatDate(m.EndDate), ""},
		{"year", m.Year(), 2008},
		{"tagline", m.Tagline, "Welcome to a world without rules."},
		{"language", m.OriginalLanguage, "en"},
		{"country", m.CountryOfOrigin, "US"},
		{"certification", m.Certification, "PG-13"},
		{"site", m.SiteURL, "https://www.themoviedb.org/movie/155"},
		{"collection", *m.BelongsTo, CollectionRef{ID: 263, Name: "The Dark Knight Collection",
			Poster: img("w342", "/fMhw9ZUTh4w2dvMExPpn10Yk3tY.jpg"), Backdrop: img("w1280", "/xfKot7lqaiW4XpL5TtDlVBA9ei9.jpg")}},
		{"trailer", *m.Trailer, Trailer{ID: "EXeTwQWrcwY", Site: "youtube", Thumbnail: "https://i.ytimg.com/vi/EXeTwQWrcwY/hqdefault.jpg"}},
		{"tags", m.Tags, []Tag{{Name: "dc comics"}, {Name: "crime fighter"}, {Name: "superhero"}}},
		{"crew", m.Crew, []CrewMember{
			{ID: 525, Name: "Christopher Nolan", Job: "Director", Image: img("w185", "/xuAIuYSmsUzKlUMBFGVZaWsY3DZ.jpg")},
			{ID: 527, Name: "Jonathan Nolan", Job: "Writer", Image: img("w185", "/2RKhqp0g3G6ZTmmPkb7Zc8HXg9d.jpg")},
			{ID: 525, Name: "Christopher Nolan", Job: "Writer", Image: img("w185", "/xuAIuYSmsUzKlUMBFGVZaWsY3DZ.jpg")},
		}},
	} {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s: got %+v, want %+v", c.name, c.got, c.want)
		}
	}

	if len(m.Studios.Nodes) != 4 || m.Studios.Nodes[0] != (Studio{ID: 9993, Name: "DC Entertainment", Logo: img("w185", "/2Tc1P3Ac8M479naPp1kYT3izLS5.png")}) {
		t.Errorf("studios: %+v", m.Studios.Nodes)
	}

	// The cast in billing order: the first six are the main cast.
	var cast []string
	for i, e := range m.Characters.Edges {
		want := "MAIN"
		if i >= mainCast {
			want = "SUPPORTING"
		}
		if e.Role != want || len(e.VoiceActors) != 1 {
			t.Errorf("cast member %d: %+v", i, e)
		}
		cast = append(cast, e.Node.Name.Full+"="+e.VoiceActors[0].Name.Full)
	}
	if want := "Bruce Wayne=Christian Bale,Joker=Heath Ledger,Harvey Dent=Aaron Eckhart,James Gordon=Gary Oldman,Rachel Dawes=Maggie Gyllenhaal,Alfred=Michael Caine,Lucius Fox=Morgan Freeman"; strings.Join(cast, ",") != want {
		t.Errorf("cast: %s", strings.Join(cast, ","))
	}
	if e := m.Characters.Edges[0]; e.Node.Image.Large != img("w185", "/7Pxez9J8fuPd2Mn9kex13YALrCQ.jpg") || e.VoiceActors[0].ID != 3894 {
		t.Errorf("first cast member: %+v", e)
	}

	// Recommendations, without the movie itself.
	var recs []int
	for _, n := range m.Recommendations.Nodes {
		recs = append(recs, n.MediaRecommendation.ID)
	}
	if !reflect.DeepEqual(recs, []int{272, 27205}) {
		t.Errorf("recommendations: %v", recs)
	}
	if r := m.Recommendations.Nodes[0].MediaRecommendation; r.Type != TypeMovie || r.Status != "FINISHED" || !reflect.DeepEqual(r.Genres, []string{"Action", "Crime", "Drama"}) {
		t.Errorf("recommendation: %+v", r)
	}

	// Where to watch, in the region, by TMDB's display order.
	wp := m.WatchProviders
	if wp == nil || wp.Region != "US" || len(wp.Flatrate) != 1 || wp.Flatrate[0].Name != "Max" || wp.Flatrate[0].Logo != img("w92", "/jbe4gVSfRlbPTdESXhEKpornsfu.jpg") {
		t.Fatalf("watch providers: %+v", wp)
	}
	if len(wp.Rent) != 2 || wp.Rent[0].Name != "Amazon Video" || wp.Rent[1].Name != "Apple TV" || !strings.Contains(wp.Link, "locale=US") {
		t.Errorf("rent: %+v", wp.Rent)
	}

	// The other movies of its collection, by release date.
	rel := map[int]string{}
	for _, e := range m.Relations.Edges {
		rel[e.Node.ID] = e.RelationType
	}
	if want := map[int]string{272: "PREQUEL", 49026: "SEQUEL", 1259102: "OTHER"}; !reflect.DeepEqual(rel, want) {
		t.Errorf("relations: %v", rel)
	}

	// Saved: the page opens again without asking TMDB.
	before := f.total()
	if !p.HasDetails(155) {
		t.Error("details not saved")
	}
	if _, err := p.Media(context.Background(), 155, false); err != nil || f.total() != before {
		t.Errorf("cached page: %v, %d requests", err, f.total()-before)
	}
	lite, err := p.MediaLite(context.Background(), 155)
	if err != nil || lite.Characters != nil || lite.Description != "" || lite.Title.English != "The Dark Knight" || f.total() != before {
		t.Errorf("lite: %+v %v", lite, err)
	}
}

// The certification and where to watch follow the region of the settings;
// the requests carry the language.
func TestRegionAndLanguage(t *testing.T) {
	f := newFakeTMDB(t, nil)
	p, _ := newTestPlatformSettings(t, f, func(s *config.Settings) {
		s.TMDB.APIKey, s.TMDB.Region, s.TMDB.Language = testKey, "FR", "fr-FR"
	})
	m, err := p.Media(context.Background(), 155, false)
	if err != nil {
		t.Fatal(err)
	}
	if m.Certification != "12" || m.WatchProviders == nil || m.WatchProviders.Flatrate[0].Name != "Canal+" {
		t.Errorf("FR: certification %q, providers %+v", m.Certification, m.WatchProviders)
	}
	q := f.last("/movie/155").URL.Query()
	if q.Get("language") != "fr-FR" || q.Get("include_video_language") != "fr,en,null" ||
		!strings.Contains(q.Get("append_to_response"), "release_dates") || strings.Contains(q.Get("append_to_response"), "content_ratings") {
		t.Errorf("request: %v", q)
	}
}

func TestShowDetails(t *testing.T) {
	f := newFakeTMDB(t, nil)
	p := newTestPlatform(t, f)
	m, err := p.Media(context.Background(), ShowID(1396), false)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		got  any
		want any
	}{
		{"id", m.ID, ShowBase + 1396},
		{"tmdb id", m.TMDBID, 1396},
		{"type", m.Type, TypeTV},
		{"format", m.Format, TypeTV},
		{"status", m.Status, "FINISHED"},
		{"imdb", m.IMDbID, "tt0903747"},
		{"tvdb", m.TVDBID, 81189},
		{"episodes", *m.Episodes, 62},
		{"duration", *m.Duration, 45},
		{"start", formatDate(m.StartDate), "2008-01-20"},
		{"end", formatDate(m.EndDate), "2013-09-29"},
		{"latest", m.LatestEpisode, EpisodeKey(5, 16)},
		{"next", m.NextAiringEpisode, (*AiringEpisode)(nil)},
		{"certification", m.Certification, "TV-MA"},
		{"site", m.SiteURL, "https://www.themoviedb.org/tv/1396"},
		{"synonyms", m.Synonyms, []string{"BB", "Breaking Bad: Ruptura total"}},
		{"studios", m.Studios.Nodes, []Studio{{ID: 174, Name: "AMC", Logo: img("w185", "/alqLicR1ZMHMaZGP3xRQxn9sq7p.png")}}},
		{"tags", len(m.Tags), 3},
		{"crew", m.Crew, []CrewMember{{ID: 66633, Name: "Vince Gilligan", Job: "Creator", Image: img("w185", "/z3E0DhBg1V1PZVEtS9vfFPzOWYB.jpg")}}},
		{"first cast", m.Characters.Edges[0].Node.Name.Full + "=" + m.Characters.Edges[0].VoiceActors[0].Name.Full, "Walter White=Bryan Cranston"},
		{"providers", m.WatchProviders.Flatrate[0].Name, "Netflix"},
		{"trailer", m.Trailer.ID, "HhesaQXLuRY"},
		{"total", m.TotalEpisodes(), 62},
		{"first episode", m.FirstEpisode(), 1001},
		{"last episode", m.LastEpisode(), 5016},
		{"aired", m.AiredEpisodes(), 62},
		{"specials", m.SeasonEpisodeCount(0), 9},
		{"after S01E07", m.NextEpisode(1007), 2001},
	} {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s: got %+v, want %+v", c.name, c.got, c.want)
		}
	}
	if len(m.Seasons) != 6 || m.Seasons[0] != (Season{Number: 0, Name: "Specials", EpisodeCount: 9, AirDate: "2009-02-17", Poster: img("w342", "/40dT79mDEZwXkQiZNBgSaydQFDP.jpg")}) {
		t.Errorf("seasons: %+v", m.Seasons)
	}
	r := m.Recommendations.Nodes[0].MediaRecommendation
	if r.ID != ShowID(60059) || r.Type != TypeTV || r.Title.English != "Better Call Saul" || r.Status != "" || !reflect.DeepEqual(r.Genres, []string{"Crime", "Drama"}) {
		t.Errorf("recommendation: %+v", r)
	}
	// The lite copy keeps the seasons: the episode helpers need them.
	if lite := m.Lite(); lite.LastEpisode() != 5016 || lite.Characters != nil {
		t.Errorf("lite: %+v", lite)
	}
}

// A show still on the air: its next episode, no end date, and similar
// shows when there are no recommendations.
func TestReturningShow(t *testing.T) {
	p := newTestPlatform(t, newFakeTMDB(t, nil))
	m, err := p.Media(context.Background(), ShowID(94997), false)
	if err != nil {
		t.Fatal(err)
	}
	airs := time.Date(2026, time.June, 21, 0, 0, 0, 0, time.UTC).Unix()
	next := m.NextAiringEpisode
	if m.Status != "RELEASING" || m.EndDate.Year != nil || next == nil || next.Episode != 3001 || next.AiringAt != int(airs) || next.Title != "A Son for a Son" {
		t.Fatalf("status %s, end %v, next %+v", m.Status, m.EndDate, next)
	}
	if d := next.TimeUntilAiring - int(airs-time.Now().Unix()); d < -5 || d > 5 {
		t.Errorf("time until airing %d", next.TimeUntilAiring)
	}
	if m.LatestEpisode != 2008 || *m.Duration != 68 || m.LatestAired() != 2008 || m.AiredEpisodes() != 18 || m.LastEpisode() != 3008 {
		t.Errorf("latest %d, duration %d, aired %d", m.LatestEpisode, *m.Duration, m.AiredEpisodes())
	}
	if len(m.Crew) != 2 || m.Crew[1].Name != "George R. R. Martin" || m.Crew[1].Job != "Creator" {
		t.Errorf("crew: %+v", m.Crew)
	}
	if m.Recommendations == nil || m.Recommendations.Nodes[0].MediaRecommendation.ID != ShowID(1399) {
		t.Errorf("similar shows: %+v", m.Recommendations)
	}
	if m.Trailer != nil || m.WatchProviders != nil || m.Characters != nil {
		t.Errorf("empty parts: %+v %+v %+v", m.Trailer, m.WatchProviders, m.Characters)
	}
}

// Titles in lists (search, discover...) carry less: genre ids, no status.
func TestListMapping(t *testing.T) {
	now := time.Date(2026, time.October, 9, 12, 0, 0, 0, time.UTC)
	mp := &mapping{c: NewClient(nil), region: "US", now: now, genres: map[string]map[int]string{
		TypeMovie: {16: "Animation", 14: "Fantasy"},
		TypeTV:    {18: "Drama"},
	}}
	var items []tmdbItem
	if err := json.Unmarshal([]byte(`[
		{"id": 129, "media_type": "movie", "title": "Spirited Away", "original_title": "千と千尋の神隠し", "original_language": "ja", "release_date": "2001-07-20", "genre_ids": [16, 10751, 14], "vote_average": 8.5, "vote_count": 17000, "popularity": 90.6},
		{"id": 194, "media_type": "movie", "title": "Amélie", "original_title": "Le Fabuleux Destin d'Amélie Poulain", "original_language": "fr", "release_date": "2001-04-25", "genre_ids": [35, 10749], "vote_average": 7.9, "vote_count": 11000, "popularity": 30.2},
		{"id": 1311031, "media_type": "movie", "title": "Upcoming", "original_title": "Upcoming", "release_date": "2027-03-01", "vote_count": 0},
		{"id": 1399, "media_type": "tv", "name": "Game of Thrones", "original_name": "Game of Thrones", "first_air_date": "2011-04-17", "genre_ids": [18]},
		{"id": 250307, "media_type": "tv", "name": "Pending Show", "original_name": "Pending Show", "first_air_date": ""},
		{"id": 87108, "name": "Chernobyl", "original_name": "Chernobyl", "type": "Miniseries", "status": "Ended", "first_air_date": "2019-05-06", "last_air_date": "2019-06-03", "number_of_episodes": 5}
	]`), &items); err != nil {
		t.Fatal(err)
	}
	var got []*Media
	for i := range items {
		got = append(got, mp.media(&items[i], TypeTV))
	}
	sa, amelie, upcoming, got1, pending, chernobyl := got[0], got[1], got[2], got[3], got[4], got[5]
	if sa.ID != 129 || sa.Type != TypeMovie || sa.Title.Romaji != "" || sa.Title.Native != "千と千尋の神隠し" || sa.PreferredTitle() != "Spirited Away" ||
		!reflect.DeepEqual(sa.Genres, []string{"Animation", "Fantasy"}) || sa.Status != "FINISHED" || *sa.AverageScore != 85 {
		t.Errorf("Spirited Away: %+v", sa)
	}
	if amelie.Title.Romaji != "Le Fabuleux Destin d'Amélie Poulain" || amelie.Title.English != "Amélie" || len(amelie.Genres) != 0 {
		t.Errorf("Amélie: %+v", amelie.Title)
	}
	if upcoming.Status != "NOT_YET_RELEASED" || upcoming.AverageScore != nil {
		t.Errorf("upcoming: %s %v", upcoming.Status, upcoming.AverageScore)
	}
	if got1.ID != ShowID(1399) || got1.Type != TypeTV || got1.Status != "" || got1.Episodes != nil || !reflect.DeepEqual(got1.Genres, []string{"Drama"}) {
		t.Errorf("Game of Thrones: %+v", got1)
	}
	if pending.Status != "NOT_YET_RELEASED" {
		t.Errorf("pending: %s", pending.Status)
	}
	if chernobyl.Format != "MINISERIES" || chernobyl.Type != TypeTV || chernobyl.Status != "FINISHED" || formatDate(chernobyl.EndDate) != "2019-06-03" || *chernobyl.Episodes != 5 {
		t.Errorf("Chernobyl: %+v", chernobyl)
	}
}

func TestSeason(t *testing.T) {
	f := newFakeTMDB(t, nil)
	p := newTestPlatform(t, f)
	ctx := context.Background()
	s, err := p.Season(ctx, ShowID(1396), 1)
	if err != nil {
		t.Fatal(err)
	}
	if s.MediaID != ShowID(1396) || s.Number != 1 || s.Name != "Season 1" || len(s.Episodes) != 7 || s.Poster != img("w342", "/1BP4xYv9ZG4ZVHkL7ocOziBbSYH.jpg") {
		t.Fatalf("season: %+v", s)
	}
	want := Episode{Key: 1001, Season: 1, Number: 1, Title: "Pilot", Overview: s.Episodes[0].Overview, Still: img("w300", "/ydlY3iPfeOAvu8gVqrxPoMvzNCn.jpg"),
		AirDate: "2008-01-20", Runtime: 59, Rating: 8.1}
	if *s.Episodes[0] != want || s.Episodes[6].Key != 1007 || s.Episodes[6].Title != "A No-Rough-Stuff-Type Deal" {
		t.Errorf("episodes: %+v ... %+v", s.Episodes[0], s.Episodes[6])
	}
	if _, err := p.Season(ctx, ShowID(1396), 1); err != nil || f.count("/tv/1396/season/1") != 1 {
		t.Errorf("not cached: %v, %d requests", err, f.count("/tv/1396/season/1"))
	}
	if _, err := p.Season(ctx, 155, 1); err == nil {
		t.Error("a movie has no seasons")
	}
}

func TestDates(t *testing.T) {
	for in, want := range map[string]string{"2008-07-16": "2008-07-16", "2008-07": "2008-07", "2008": "2008", "": "", "soon": ""} {
		if got := formatDate(parseDate(in)); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
	if d := parseDate("2008-07-16"); *d.Year != 2008 || *d.Month != 7 || *d.Day != 16 {
		t.Errorf("parsed %+v", d)
	}
}
