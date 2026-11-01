package letterboxd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// The official API
//
// Letterboxd's API (api-docs.letterboxd.com) is only open to apps it grants
// a client id and secret to, so this client can't be tried against the real
// service here and is written from the documentation, defensively:
//
//   - Sign-in is OAuth2's password grant: POST /auth/token with the form
//     grant_type=password, username, password, client_id, client_secret,
//     answered with {access_token, token_type, refresh_token, expires_in}.
//     The refresh_token grant renews it.
//   - Requests carry "Authorization: Bearer <access_token>".
//   - Films are found by their TMDB or IMDb id: GET /films?filmId=tmdb:603
//     lists them; GET /film/tmdb:603 is tried too when that comes back
//     empty, as the id forms are accepted wherever a film id is.
//   - Errors come as JSON with "message" (API) or "error" and
//     "error_description" (OAuth); 401, invalid_grant and invalid_token mean
//     the token or the credentials were refused (ErrAPIUnauthorized).
//   - Unknown fields are ignored, missing ones read as empty: only ids,
//     names and the token are needed.

// DefaultAPIBaseURL is the API's address ($KUMOBOX_LETTERBOXD_API_URL or
// Sync.APIBaseURL point elsewhere).
const DefaultAPIBaseURL = "https://api.letterboxd.com/api/v0"

var (
	// ErrAPIUnauthorized: the API refused the token or the credentials.
	ErrAPIUnauthorized = errors.New("letterboxd: the API refused the sign-in, please sign in again")
	// ErrNoAPIAccess: no API client credentials, or nobody signed in.
	ErrNoAPIAccess = errors.New("letterboxd: API access isn't set up (it needs client credentials from Letterboxd and a sign-in)")
	// ErrFilmNotFound: Letterboxd has no film with that id.
	ErrFilmNotFound = errors.New("letterboxd: film not found on Letterboxd")
)

// APIError is an error answer of the API.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = e.Code
	}
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	return fmt.Sprintf("letterboxd API: %s (HTTP %d)", msg, e.Status)
}

// Unwrap makes refused credentials match ErrAPIUnauthorized.
func (e *APIError) Unwrap() error {
	switch {
	case e.Status == http.StatusUnauthorized, e.Code == "invalid_grant", e.Code == "invalid_token", e.Code == "invalid_client":
		return ErrAPIUnauthorized
	}
	return nil
}

