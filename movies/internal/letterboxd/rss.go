package letterboxd

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// The diary feed
//
// Every member has a public RSS feed (letterboxd.com/<username>/rss/) with
// their latest 50 diary entries and lists. A diary entry looks like:
//
//	<item>
//	  <title>The Matrix, 1999 - ★★★★½</title>
//	  <link>https://letterboxd.com/dave/film/the-matrix/</link>
//	  <guid isPermaLink="false">letterboxd-review-123</guid>
//	  <pubDate>Mon, 01 Jan 2024 10:00:00 +1300</pubDate>
//	  <letterboxd:watchedDate>2024-01-01</letterboxd:watchedDate>
//	  <letterboxd:rewatch>No</letterboxd:rewatch>
//	  <letterboxd:filmTitle>The Matrix</letterboxd:filmTitle>
//	  <letterboxd:filmYear>1999</letterboxd:filmYear>
//	  <letterboxd:memberRating>4.5</letterboxd:memberRating>
//	  <letterboxd:memberLike>Yes</letterboxd:memberLike>
//	  <tmdb:movieId>603</tmdb:movieId>
//	  <description><![CDATA[<p><img src="poster.jpg"/></p> <p>The review.</p>]]></description>
//	  <dc:creator>Dave</dc:creator>
//	</item>
//
// The guid is letterboxd-review-<n> for entries with a review and
// letterboxd-watch-<n> for the others (whose description is just "Watched on
// Monday January 1, 2024."); lists are letterboxd-list-<n> and have no film.
// A rewatch links to .../film/<slug>/<n>/, the n-th viewing. Elements are
// matched by local name and a namespace that mentions letterboxd, themoviedb
// or purl.org/dc, so small changes of the namespace URIs don't break it.

// Feed is a member's diary feed.
type Feed struct {
	Username    string      `json:"username"`
	DisplayName string      `json:"displayName"`
	Items       []DiaryItem `json:"items"`
}

// DiaryItem is one diary entry (or review) of the feed.
type DiaryItem struct {
	GUID        string `json:"guid"`
	Link        string `json:"link"`
	FilmTitle   string `json:"filmTitle"`
	FilmYear    int    `json:"filmYear,omitempty"`
	TMDBMovieID int    `json:"tmdbMovieId,omitempty"`
	TMDBTVID    int    `json:"tmdbTvId,omitempty"`
	// WatchedDate is the diary date (YYYY-MM-DD); empty for a review that
	// isn't in the diary.
	WatchedDate string `json:"watchedDate,omitempty"`
	Rewatch     bool   `json:"rewatch"`
	// Rating is 0.5-5 stars, 0 when not rated.
	Rating float64 `json:"rating,omitempty"`
	Liked  bool    `json:"liked"`
	// Review is the review as plain text (paragraphs separated by blank
	// lines), empty when there is none.
	Review   string `json:"review,omitempty"`
	Spoilers bool   `json:"spoilers,omitempty"`
	Poster   string `json:"poster,omitempty"`
	// Creator is the member's display name (dc:creator).
	Creator string `json:"creator,omitempty"`
	// Title is the item's own title: "The Matrix, 1999 - ★★★★½".
	Title string `json:"title,omitempty"`
	// Published is when the entry was logged (pubDate).
	Published time.Time `json:"published"`
}

// FeedURL is the address of username's feed on base (DefaultBaseURL when
// empty).
func FeedURL(base, username string) string { return ProfileURL(base, username) + "rss/" }

// maxFeedSize bounds the feed read (the real one is well under 1 MB).
const maxFeedSize = 16 << 20

