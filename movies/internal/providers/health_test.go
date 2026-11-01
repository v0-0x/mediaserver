package providers

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// AggregateEach hands over each provider's answer as soon as it comes, the
// fastest first, cleaned up as Aggregate returns them.
func TestAggregateEach(t *testing.T) {
	both := []string{TypeMovie, TypeTV}
	ps := []Provider{
		&fakeProvider{id: "slow", types: both, delay: 150 * time.Millisecond, out: []Stream{{URL: "https://slow/1.mp4"}}},
		&fakeProvider{id: "fast", types: both, out: []Stream{{URL: "https://fast/1.mp4"}, {Title: "nothing to play"}}},
		&fakeProvider{id: "fails", types: both, delay: 50 * time.Millisecond, err: errors.New("site down")},
		&fakeProvider{id: "movies-only", types: []string{TypeMovie}},
	}
	var mu sync.Mutex
	var order []string
	var fast Answer
	streams, errs := AggregateEach(context.Background(), ps, Request{Type: TypeTV}, time.Second, func(a Answer) {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, a.Provider.ID())
		if a.Provider.ID() == "fast" {
			fast = a
		}
		if a.Provider.ID() == "fails" && (a.Err == nil || a.Took < 40*time.Millisecond) {
			t.Errorf("failing answer = %+v", a)
		}
	})
	if got := strings.Join(order, ","); got != "fast,fails,slow" {
		t.Errorf("answers came in as %s", got)
	}
	if len(fast.Streams) != 1 || fast.Streams[0].Provider != "fast" || fast.Streams[0].FileIdx != -1 || fast.Err != nil {
		t.Errorf("fast answer = %+v", fast)
	}
	if len(streams) != 2 || streams[0].Provider != "slow" || len(errs) != 1 {
		t.Errorf("streams %+v, errors %+v", streams, errs)
	}
}

// Failures in a row demote a provider until it answers again; errors that
// say nothing about the provider aren't counted; the last answers' times
// are kept.
func TestHealth(t *testing.T) {
	h := NewHealth()
	now := time.Unix(1_700_000_000, 0)
	h.now = func() time.Time { return now }
	changes := 0
	h.OnChange = func() { changes++ }

	h.Record("a", nil, 100*time.Millisecond)
	for i := 0; i < DemoteAfter-1; i++ {
		h.Record("a", errors.New("site down"), time.Second)
	}
	if h.Demoted("a") {
		t.Fatalf("demoted after %d failures", DemoteAfter-1)
	}
	h.Record("a", Skip(errors.New("no IMDb id")), 0)
	h.Record("a", context.Canceled, 0)
	if st := h.Get("a"); st.Failures != DemoteAfter-1 || st.Successes != 1 || changes != DemoteAfter {
		t.Fatalf("skips or cancellations counted: %+v (%d changes)", st, changes)
	}
	h.Record("a", errors.New("no answer within 20s"), 20*time.Second)
	st := h.Get("a")
	if !h.Demoted("a") || st.ConsecutiveFailures != DemoteAfter || st.LastError != "no answer within 20s" || st.LastErrorAt != now.Unix() || st.LastSuccessAt != now.Unix() {
		t.Fatalf("after %d failures: %+v", DemoteAfter, st)
	}
	if st.AvgMs() != (100+1000+1000+20000)/4 {
		t.Errorf("average = %d ms (%v)", st.AvgMs(), st.Latencies)
	}

	h.Record("a", nil, 300*time.Millisecond)
	if h.Demoted("a") || h.Get("a").ConsecutiveFailures != 0 || h.Get("a").Failures != DemoteAfter {
		t.Fatalf("after a success: %+v", h.Get("a"))
	}
	for i := 0; i < 2*latencyWindow; i++ {
		h.Record("a", nil, time.Duration(i)*time.Millisecond)
	}
	if lat := h.Get("a").Latencies; len(lat) != latencyWindow || lat[len(lat)-1] != 2*latencyWindow-1 {
		t.Errorf("latencies = %v", lat)
	}

	// Saved and loaded back (a copy: changing one leaves the other).
	all := h.All()
	other := NewHealth()
	other.Load(all)
	other.Record("a", errors.New("x"), 0)
	if h.Get("a").Failures == other.Get("a").Failures || other.Get("b").Successes != 0 {
		t.Errorf("loaded stats shared: %+v / %+v", h.Get("a"), other.Get("a"))
	}

	var none *Health
	none.Record("a", errors.New("x"), 0)
	if none.Demoted("a") || len(none.All()) != 0 {
		t.Error("a nil Health records")
	}
}

// Errors keep the host of the links they name, not their path or query
// (an addon's settings, keys).
func TestErrorText(t *testing.T) {
	link := "https://addon.example/eyJhcGlLZXkiOiJzZWNyZXQifQ/stream/movie/tt0063350.json?token=secret"
	err := fmt.Errorf("stremio: %w", &url.Error{Op: "Get", URL: link, Err: errors.New("connection refused")})
	got := ErrorText(err)
	if strings.Contains(got, "secret") || strings.Contains(got, "eyJ") || !strings.Contains(got, "https://addon.example/…") || !strings.Contains(got, "connection refused") {
		t.Errorf("ErrorText = %q", got)
	}
	if got := ErrorText(errors.New("the addon answered 500")); got != "the addon answered 500" {
		t.Errorf("plain error = %q", got)
	}
	if ErrorText(nil) != "" {
		t.Error("nil error")
	}
	if !IsSkip(fmt.Errorf("wrapped: %w", Skip(errors.New("no id")))) || IsSkip(errors.New("x")) || Skip(nil) != nil {
		t.Error("Skip/IsSkip")
	}
}
