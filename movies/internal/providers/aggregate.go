package providers

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
)

// ProviderError is why a provider gave nothing.
type ProviderError struct {
	Provider string `json:"provider"`
	Name     string `json:"name"`
	Message  string `json:"error"`
}

func (e ProviderError) Error() string { return e.Name + ": " + e.Message }

// ErrorText is err's message, the links it names cut down to their host:
// addon links often carry the user's addon settings or keys in their path
// or query, and errors are shown (to devices on the LAN too) and kept (see
// Health).
func ErrorText(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	var ue *url.Error
	if errors.As(err, &ue) && ue.URL != "" {
		short := "a link"
		if u, perr := url.Parse(ue.URL); perr == nil && u.Host != "" {
			short = u.Scheme + "://" + u.Host + "/…"
		}
		msg = strings.ReplaceAll(msg, ue.URL, short)
	}
	return msg
}

// Aggregate asks every provider that supports r.Type for streams, all at
// once, giving each one at most timeout (no limit when 0). It returns the
// streams in provider order (each provider's own order kept, duplicates
// and streams with nothing to play dropped, Provider/ProviderName filled
// in) and an error per provider that
// failed or didn't answer in time. A provider that ignores its context
// doesn't hold the others up: its late answer is dropped.
func Aggregate(ctx context.Context, ps []Provider, r Request, timeout time.Duration) ([]Stream, []ProviderError) {
	return AggregateEach(ctx, ps, r, timeout, nil)
}

// Answer is what one provider answered Aggregate.
type Answer struct {
	Provider Provider
	// Streams are the provider's playable streams, cleaned up as Aggregate
	// returns them (but not yet told apart from the other providers').
	Streams []Stream
	Err     error
	// Took is how long the provider took to answer (or to fail).
	Took time.Duration
}

// AggregateEach is Aggregate, calling each (when not nil) with every
// provider's answer as soon as it comes in: the UI shows the streams found
// so far, and the providers' health is recorded. each is called once at a
// time, and never after AggregateEach returned.
func AggregateEach(ctx context.Context, ps []Provider, r Request, timeout time.Duration, each func(Answer)) ([]Stream, []ProviderError) {
	results := make([]*Answer, len(ps))
	var wg sync.WaitGroup
	var mu sync.Mutex
	for i, p := range ps {
		if p == nil || !p.Supports(r.Type) {
			continue
		}
		wg.Add(1)
		go func(i int, p Provider) {
			defer wg.Done()
			start := time.Now()
			streams, err := callWithTimeout(ctx, timeout, func(ctx context.Context) ([]Stream, error) {
				return p.Streams(ctx, r)
			})
			a := &Answer{Provider: p, Err: err, Took: time.Since(start)}
			if err == nil {
				a.Streams = clean(p, streams)
			}
			mu.Lock()
			defer mu.Unlock()
			results[i] = a
			if each != nil {
				each(*a)
			}
		}(i, p)
	}
	wg.Wait()

	var out []Stream
	var errs []ProviderError
	for _, a := range results {
		if a == nil {
			continue
		}
		if a.Err != nil {
			errs = append(errs, ProviderError{Provider: a.Provider.ID(), Name: a.Provider.Name(), Message: ErrorText(a.Err)})
			continue
		}
		out = append(out, a.Streams...)
	}
	return Dedupe(out), errs
}

// clean keeps the streams of p that have something to play, with their
// provider and the fields every consumer expects filled in.
func clean(p Provider, streams []Stream) []Stream {
	out := make([]Stream, 0, len(streams))
	for _, s := range streams {
		if !Playable(s) {
			continue
		}
		if s.Provider == "" {
			s.Provider = p.ID()
		}
		if s.ProviderName == "" {
			s.ProviderName = p.Name()
		}
		out = append(out, normalize(s))
	}
	return out
}

