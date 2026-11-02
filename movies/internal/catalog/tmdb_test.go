package catalog

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/config"
)

// A v3 key goes in the api_key parameter, a v4 read access token (a JWT) in
// the Authorization header; the language comes from the settings.
func TestClientAuth(t *testing.T) {
	const token = "eyJhbGciOiJIUzI1NiJ9.eyJhdWQiOiJ0ZXN0In0.c2lnbmF0dXJl"
	var gotKey, gotAuth, gotLang atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey.Store(r.URL.Query().Get("api_key"))
		gotAuth.Store(r.Header.Get("Authorization"))
		gotLang.Store(r.URL.Query().Get("language"))
		reply(w, http.StatusOK, map[string]any{"genres": []any{}})
	}))
	defer srv.Close()
	cfg := config.Defaults()
	cfg.TMDB.Language = "fr-FR"
	c := NewClient(func() config.Settings { return cfg })
	c.base = srv.URL

	for _, tc := range []struct {
		key, wantParam, wantAuth string
	}{
		{testKey, testKey, ""},
		{"  " + testKey + " ", testKey, ""},
		{token, "", "Bearer " + token},
	} {
		cfg.TMDB.APIKey = tc.key
		if err := c.get(context.Background(), "/genre/movie/list", nil, nil); err != nil {
			t.Fatalf("%q: %v", tc.key, err)
		}
		if gotKey.Load() != tc.wantParam || gotAuth.Load() != tc.wantAuth || gotLang.Load() != "fr-FR" {
			t.Errorf("%q: api_key=%q Authorization=%q language=%q", tc.key, gotKey.Load(), gotAuth.Load(), gotLang.Load())
		}
	}
}

// Without a key nothing is asked, and the error tells where to add one.
func TestNoAPIKey(t *testing.T) {
	p, _ := newTestPlatformSettings(t, nil, nil)
	if p.Configured() {
		t.Fatal("configured without a key")
	}
	_, err := p.Media(context.Background(), 155, false)
	if !errors.Is(err, ErrNoAPIKey) || !strings.Contains(err.Error(), "Settings › Metadata") {
		t.Fatalf("got %v, want ErrNoAPIKey", err)
	}
	if _, err := p.Discover(context.Background()); !errors.Is(err, ErrNoAPIKey) {
		t.Fatalf("discover: %v", err)
	}
}

// The key is read on every request: one added (or changed) in the settings
// works at once.
func TestClientReadsSettingsLive(t *testing.T) {
	f := newFakeTMDB(t, nil)
	p, settings := newTestPlatformSettings(t, f, nil)
	ctx := context.Background()
	if _, err := p.Media(ctx, 155, false); !errors.Is(err, ErrNoAPIKey) {
		t.Fatalf("without a key: %v", err)
	}
	s := settings.Get()
	s.TMDB.APIKey = "wrong"
	if _, err := settings.Save(s); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Media(ctx, 155, false); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("with a wrong key: %v", err)
	}
	s.TMDB.APIKey = testKey
	if _, err := settings.Save(s); err != nil {
		t.Fatal(err)
	}
	if m, err := p.Media(ctx, 155, false); err != nil || m.Title.English != "The Dark Knight" {
		t.Fatalf("with the key: %v %v", m, err)
	}
}

func TestClientErrors(t *testing.T) {
	p := newTestPlatform(t, newFakeTMDB(t, nil))
	if _, err := p.Media(context.Background(), 404404, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing movie: %v", err)
	}
	if _, err := p.Season(context.Background(), ShowID(1396), 9); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing season: %v", err)
	}
}

// Rate limiting is waited out as TMDB asks, a server error retried once.
func TestClientRetries(t *testing.T) {
	var limited, failing atomic.Int32
	f := newFakeTMDB(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch {
		case r.URL.Path == "/movie/155" && limited.Add(-1) >= 0:
			w.Header().Set("Retry-After", "0")
			reply(w, http.StatusTooManyRequests, map[string]any{"status_code": 25, "status_message": "Your request count (41) is over the allowed limit of 40."})
			return true
		case r.URL.Path == "/tv/1396" && failing.Add(-1) >= 0:
			reply(w, http.StatusBadGateway, map[string]any{})
			return true
		}
		return false
	})
	p := newTestPlatform(t, f)
	ctx := context.Background()

	limited.Store(2)
	if _, err := p.Media(ctx, 155, true); err != nil {
		t.Fatalf("after two 429s: %v", err)
	}
	if n := f.count("/movie/155"); n != 3 {
		t.Errorf("%d requests, want 3", n)
	}
	limited.Store(100)
	if err := p.client.get(ctx, "/movie/155", nil, nil); !errors.Is(err, errRateLimited) {
		t.Errorf("rate limited for good: %v", err)
	}

	failing.Store(1)
	if _, err := p.Media(ctx, ShowID(1396), true); err != nil {
		t.Fatalf("after one server error: %v", err)
	}
	failing.Store(2)
	before := f.count("/tv/1396")
	if err := p.client.get(ctx, "/tv/1396", nil, nil); err == nil || !strings.Contains(err.Error(), "502") {
		t.Errorf("two server errors: %v", err)
	}
	if n := f.count("/tv/1396") - before; n != 2 {
		t.Errorf("%d attempts after server errors, want 2", n)
	}
}

func TestRetryAfter(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"":     time.Second,
		"0":    0,
		"3":    3 * time.Second,
		"120":  maxRetryAfter,
		"soon": time.Second,
	} {
		h := http.Header{}
		if in != "" {
			h.Set("Retry-After", in)
		}
		if got := retryAfter(h); got != want {
			t.Errorf("Retry-After %q: %v, want %v", in, got, want)
		}
	}
}

// The key is part of request URLs: errors (shown in the UI, logged) must
// not quote it.
func TestErrorsHideTheKey(t *testing.T) {
	f := newFakeTMDB(t, nil)
	p := newTestPlatform(t, f)
	f.srv.Close()
	_, err := p.Media(context.Background(), 155, false)
	if err == nil || strings.Contains(err.Error(), testKey) {
		t.Fatalf("got %v", err)
	}
}

func TestEnvOverrides(t *testing.T) {
	t.Setenv("MOVIKO_TMDB_URL", "http://127.0.0.1:9/3/")
	t.Setenv("MOVIKO_TMDB_IMAGE_URL", "http://127.0.0.1:9/img")
	c := NewClient(nil)
	if c.base != "http://127.0.0.1:9/3" || c.Image("w500", "/a.jpg") != "http://127.0.0.1:9/img/w500/a.jpg" || c.Image("w500", "") != "" {
		t.Fatalf("base %q, image %q", c.base, c.Image("w500", "/a.jpg"))
	}
}