// FetchFeed downloads and parses username's diary feed from base
// (DefaultBaseURL when empty). It returns ErrNoSuchMember when Letterboxd
// has no such member.
func FetchFeed(ctx context.Context, hc *http.Client, base, username string) (*Feed, error) {
	username, err := ParseUsername(username)
	if err != nil {
		return nil, err
	}
	if hc == nil {
		hc = defaultHTTP
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, FeedURL(base, username), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent())
	req.Header.Set("Accept", "application/rss+xml, application/xml;q=0.9, */*;q=0.5")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("letterboxd: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, ErrNoSuchMember
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
		return nil, fmt.Errorf("letterboxd refused the request (HTTP %d), try again later", resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("letterboxd: HTTP %d", resp.StatusCode)
	}
	feed, err := ParseFeed(io.LimitReader(resp.Body, maxFeedSize))
	if err != nil {
		return nil, err
	}
	if feed.Username == "" {
		feed.Username = username
	}
	if feed.DisplayName == "" {
		feed.DisplayName = feed.Username
	}
	return feed, nil
}

// rawElem is any element of an item or of the channel.
type rawElem struct {
	XMLName xml.Name
	Text    string `xml:",chardata"`
	Inner   string `xml:",innerxml"`
}

func (e rawElem) value() string {
	if t := strings.TrimSpace(e.Text); t != "" {
		return t
	}
	return strings.TrimSpace(e.Inner)
}

type rawChannel struct {
	Elems []rawElem `xml:",any"`
	Items []rawItem `xml:"item"`
}

type rawItem struct {
	Elems []rawElem `xml:",any"`
}

type rawRSS struct {
	XMLName xml.Name   `xml:"rss"`
	Channel rawChannel `xml:"channel"`
}

// namespace kinds
const (
	nsNone = iota
	nsLetterboxd
	nsTMDB
	nsDC
	nsOther
)

func nsKind(space string) int {
	s := strings.ToLower(space)
	switch {
	case s == "":
		return nsNone
	case strings.Contains(s, "letterboxd"):
		return nsLetterboxd
	case strings.Contains(s, "themoviedb") || s == "tmdb":
		return nsTMDB
	case strings.Contains(s, "purl.org/dc") || s == "dc":
		return nsDC
	}
	return nsOther
}

// ParseFeed parses a diary feed. Items that aren't about a film (lists) are
// left out.
func ParseFeed(r io.Reader) (*Feed, error) {
	dec := xml.NewDecoder(r)
	dec.Entity = xml.HTMLEntity
	var raw rawRSS
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("letterboxd: not a diary feed: %w", err)
	}
	feed := &Feed{}
	for _, e := range raw.Channel.Elems {
		if nsKind(e.XMLName.Space) != nsNone {
			continue
		}
		switch e.XMLName.Local {
		case "title":
			// "Letterboxd - Dave Vis"
			t := e.value()
			if i := strings.Index(t, " - "); i >= 0 && strings.EqualFold(strings.TrimSpace(t[:i]), "Letterboxd") {
				feed.DisplayName = strings.TrimSpace(t[i+3:])
			}
		case "link":
			if u := usernameFromLink(e.value()); u != "" {
				feed.Username = u
			}
		}
	}
	creator := ""
	for _, ri := range raw.Channel.Items {
		it, ok := parseItem(ri)
		if it.Creator != "" && creator == "" {
			creator = it.Creator
		}
		if ok {
			feed.Items = append(feed.Items, it)
		}
	}
	// dc:creator holds the display name exactly; the channel title may have
	// been shortened.
	if creator != "" {
		feed.DisplayName = creator
	}
	return feed, nil
}

var linkUserRe = regexp.MustCompile(`^https?://[^/]+/([A-Za-z0-9_]+)/?`)

// usernameFromLink returns the member of a profile link.
func usernameFromLink(link string) string {
	if m := linkUserRe.FindStringSubmatch(strings.TrimSpace(link)); m != nil {
		switch strings.ToLower(m[1]) {
		case "film", "films", "list", "lists", "rss":
			return ""
		}
		return m[1]
	}
	return ""
}

func parseItem(ri rawItem) (DiaryItem, bool) {
	var it DiaryItem
	var description string
	for _, e := range ri.Elems {
		v := e.value()
		switch nsKind(e.XMLName.Space) {
		case nsNone:
			switch e.XMLName.Local {
			case "title":
				it.Title = v
			case "link":
				it.Link = v
			case "guid":
				it.GUID = v
			case "pubDate":
				it.Published = parseRSSDate(v)
			case "description":
				description = e.Text
				if strings.TrimSpace(description) == "" {
					description = e.Inner
				}
			}
		case nsLetterboxd:
			switch e.XMLName.Local {
			case "watchedDate":
				it.WatchedDate = normDate(v)
			case "rewatch":
				it.Rewatch = yes(v)
			case "filmTitle":
				it.FilmTitle = v
			case "filmYear":
				it.FilmYear, _ = strconv.Atoi(v)
			case "memberRating":
				it.Rating = parseStars(v)
			case "memberLike":
				it.Liked = yes(v)
			}
		case nsTMDB:
			switch e.XMLName.Local {
			case "movieId":
				it.TMDBMovieID, _ = strconv.Atoi(v)
			case "tvId":
				it.TMDBTVID, _ = strconv.Atoi(v)
			}
		case nsDC:
			if e.XMLName.Local == "creator" {
				it.Creator = v
			}
		}
	}
	if it.GUID == "" {
		it.GUID = it.Link
	}
	if it.FilmTitle == "" || strings.HasPrefix(it.GUID, "letterboxd-list-") {
		return it, false
	}
	it.Review, it.Poster, it.Spoilers = parseDescription(description)
	return it, true
}

