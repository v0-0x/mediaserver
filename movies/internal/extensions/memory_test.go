package extensions

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dop251/goja"
)

// smallMemoryBudget gives the watchdog a small budget and a quick look at
// the heap for a test.
func smallMemoryBudget(t *testing.T, budget uint64) {
	t.Helper()
	oldBudget, oldEvery := memoryBudgetForTests, memorySampleEvery
	memoryBudgetForTests, memorySampleEvery = budget, 10*time.Millisecond
	t.Cleanup(func() { memoryBudgetForTests, memorySampleEvery = oldBudget, oldEvery })
}

// fewVMSlots lets n extensions run at once for a test.
func fewVMSlots(t *testing.T, n int) {
	t.Helper()
	old := vmSlots
	vmSlots = make(chan struct{}, n)
	t.Cleanup(func() { vmSlots = old })
}

// Scripts that build a huge value are stopped by the watchdog with an error
// saying so, whether it's a call into a provider, a Nuvio provider's
// getStreams or its top-level code, and the runtime is closed (what it
// held is freed).
func TestMemoryWatchdogStopsHugeAllocations(t *testing.T) {
	smallMemoryBudget(t, 24<<20)
	const (
		hugeArray  = `const parts = []; for (let i = 0; ; i++) parts.push('x'.repeat(1 << 16) + i);`
		hugeString = `let s = 'y'.repeat(1 << 20); for (;;) s += s.slice(0, 1 << 20) + '!';`
	)
	call := func(t *testing.T, rt *Runtime) error {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, err := rt.CallProvider(ctx, "search", map[string]any{"query": "x"})
		return err
	}
	nuvio := func(t *testing.T, rt *Runtime) error {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, err := rt.callNuvio(ctx, "1", "movie", 0, 0)
		return err
	}
	for _, tc := range []struct {
		name string
		run  func(t *testing.T) (*Runtime, error)
	}{
		{"array in a call", func(t *testing.T) (*Runtime, error) {
			rt := testRuntime(t, `class Provider { search() { `+hugeArray+` } }`, RuntimeOptions{})
			return rt, call(t, rt)
		}},
		{"string in a call", func(t *testing.T) (*Runtime, error) {
			rt := testRuntime(t, `class Provider { search() { `+hugeString+` } }`, RuntimeOptions{})
			return rt, call(t, rt)
		}},
		{"array in an async call, after an await", func(t *testing.T) (*Runtime, error) {
			rt := testRuntime(t, `class Provider { async search() { await Promise.resolve(); `+hugeArray+` } }`, RuntimeOptions{})
			return rt, call(t, rt)
		}},
		{"string in getStreams", func(t *testing.T) (*Runtime, error) {
			rt := nuvioRuntime(t, nil, `module.exports = { getStreams: async function () { `+hugeString+` } };`)
			return rt, nuvio(t, rt)
		}},
		{"array in top-level code", func(t *testing.T) (*Runtime, error) {
			man := providerManifest("hog")
			prog, err := Compile(man, hugeArray)
			if err != nil {
				t.Fatal(err)
			}
			rt, err := NewRuntime(man, prog, RuntimeOptions{LoadTimeout: 20 * time.Second})
			return rt, err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Now()
			rt, err := tc.run(t)
			if !errors.Is(err, ErrTooMuchMemory) || !strings.Contains(err.Error(), "the provider used too much memory") {
				t.Fatalf("err = %v", err)
			}
			if d := time.Since(start); d > 10*time.Second {
				t.Errorf("stopped after %s", d)
			}
			if rt != nil {
				if !rt.closed.Load() {
					t.Error("the runtime wasn't closed")
				}
				if logs := fmt.Sprint(rt.Logs()); !strings.Contains(logs, "used too much memory") {
					t.Errorf("logs: %s", logs)
				}
			}
		})
	}
}