// AggregateSubtitles asks every subtitle provider at once, like Aggregate.
func AggregateSubtitles(ctx context.Context, ps []SubtitleProvider, r Request, timeout time.Duration) ([]Subtitle, []ProviderError) {
	return aggregateSubtitles(ctx, ps, timeout, func(ctx context.Context, p SubtitleProvider) ([]Subtitle, error) {
		return p.Subtitles(ctx, r)
	})
}

// AggregateVideoSubtitles is AggregateSubtitles for the video file v: the
// providers that can match subtitles with the very file are told about
// it.
func AggregateVideoSubtitles(ctx context.Context, ps []SubtitleProvider, r Request, v Video, timeout time.Duration) ([]Subtitle, []ProviderError) {
	return aggregateSubtitles(ctx, ps, timeout, func(ctx context.Context, p SubtitleProvider) ([]Subtitle, error) {
		if vp, ok := p.(VideoSubtitleProvider); ok {
			return vp.VideoSubtitles(ctx, r, v)
		}
		return p.Subtitles(ctx, r)
	})
}

func aggregateSubtitles(ctx context.Context, ps []SubtitleProvider, timeout time.Duration, ask func(context.Context, SubtitleProvider) ([]Subtitle, error)) ([]Subtitle, []ProviderError) {
	type result struct {
		subs []Subtitle
		err  error
	}
	results := make([]result, len(ps))
	var wg sync.WaitGroup
	for i, p := range ps {
		if p == nil {
			continue
		}
		wg.Add(1)
		go func(i int, p SubtitleProvider) {
			defer wg.Done()
			subs, err := callWithTimeout(ctx, timeout, func(ctx context.Context) ([]Subtitle, error) {
				return ask(ctx, p)
			})
			results[i] = result{subs, err}
		}(i, p)
	}
	wg.Wait()
	var out []Subtitle
	var errs []ProviderError
	seen := map[string]bool{}
	for i, res := range results {
		if res.err != nil {
			id, name := describeAny(ps[i])
			errs = append(errs, ProviderError{Provider: id, Name: name, Message: ErrorText(res.err)})
			continue
		}
		for _, s := range res.subs {
			if s.URL == "" || seen[s.URL] {
				continue
			}
			seen[s.URL] = true
			out = append(out, s)
		}
	}
	return out, errs
}

func describeAny(p any) (id, name string) {
	if x, ok := p.(interface{ ID() string }); ok {
		id = x.ID()
	}
	if x, ok := p.(interface{ Name() string }); ok {
		name = x.Name()
	}
	if name == "" {
		name = id
	}
	return id, name
}

// callWithTimeout runs fn with a deadline and returns when it's done or
// when the deadline passes, whichever comes first. Panics become errors.
func callWithTimeout[T any](ctx context.Context, timeout time.Duration, fn func(context.Context) (T, error)) (T, error) {
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	} else {
		ctx, cancel = context.WithCancel(ctx)
	}
	defer cancel()
	type result struct {
		v   T
		err error
	}
	ch := make(chan result, 1)
	go func() {
		var res result
		defer func() {
			if p := recover(); p != nil {
				res.err = fmt.Errorf("internal error: %v", p)
			}
			ch <- res
		}()
		res.v, res.err = fn(ctx)
	}()
	select {
	case res := <-ch:
		if res.err != nil && errors.Is(res.err, context.DeadlineExceeded) && timeout > 0 {
			res.err = fmt.Errorf("no answer within %s", timeout)
		}
		return res.v, res.err
	case <-ctx.Done():
		var zero T
		if errors.Is(ctx.Err(), context.DeadlineExceeded) && timeout > 0 {
			return zero, fmt.Errorf("no answer within %s", timeout)
		}
		return zero, ctx.Err()
	}
}

// normalize fills the fields every consumer expects.
func normalize(s Stream) Stream {
	if s.InfoHash == "" && s.Magnet == "" {
		s.FileIdx = -1
	}
	if s.Type == "" {
		s.Type = StreamUnknown
		if s.Direct() {
			s.Type = GuessType(s.URL)
			if s.Type == StreamUnknown && s.Filename != "" {
				s.Type = GuessType(s.Filename)
			}
		}
	}
	return s
}
