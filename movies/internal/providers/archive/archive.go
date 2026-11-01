// Package archive is the built-in Internet Archive provider: it streams the
// public-domain and freely licensed films archive.org hosts (feature films,
// silent films, classic cartoons, film noir...). It finds the film by
// title and year with the advanced search API, then reads the item's
// files from the metadata API and offers its video files.
package archive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/providers"
	"github.com/v0-0x/mediaserver/movies/internal/util"
)

const (
	ID   = "archive"
	Name = "Internet Archive"

	// DefaultBaseURL is archive.org.
	DefaultBaseURL = "https://archive.org"

	maxResponse = 16 << 20
)

// preferredCollections hold the curated films: items found in them rank
// higher than random uploads of the same title.
var preferredCollections = []string{
	"feature_films", "moviesandfilms", "silent_films", "classic_tv", "film_noir",
	"sci-fi_horror", "comedy_films", "classic_cartoons", "cinemocracy", "SciFi_Horror",
}

// Provider is the Internet Archive provider. The zero value is not usable:
// call New.
type Provider struct {
	// BaseURL is archive.org's address (tests point it at a fake server).
	BaseURL string
	// HTTP is the client used for the API (util.HTTP, which can't reach the
	// LAN, by default).
	HTTP *http.Client
	// MaxItems is how many matching items are turned into streams.
	MaxItems int
}

// New returns the provider for archive.org.
func New() *Provider {
	return &Provider{BaseURL: DefaultBaseURL, HTTP: util.HTTP, MaxItems: 3}
}

func (p *Provider) ID() string   { return ID }
func (p *Provider) Name() string { return Name }
func (p *Provider) Kind() string { return providers.KindBuiltin }
func (p *Provider) Icon() string { return "https://archive.org/images/glogo.jpg" }

// Supports reports movies only: the archive's TV holdings are too patchy
// to be matched by season and episode.
func (p *Provider) Supports(mediaType string) bool { return mediaType == providers.TypeMovie }

// Item is a search result.
type Item struct {
	Identifier  string   `json:"identifier"`
	Title       string   `json:"title"`
	Year        int      `json:"year"`
	Downloads   int      `json:"downloads"`
	Collections []string `json:"collections"`
	// Score is how well the item matches the request (higher is better).
	Score float64 `json:"score"`
}