// Token is a signed-in member's access to the API.
type Token struct {
	AccessToken  string    `json:"accessToken"`
	RefreshToken string    `json:"refreshToken,omitempty"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

// Expired reports whether the token has expired (or will within a minute).
func (t *Token) Expired(now time.Time) bool {
	return t == nil || t.AccessToken == "" || (!t.ExpiresAt.IsZero() && now.After(t.ExpiresAt.Add(-time.Minute)))
}

// Member is the signed-in Letterboxd member.
type Member struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	Avatar      string `json:"avatar,omitempty"`
}

// Film is a Letterboxd film; ID is its LID ("2a1m").
type Film struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ReleaseYear int    `json:"releaseYear,omitempty"`
}

// LogEntryRequest is a diary entry (or, without Date, a review or rating) to
// create.
type LogEntryRequest struct {
	FilmID   string
	Date     string // YYYY-MM-DD, the diary date
	Rewatch  bool
	Rating   float64 // stars, 0 for none
	Like     bool
	Review   string
	Spoilers bool
	Tags     []string
}

// RelationshipUpdate changes the member's relationship with a film; nil
// fields are left as they are. A Rating of 0 removes the rating.
type RelationshipUpdate struct {
	Watched     *bool    `json:"watched,omitempty"`
	Liked       *bool    `json:"liked,omitempty"`
	InWatchlist *bool    `json:"inWatchlist,omitempty"`
	Rating      *float64 `json:"rating,omitempty"`
}

// APIClient talks to the API. AccessToken is sent with every request but
// the sign-in ones.
type APIClient struct {
	BaseURL      string
	ClientID     string
	ClientSecret string
	AccessToken  string
	HTTP         *http.Client

	now func() time.Time
}

func (c *APIClient) base() string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	return DefaultAPIBaseURL
}

func (c *APIClient) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// PasswordLogin signs a member in with their username (or email) and
// password.
func (c *APIClient) PasswordLogin(ctx context.Context, username, password string) (*Token, error) {
	if c.ClientID == "" || c.ClientSecret == "" {
		return nil, ErrNoAPIAccess
	}
	return c.token(ctx, url.Values{
		"grant_type": {"password"},
		"username":   {username},
		"password":   {password},
	})
}

// Refresh renews a token with its refresh token.
func (c *APIClient) Refresh(ctx context.Context, refreshToken string) (*Token, error) {
	if c.ClientID == "" || c.ClientSecret == "" {
		return nil, ErrNoAPIAccess
	}
	if refreshToken == "" {
		return nil, ErrAPIUnauthorized
	}
	tok, err := c.token(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	})
	if err == nil && tok.RefreshToken == "" {
		tok.RefreshToken = refreshToken
	}
	return tok, err
}

func (c *APIClient) token(ctx context.Context, form url.Values) (*Token, error) {
	form.Set("client_id", c.ClientID)
	form.Set("client_secret", c.ClientSecret)
	var out struct {
		AccessToken  string          `json:"access_token"`
		RefreshToken string          `json:"refresh_token"`
		ExpiresIn    json.RawMessage `json:"expires_in"`
	}
	body := strings.NewReader(form.Encode())
	if err := c.do(ctx, http.MethodPost, "/auth/token", nil, body, "application/x-www-form-urlencoded", false, &out); err != nil {
		return nil, err
	}
	if out.AccessToken == "" {
		return nil, errors.New("letterboxd API: the sign-in answer has no access token")
	}
	tok := &Token{AccessToken: out.AccessToken, RefreshToken: out.RefreshToken}
	// expires_in is a number of seconds (some servers send it as a string).
	if secs, err := strconv.ParseFloat(strings.Trim(string(out.ExpiresIn), `"`), 64); err == nil && secs > 0 {
		tok.ExpiresAt = c.clock().Add(time.Duration(secs) * time.Second)
	}
	return tok, nil
}

// Me returns the signed-in member.
func (c *APIClient) Me(ctx context.Context) (*Member, error) {
	var out struct {
		Member struct {
			ID          string `json:"id"`
			Username    string `json:"username"`
			DisplayName string `json:"displayName"`
			Avatar      struct {
				Sizes []struct {
					Width int    `json:"width"`
					URL   string `json:"url"`
				} `json:"sizes"`
			} `json:"avatar"`
		} `json:"member"`
	}
	if err := c.getJSON(ctx, "/me", nil, &out); err != nil {
		return nil, err
	}
	m := &Member{ID: out.Member.ID, Username: out.Member.Username, DisplayName: out.Member.DisplayName}
	best := -1
	for _, s := range out.Member.Avatar.Sizes {
		if s.URL != "" && s.Width > best {
			best, m.Avatar = s.Width, s.URL
		}
	}
	if m.Username == "" && m.ID == "" {
		return nil, errors.New("letterboxd API: /me has no member")
	}
	if m.DisplayName == "" {
		m.DisplayName = m.Username
	}
	return m, nil
}

// FilmByTMDB finds the film of a TMDB movie id.
func (c *APIClient) FilmByTMDB(ctx context.Context, tmdbID int) (*Film, error) {
	if tmdbID <= 0 {
		return nil, ErrFilmNotFound
	}
	return c.findFilm(ctx, "tmdb:"+strconv.Itoa(tmdbID))
}

// FilmByIMDb finds the film of an IMDb id (tt0133093).
func (c *APIClient) FilmByIMDb(ctx context.Context, imdbID string) (*Film, error) {
	imdbID = strings.TrimSpace(imdbID)
	if imdbID == "" {
		return nil, ErrFilmNotFound
	}
	return c.findFilm(ctx, "imdb:"+imdbID)
}

