package catalog

// TMDB's JSON, as far as KumoBox reads it. One type, tmdbItem, holds a
// movie or a show, in a list (search, discover, trending, recommendations)
// or with its whole page (details with appended credits, videos...): the
// fields of the other kind simply stay empty.

type tmdbGenre struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type tmdbCompany struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	LogoPath string `json:"logo_path"`
}

type tmdbCountry struct {
	ISO  string `json:"iso_3166_1"`
	Name string `json:"name"`
}

type tmdbCollectionRef struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	PosterPath   string `json:"poster_path"`
	BackdropPath string `json:"backdrop_path"`
}

type tmdbSeason struct {
	SeasonNumber int    `json:"season_number"`
	Name         string `json:"name"`
	Overview     string `json:"overview"`
	EpisodeCount int    `json:"episode_count"`
	AirDate      string `json:"air_date"`
	PosterPath   string `json:"poster_path"`
}

type tmdbEpisode struct {
	ID            int     `json:"id"`
	Name          string  `json:"name"`
	Overview      string  `json:"overview"`
	AirDate       string  `json:"air_date"`
	SeasonNumber  int     `json:"season_number"`
	EpisodeNumber int     `json:"episode_number"`
	Runtime       int     `json:"runtime"`
	StillPath     string  `json:"still_path"`
	VoteAverage   float64 `json:"vote_average"`
}

type tmdbSeasonDetail struct {
	ID           int           `json:"id"`
	SeasonNumber int           `json:"season_number"`
	Name         string        `json:"name"`
	Overview     string        `json:"overview"`
	AirDate      string        `json:"air_date"`
	PosterPath   string        `json:"poster_path"`
	Episodes     []tmdbEpisode `json:"episodes"`
}

type tmdbCast struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Character   string `json:"character"`
	ProfilePath string `json:"profile_path"`
	Order       int    `json:"order"`
}

type tmdbCrew struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Job         string `json:"job"`
	Department  string `json:"department"`
	ProfilePath string `json:"profile_path"`
}

type tmdbCredits struct {
	Cast []tmdbCast `json:"cast"`
	Crew []tmdbCrew `json:"crew"`
}

type tmdbVideo struct {
	Key       string `json:"key"`
	Name      string `json:"name"`
	Site      string `json:"site"`
	Type      string `json:"type"`
	Official  bool   `json:"official"`
	Language  string `json:"iso_639_1"`
	Published string `json:"published_at"`
}

type tmdbKeyword struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type tmdbReleaseDate struct {
	Certification string `json:"certification"`
	Type          int    `json:"type"`
	ReleaseDate   string `json:"release_date"`
}

type tmdbProvider struct {
	ID       int    `json:"provider_id"`
	Name     string `json:"provider_name"`
	LogoPath string `json:"logo_path"`
	Priority int    `json:"display_priority"`
}

type tmdbProviderRegion struct {
	Link     string         `json:"link"`
	Flatrate []tmdbProvider `json:"flatrate"`
	Free     []tmdbProvider `json:"free"`
	Ads      []tmdbProvider `json:"ads"`
	Rent     []tmdbProvider `json:"rent"`
	Buy      []tmdbProvider `json:"buy"`
}

type tmdbAltTitle struct {
	ISO   string `json:"iso_3166_1"`
	Title string `json:"title"`
	Type  string `json:"type"`
}

type tmdbExternalIDs struct {
	IMDbID string `json:"imdb_id"`
	TVDBID int    `json:"tvdb_id"`
}