// A provider the watchdog stopped says why on its card, and starts afresh
// on its next call.
func TestProviderStartsAgainAfterUsingTooMuchMemory(t *testing.T) {
	smallMemoryBudget(t, 24<<20)
	m := newTestManager(t)
	installTestExtension(t, m, providerManifest("hog"), `
class Provider {
  search(opts) {
    if (opts.query === 'huge') { const parts = []; for (let i = 0; ; i++) parts.push('x'.repeat(1 << 16) + i); }
    return [{ id: '1', title: opts.query, url: '', subOrDub: 'sub' }];
  }
}`, false)
	p, err := m.OnlineStreamProvider("hog")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := p.Search(ctx, nil, "huge", false); !errors.Is(err, ErrTooMuchMemory) {
		t.Fatalf("huge: %v", err)
	}
	waitFor(t, 5*time.Second, "the card to say why", func() bool {
		inf, _ := m.Get("hog")
		return inf.Error == ErrTooMuchMemory.Error() && !inf.Running
	})
	res, err := p.Search(ctx, nil, "small", false)
	if err != nil || len(res) != 1 || res[0].Title != "small" {
		t.Fatalf("after: %v %+v", err, res)
	}
	if inf, _ := m.Get("hog"); inf.Error != "" || !inf.Running {
		t.Errorf("after: %+v", inf)
	}
	if logs := fmt.Sprint(m.Logs("hog")); !strings.Contains(logs, "used too much memory") {
		t.Errorf("the logs lost why: %s", logs)
	}
}

// A plugin whose handler builds a huge value is stopped, and says why.
func TestPluginStoppedForUsingTooMuchMemory(t *testing.T) {
	smallMemoryBudget(t, 24<<20)
	m := newTestManager(t)
	l := installTestExtension(t, m, pluginManifest("hog-plugin"), `
function init() {
  $ui.register(function (ctx) {
    ctx.registerEventHandler('grow', function () {
      const parts = []; for (let i = 0; ; i++) parts.push('x'.repeat(1 << 16) + i);
    });
  });
}`, true)
	m.startPlugin(l)
	if inf, _ := m.Get("hog-plugin"); !inf.Running || inf.Error != "" {
		t.Fatalf("not started: %+v", inf)
	}
	if err := m.DispatchPluginEvent("hog-plugin", map[string]any{"kind": "handler", "name": "grow"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, "the plugin to be stopped", func() bool {
		inf, _ := m.Get("hog-plugin")
		return !inf.Running && inf.Error == errPluginMemory.Error()
	})
}

// Ordinary scripts aren't affected: real budget, ordinary work, the string
// helpers as they were for ordinary sizes; only absurd ones are refused.
func TestNormalScriptsUnaffectedByMemoryGuards(t *testing.T) {
	rt := testRuntime(t, `
class Provider {
  search() {
    const items = [];
    for (let i = 0; i < 20000; i++) items.push({ id: String(i), title: 'Title ' + i, tags: ['a', 'b'] });
    const big = 'z'.repeat(1 << 20);
    const json = JSON.parse(JSON.stringify(items));
    let refused = '';
    try { 'x'.repeat(1e10); } catch (e) { refused = e.name + ': ' + e.message; }
    let refusedPad = '';
    try { 'x'.padStart(1e9, '-'); } catch (e) { refusedPad = e.name; }
    return {
      count: json.length, size: big.length, refused: refused, refusedPad: refusedPad,
      helpers: ['ab'.repeat(3), '5'.padStart(3, '0'), 'x'.padEnd(3, '.'), 'q'.repeat(0)].join('|'),
      errors: [(function(){ try { 'a'.repeat(-1) } catch (e) { return e.name } })(), (function(){ try { String.prototype.repeat.call(null, 2) } catch (e) { return e.name } })()].join('|'),
    };
  }
}`, RuntimeOptions{})
	for i := 0; i < 3; i++ {
		raw, err := rt.CallProvider(context.Background(), "search")
		if err != nil {
			t.Fatal(err)
		}
		got := string(raw)
		for _, want := range []string{`"count":20000`, `"size":1048576`, `"refused":"RangeError: Invalid string length"`, `"refusedPad":"RangeError"`,
			`"helpers":"ababab|005|x..|"`, `"errors":"RangeError|TypeError"`} {
			if !strings.Contains(got, want) {
				t.Fatalf("call %d: %s lacks %s", i, got, want)
			}
		}
	}
	if rt.closed.Load() || rt.memoryStopped.Load() {
		t.Fatal("the watchdog stopped an ordinary provider")
	}
}

// At most so many extensions run at once; the others wait their turn, and
// give up when their caller does.
func TestRunningExtensionsCapped(t *testing.T) {
	var running, most atomic.Int32
	enter := func(r *Runtime, vm *goja.Runtime) error {
		return vm.Set("enter", func() {
			n := running.Add(1)
			for {
				m := most.Load()
				if n <= m || most.CompareAndSwap(m, n) {
					break
				}
			}
			time.Sleep(60 * time.Millisecond)
			running.Add(-1)
		})
	}
	src := `class Provider { search() { enter(); return 'done'; } }`
	var rts []*Runtime
	for i := 0; i < 6; i++ {
		rts = append(rts, testRuntime(t, src, RuntimeOptions{Extra: enter}))
	}
	fewVMSlots(t, 2)

	var wg sync.WaitGroup
	errs := make(chan error, len(rts))
	for _, rt := range rts {
		wg.Add(1)
		go func(rt *Runtime) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if _, err := rt.CallProvider(ctx, "search"); err != nil {
				errs <- err
			}
		}(rt)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("call: %v", err)
	}
	if n := most.Load(); n != 2 {
		t.Fatalf("%d ran at once, want 2", n)
	}

	// Both turns taken: a caller that stops waiting gets its error at once.
	release1, _ := acquireVM(context.Background())
	release2, _ := acquireVM(context.Background())
	defer release1()
	defer release2()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	returnsWithin(t, 2*time.Second, "a call waiting for its turn", func() {
		if _, err := rts[0].CallProvider(ctx, "search"); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("err = %v", err)
		}
	})
	if n := running.Load(); n != 0 {
		t.Errorf("%d running", n)
	}
}