func yes(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "yes", "true", "1", "y":
		return true
	}
	return false
}

// parseStars reads a rating: "3.5" (or "3,5"); anything else is no rating.
func parseStars(s string) float64 {
	s = strings.ReplaceAll(strings.TrimSpace(s), ",", ".")
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f <= 0 {
		return 0
	}
	if f > 5 {
		f = 5
	}
	return f
}

var rssDateLayouts = []string{
	time.RFC1123Z,
	"Mon, 2 Jan 2006 15:04:05 -0700",
	time.RFC1123,
	"Mon, 2 Jan 2006 15:04:05 MST",
	"2 Jan 2006 15:04:05 -0700",
	time.RFC3339,
}

func parseRSSDate(s string) time.Time {
	s = strings.TrimSpace(s)
	for _, layout := range rssDateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// watchedOnRe is the description of an entry without a review.
var watchedOnRe = regexp.MustCompile(`(?i)^(re)?watched on [a-z]+,? [a-z]+ \d{1,2},? \d{4}\.?$`)

var spoilerRe = regexp.MustCompile(`(?i)^this review may contain spoilers\.?$`)

// parseDescription splits an item's HTML description into the review (plain
// text), the poster and whether the review is marked as having spoilers.
func parseDescription(s string) (review, poster string, spoilers bool) {
	if strings.TrimSpace(s) == "" {
		return "", "", false
	}
	nodes, err := html.ParseFragment(strings.NewReader(s), &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body})
	if err != nil {
		return strings.TrimSpace(s), "", false
	}
	var all strings.Builder
	for _, n := range nodes {
		if poster == "" {
			poster = firstImage(n)
		}
		all.WriteString(nodeText(n))
	}
	var blocks []string
	for _, text := range strings.Split(cleanText(all.String()), "\n\n") {
		switch text = strings.TrimSpace(text); {
		case text == "":
		case spoilerRe.MatchString(text):
			spoilers = true
		default:
			blocks = append(blocks, text)
		}
	}
	if len(blocks) == 1 && watchedOnRe.MatchString(blocks[0]) {
		return "", poster, spoilers
	}
	return strings.Join(blocks, "\n\n"), poster, spoilers
}

func firstImage(n *html.Node) string {
	if n.Type == html.ElementNode && n.DataAtom == atom.Img {
		for _, a := range n.Attr {
			if a.Key == "src" {
				return a.Val
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if src := firstImage(c); src != "" {
			return src
		}
	}
	return ""
}

// blockElems start on a line of their own.
var blockElems = map[atom.Atom]bool{
	atom.P: true, atom.Div: true, atom.Blockquote: true, atom.Li: true, atom.Ul: true, atom.Ol: true,
	atom.H1: true, atom.H2: true, atom.H3: true, atom.H4: true, atom.H5: true, atom.H6: true, atom.Pre: true,
}

// nodeText is the text of n: whitespace runs collapse to a space, <br> and
// block elements become line breaks.
func nodeText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		switch n.Type {
		case html.TextNode:
			b.WriteString(collapseSpace(n.Data))
			return
		case html.ElementNode:
			switch n.DataAtom {
			case atom.Br:
				b.WriteString("\n")
				return
			case atom.Script, atom.Style, atom.Img:
				return
			}
		}
		block := n.Type == html.ElementNode && blockElems[n.DataAtom]
		if block {
			b.WriteString("\n\n")
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if block {
			b.WriteString("\n\n")
		}
	}
	walk(n)
	return b.String()
}

var spaceRe = regexp.MustCompile(`[ \t\r\n\f\v]+`)

func collapseSpace(s string) string { return spaceRe.ReplaceAllString(s, " ") }

var blankLinesRe = regexp.MustCompile(`\n{3,}`)

// cleanText trims each line and keeps at most one blank line in a row.
func cleanText(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(strings.ReplaceAll(l, " ", " "))
	}
	s = strings.Join(lines, "\n")
	s = blankLinesRe.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}