type tmdbItem struct {
	ID int `json:"id"`
	// MediaType is set in mixed lists (search/multi, trending/all): movie,
	// tv or person.
	MediaType        string      `json:"media_type"`
	Title            string      `json:"title"`
	OriginalTitle    string      `json:"original_title"`
	Name             string      `json:"name"`
	OriginalName     string      `json:"original_name"`
	OriginalLanguage string      `json:"original_language"`
	Overview         string      `json:"overview"`
	Tagline          string      `json:"tagline"`
	PosterPath       string      `json:"poster_path"`
	BackdropPath     string      `json:"backdrop_path"`
	ReleaseDate      string      `json:"release_date"`
	FirstAirDate     string      `json:"first_air_date"`
	LastAirDate      string      `json:"last_air_date"`
	Adult            bool        `json:"adult"`
	VoteAverage      float64     `json:"vote_average"`
	VoteCount        int         `json:"vote_count"`
	Popularity       float64     `json:"popularity"`
	GenreIDs         []int       `json:"genre_ids"`
	Genres           []tmdbGenre `json:"genres"`
	OriginCountry    []string    `json:"origin_country"`
	Status           string      `json:"status"`

	// Movie details
	Runtime             int                `json:"runtime"`
	IMDbID              string             `json:"imdb_id"`
	ProductionCompanies []tmdbCompany      `json:"production_companies"`
	ProductionCountries []tmdbCountry      `json:"production_countries"`
	BelongsToCollection *tmdbCollectionRef `json:"belongs_to_collection"`

	// Show details
	Type             string        `json:"type"` // Scripted, Miniseries, Documentary...
	NumberOfEpisodes int           `json:"number_of_episodes"`
	NumberOfSeasons  int           `json:"number_of_seasons"`
	EpisodeRunTime   []int         `json:"episode_run_time"`
	Networks         []tmdbCompany `json:"networks"`
	CreatedBy        []tmdbCrew    `json:"created_by"`
	Seasons          []tmdbSeason  `json:"seasons"`
	LastEpisodeToAir *tmdbEpisode  `json:"last_episode_to_air"`
	NextEpisodeToAir *tmdbEpisode  `json:"next_episode_to_air"`

	// Appended to the details (append_to_response)
	Credits         *tmdbCredits                   `json:"credits"`
	Videos          *struct{ Results []tmdbVideo } `json:"videos"`
	Recommendations *tmdbPage                      `json:"recommendations"`
	Similar         *tmdbPage                      `json:"similar"`
	ExternalIDs     *tmdbExternalIDs               `json:"external_ids"`
	// Keywords: a movie's are in Keywords, a show's in Results.
	Keywords *struct {
		Keywords []tmdbKeyword `json:"keywords"`
		Results  []tmdbKeyword `json:"results"`
	} `json:"keywords"`
	ReleaseDates *struct {
		Results []struct {
			ISO          string            `json:"iso_3166_1"`
			ReleaseDates []tmdbReleaseDate `json:"release_dates"`
		} `json:"results"`
	} `json:"release_dates"`
	ContentRatings *struct {
		Results []struct {
			ISO    string `json:"iso_3166_1"`
			Rating string `json:"rating"`
		} `json:"results"`
	} `json:"content_ratings"`
	WatchProviders *struct {
		Results map[string]tmdbProviderRegion `json:"results"`
	} `json:"watch/providers"`
	// AlternativeTitles: a movie's are in Titles, a show's in Results.
	AlternativeTitles *struct {
		Titles  []tmdbAltTitle `json:"titles"`
		Results []tmdbAltTitle `json:"results"`
	} `json:"alternative_titles"`
}

type tmdbPage struct {
	Page         int        `json:"page"`
	TotalPages   int        `json:"total_pages"`
	TotalResults int        `json:"total_results"`
	Results      []tmdbItem `json:"results"`
}

type tmdbCollection struct {
	ID           int        `json:"id"`
	Name         string     `json:"name"`
	PosterPath   string     `json:"poster_path"`
	BackdropPath string     `json:"backdrop_path"`
	Parts        []tmdbItem `json:"parts"`
}

type tmdbFind struct {
	MovieResults []tmdbItem `json:"movie_results"`
	TVResults    []tmdbItem `json:"tv_results"`
}