// Answers to fetch() are read up to a size for their type, refused past it
// whether the server says their length or not; Nuvio repositories and code
// keep their own limits.
func TestFetchAnswerSizeLimits(t *testing.T) {
	bigText := strings.Repeat("a", maxFetchText+1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/big.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(bigText))
		case "/big-chunked.html":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			for i := 0; i < len(bigText); i += 1 << 20 {
				_, _ = w.Write([]byte(bigText[i:min(i+1<<20, len(bigText))]))
				w.(http.Flusher).Flush()
			}
		case "/image.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte(bigText)) // over the text limit, under the binary one
		case "/ok.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
		}
	}))
	defer srv.Close()
	f := testFetcher(srv)
	for _, tc := range []struct {
		path  string
		limit int64
		ok    bool
	}{
		{"/ok.json", 0, true},
		{"/big.json", 0, false},
		{"/big-chunked.html", 0, false},
		{"/image.png", 0, true},
		{"/ok.json", 4, false}, // a caller's own limit
	} {
		resp, err := f.Do(context.Background(), fetchRequest{url: "http://fetch.test" + tc.path, limit: tc.limit})
		if tc.ok && (err != nil || resp.status != 200) {
			t.Errorf("%s: %v", tc.path, err)
		}
		if !tc.ok && (err == nil || !strings.Contains(err.Error(), "too large")) {
			t.Errorf("%s (limit %d): err = %v", tc.path, tc.limit, err)
		}
	}

	// What an extension sees.
	rt := testRuntime(t, `class Provider { async search() {
		try { await fetch('http://fetch.test/big.json'); return 'read'; } catch (e) { return String(e.message || e); }
	} }`, RuntimeOptions{Fetcher: f})
	raw, err := rt.CallProvider(context.Background(), "search")
	if err != nil || !strings.Contains(string(raw), "too large (over 8 MB)") {
		t.Fatalf("%v %s", err, raw)
	}
}

func TestFetchLimitByType(t *testing.T) {
	for ct, want := range map[string]int64{
		"":                                maxFetchText,
		"text/html; charset=utf-8":        maxFetchText,
		"application/json":                maxFetchText,
		"application/vnd.api+json":        maxFetchText,
		"application/javascript":          maxFetchText,
		"application/xml":                 maxFetchText,
		"application/vnd.apple.mpegurl":   maxFetchText,
		"image/jpeg":                      maxFetchBinary,
		"application/octet-stream":        maxFetchBinary,
		"Application/JSON; charset=UTF-8": maxFetchText,
	} {
		if got := fetchLimit(ct); got != want {
			t.Errorf("fetchLimit(%q) = %d, want %d", ct, got, want)
		}
	}
}

// The budget is half the soft memory limit, never tiny.
func TestMemoryBudget(t *testing.T) {
	if b := memoryBudget(); b < minMemoryBudget {
		t.Fatalf("budget %d", b)
	}
	if heapBytes() == 0 {
		t.Fatal("the heap's size can't be read")
	}
}