// Streams finds the movie on archive.org and returns the video files of the
// best matching items.
func (p *Provider) Streams(ctx context.Context, r providers.Request) ([]providers.Stream, error) {
	if r.Type != providers.TypeMovie {
		return nil, nil
	}
	items, err := p.Search(ctx, r.Title, r.Year)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 && r.OriginalTitle != "" && !strings.EqualFold(r.OriginalTitle, r.Title) {
		if items, err = p.Search(ctx, r.OriginalTitle, r.Year); err != nil {
			return nil, err
		}
	}
	n := p.MaxItems
	if n <= 0 {
		n = 3
	}
	if len(items) > n {
		items = items[:n]
	}
	// Fetch the items' files at once, keeping the ranking order.
	results := make([][]providers.Stream, len(items))
	errs := make([]error, len(items))
	var wg sync.WaitGroup
	for i, it := range items {
		wg.Add(1)
		go func(i int, it Item) {
			defer wg.Done()
			results[i], errs[i] = p.ItemStreams(ctx, it.Identifier)
		}(i, it)
	}
	wg.Wait()
	var out []providers.Stream
	for _, s := range results {
		out = append(out, s...)
	}
	if len(out) == 0 {
		for _, err := range errs {
			if err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// Search looks a film up by title (and year, when known) and returns the
// matching items, best first. Trailers, reviews and other titles are left
// out.
func (p *Provider) Search(ctx context.Context, title string, year int) ([]Item, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, errors.New("no title to search for")
	}
	q := fmt.Sprintf(`title:(%s) AND mediatype:(movies)`, quoteQuery(title))
	v := url.Values{}
	v.Set("q", q)
	for _, f := range []string{"identifier", "title", "year", "date", "downloads", "collection"} {
		v.Add("fl[]", f)
	}
	v.Set("rows", "20")
	v.Set("output", "json")
	var res struct {
		Response struct {
			Docs []searchDoc `json:"docs"`
		} `json:"response"`
	}
	if err := p.getJSON(ctx, p.base()+"/advancedsearch.php?"+v.Encode(), &res); err != nil {
		return nil, fmt.Errorf("archive.org search: %w", err)
	}
	var items []Item
	for _, d := range res.Response.Docs {
		it := d.item()
		if it.Identifier == "" {
			continue
		}
		score, ok := matchScore(title, year, it)
		if !ok {
			continue
		}
		it.Score = score
		items = append(items, it)
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Score > items[j].Score })
	return items, nil
}

// quoteQuery makes title one phrase of the search syntax.
func quoteQuery(title string) string {
	title = strings.NewReplacer(`\`, " ", `"`, " ").Replace(title)
	return `"` + strings.Join(strings.Fields(title), " ") + `"`
}

// matchScore rates an item for a title and year: title similarity first,
// then the year (±1, as release and upload years differ), the curated
// collections and popularity.
func matchScore(title string, year int, it Item) (float64, bool) {
	sim := util.Similarity(title, it.Title)
	if sim < 0.7 {
		return 0, false
	}
	lt := strings.ToLower(it.Title)
	for _, junk := range []string{"trailer", "teaser", "review", "commentary", "behind the scenes", "featurette"} {
		if strings.Contains(lt, junk) && !strings.Contains(strings.ToLower(title), junk) {
			return 0, false
		}
	}
	score := sim * 100
	if year > 0 && it.Year > 0 {
		d := year - it.Year
		if d < 0 {
			d = -d
		}
		switch {
		case d <= 1:
			score += 25
		case d > 3:
			return 0, false // another film with the same title
		default:
			score -= 10
		}
	}
	for _, c := range it.Collections {
		if containsFold(preferredCollections, c) {
			score += 10
			break
		}
	}
	score += math.Log10(float64(it.Downloads)+1) * 2
	return score, true
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

// searchDoc is a search result as the API returns it: fields are a string
// or an array of strings, the year a string or a number, depending on the
// item.
type searchDoc struct {
	Identifier json.RawMessage `json:"identifier"`
	Title      json.RawMessage `json:"title"`
	Year       json.RawMessage `json:"year"`
	Date       json.RawMessage `json:"date"`
	Downloads  json.RawMessage `json:"downloads"`
	Collection json.RawMessage `json:"collection"`
}

func (d searchDoc) item() Item {
	it := Item{
		Identifier:  firstString(d.Identifier),
		Title:       firstString(d.Title),
		Downloads:   flexInt(d.Downloads),
		Collections: stringList(d.Collection),
	}
	it.Year = flexInt(d.Year)
	if it.Year == 0 {
		if s := firstString(d.Date); len(s) >= 4 {
			it.Year, _ = strconv.Atoi(s[:4])
		}
	}
	return it
}

// stringList reads a string or an array of strings.
func stringList(raw json.RawMessage) []string {
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		return list
	}
	var s string
	if json.Unmarshal(raw, &s) == nil && s != "" {
		return []string{s}
	}
	return nil
}

func firstString(raw json.RawMessage) string {
	if l := stringList(raw); len(l) > 0 {
		return strings.TrimSpace(l[0])
	}
	return ""
}

// flexInt reads a number given as a number or a string (or the first of an
// array of them).
func flexInt(raw json.RawMessage) int {
	var f float64
	if json.Unmarshal(raw, &f) == nil {
		return int(f)
	}
	if s := firstString(raw); s != "" {
		if n, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
			return int(n)
		}
		if len(s) >= 4 {
			n, _ := strconv.Atoi(s[:4]) // "1968-10-01"
			return n
		}
	}
	return 0
}

func (p *Provider) base() string {
	if p.BaseURL == "" {
		return DefaultBaseURL
	}
	return strings.TrimRight(p.BaseURL, "/")
}

func (p *Provider) client() *http.Client {
	if p.HTTP != nil {
		return p.HTTP
	}
	return util.HTTP
}

func (p *Provider) getJSON(ctx context.Context, u string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", util.UserAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := p.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return err
	}
	if len(body) > maxResponse {
		return errors.New("response too large")
	}
	return json.Unmarshal(body, out)
}
