package stream

import (
	"context"
	"sync"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/providers"
)

// Searches as they go
//
// Asking every provider takes as long as the slowest one (up to the
// provider timeout). StreamsLive reports each provider's answer as it comes
// in, so Watch online shows the first streams within a second or two and
// which providers are still looking; the whole result, ranked as Streams
// ranks it, comes at the end. Searches are still shared and kept: a request
// joining a search under way gets the answers that came before it, and
// answers kept from a moment ago are given all at once.

// Asked is a provider a search asks.
type Asked struct {
	Provider string `json:"provider"`
	Name     string `json:"name"`
	// Demoted is set for a provider that failed again and again (see
	// providers.Health): its streams come last.
	Demoted bool `json:"demoted,omitempty"`
}

// Answer is one provider's answer to a search.
type Answer struct {
	Provider string `json:"provider"`
	Name     string `json:"name"`
	// Streams are the provider's own, in its order (Streams ranks them with
	// the others' at the end).
	Streams []Stream `json:"streams"`
	Error   string   `json:"error,omitempty"`
	// Ms is how long the provider took.
	Ms int64 `json:"ms"`
	// Cached is set on answers kept from a moment ago.
	Cached bool `json:"cached,omitempty"`
}

// Progress receives a search's progress: Asking once, with the providers
// asked (the demoted ones last), then Answer for each provider, as it
// answers (all at once when kept from a moment ago). Either may be nil.
type Progress struct {
	Asking func([]Asked)
	Answer func(Answer)
}

// StreamsLive is Streams, reporting the search's progress to p.
func (s *Service) StreamsLive(ctx context.Context, media *catalog.Media, episode int, provider string, refresh bool, p *Progress) (*Result, error) {
	return s.streams(ctx, media, episode, provider, refresh, false, p)
}

// answerOf is a provider's answer as StreamsLive reports it, recorded in
// the providers' health.
func (s *Service) answerOf(a providers.Answer) Answer {
	s.Health.Record(a.Provider.ID(), a.Err, a.Took)
	out := Answer{Provider: a.Provider.ID(), Name: a.Provider.Name(), Ms: a.Took.Milliseconds(), Streams: make([]Stream, 0, len(a.Streams))}
	if a.Err != nil {
		out.Error = providers.ErrorText(a.Err)
	}
	for _, st := range a.Streams {
		out.Streams = append(out.Streams, Wrap(st))
	}
	return out
}

// Test asks one provider for req right away (whatever was found a moment
// ago), records how it went in the providers' health and returns its
// answer: the Test button of Settings › Providers.
func (s *Service) Test(ctx context.Context, p providers.Provider, req providers.Request) Answer {
	out := Answer{Provider: p.ID(), Name: p.Name(), Streams: []Stream{}}
	providers.AggregateEach(ctx, []providers.Provider{p}, req, s.options().Timeout, func(a providers.Answer) {
		out = s.answerOf(a)
	})
	return out
}

// replay is how each provider answered, from what was kept: each provider's
// streams, but those another provider listed first.
func (c *cached) replay() []Answer {
	byID := make(map[string]Stream, len(c.streams))
	for _, st := range c.streams {
		byID[st.ID] = st
	}
	out := make([]Answer, 0, len(c.answers))
	for _, a := range c.answers {
		ans := Answer{Provider: a.provider, Name: a.name, Error: a.err, Ms: a.ms, Cached: true, Streams: []Stream{}}
		for _, id := range a.ids {
			if st, ok := byID[id]; ok {
				ans.Streams = append(ans.Streams, st)
				delete(byID, id) // listed once
			}
		}
		out = append(out, ans)
	}
	return out
}

// flight is a search in progress that other requests for the same thing
// wait for, instead of asking every provider again. It keeps the answers
// that came in so far, for requests following its progress.
type flight struct {
	done chan struct{}
	res  *cached

	mu      sync.Mutex
	answers []Answer
	// more is closed (and replaced) when an answer comes.
	more chan struct{}
}

func newFlight() *flight {
	return &flight{done: make(chan struct{}), more: make(chan struct{})}
}

// add records a provider's answer and wakes up those following.
func (f *flight) add(a Answer) {
	f.mu.Lock()
	f.answers = append(f.answers, a)
	close(f.more)
	f.more = make(chan struct{})
	f.mu.Unlock()
}

// since returns the answers from the i-th on, and a channel closed when
// another one comes.
func (f *flight) since(i int) ([]Answer, chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.answers[i:], f.more
}

// wait waits for the search's result, handing each (when not nil) every
// answer as it comes.
func (f *flight) wait(ctx context.Context, each func(Answer)) (*cached, error) {
	seen := 0
	pass := func() chan struct{} {
		answers, more := f.since(seen)
		seen += len(answers)
		if each != nil {
			for _, a := range answers {
				each(a)
			}
		}
		return more
	}
	for {
		more := pass()
		select {
		case <-more:
		case <-f.done:
			pass() // the last answers came with the end
			return f.res, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}