func (c *APIClient) findFilm(ctx context.Context, id string) (*Film, error) {
	var list struct {
		Items []Film `json:"items"`
	}
	err := c.getJSON(ctx, "/films", url.Values{"filmId": {id}, "perPage": {"1"}}, &list)
	if err == nil {
		for _, f := range list.Items {
			if f.ID != "" {
				return &f, nil
			}
		}
	} else if !isNotFoundish(err) {
		return nil, err // refused, server or network error: no point trying again
	}
	// Nothing listed (or the filter isn't understood): ask for the film.
	var f Film
	err = c.getJSON(ctx, "/film/"+url.PathEscape(id), nil, &f)
	switch {
	case err == nil && f.ID != "":
		return &f, nil
	case err == nil || isNotFoundish(err):
		return nil, ErrFilmNotFound
	}
	return nil, err
}

func isNotFoundish(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && (ae.Status == http.StatusNotFound || ae.Status == http.StatusBadRequest)
}

// LogEntry creates a diary entry and returns its id.
func (c *APIClient) LogEntry(ctx context.Context, r LogEntryRequest) (string, error) {
	if r.FilmID == "" {
		return "", ErrFilmNotFound
	}
	body := map[string]any{"filmId": r.FilmID}
	// "like" is the member's like of the film, not of this entry: false
	// would unlike a film liked on Letterboxd, so only true is sent.
	if r.Like {
		body["like"] = true
	}
	if d := normDate(r.Date); d != "" {
		body["diaryDetails"] = map[string]any{"diaryDate": d, "rewatch": r.Rewatch}
	}
	if r.Rating > 0 {
		body["rating"] = ScoreToStars(r.Rating * 20)
	}
	if strings.TrimSpace(r.Review) != "" {
		body["review"] = map[string]any{"text": r.Review, "containsSpoilers": r.Spoilers}
	}
	if len(r.Tags) > 0 {
		body["tags"] = r.Tags
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := c.sendJSON(ctx, http.MethodPost, "/log-entries", body, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// SetFilmRelationship marks a film watched, liked, on the watchlist or rated.
func (c *APIClient) SetFilmRelationship(ctx context.Context, filmID string, u RelationshipUpdate) error {
	if filmID == "" {
		return ErrFilmNotFound
	}
	return c.sendJSON(ctx, http.MethodPatch, "/film/"+url.PathEscape(filmID)+"/me", u, nil)
}

func (c *APIClient) getJSON(ctx context.Context, path string, q url.Values, out any) error {
	return c.do(ctx, http.MethodGet, path, q, nil, "", true, out)
}

func (c *APIClient) sendJSON(ctx context.Context, method, path string, body, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	return c.do(ctx, method, path, nil, bytes.NewReader(raw), "application/json", true, out)
}

func (c *APIClient) do(ctx context.Context, method, path string, q url.Values, body io.Reader, contentType string, auth bool, out any) error {
	u := c.base() + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent())
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if auth {
		if c.AccessToken == "" {
			return ErrNoAPIAccess
		}
		req.Header.Set("Authorization", "Bearer "+c.AccessToken)
	}
	hc := c.HTTP
	if hc == nil {
		hc = defaultHTTP
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("letterboxd API: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("letterboxd API: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return apiError(resp.StatusCode, data)
	}
	if out == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("letterboxd API: unexpected answer: %w", err)
	}
	return nil
}

func apiError(status int, data []byte) error {
	e := &APIError{Status: status}
	var body struct {
		Message          string `json:"message"`
		Code             string `json:"code"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if json.Unmarshal(data, &body) == nil {
		e.Code = body.Code
		if e.Code == "" {
			e.Code = body.Error
		}
		e.Message = body.Message
		if e.Message == "" {
			e.Message = body.ErrorDescription
		}
	}
	return e
}
