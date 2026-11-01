package extensions

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestUnloadIfIdle(t *testing.T) {
	const after = time.Minute
	now := time.Now()
	cases := []struct {
		name   string
		at     time.Time // when unloading is considered
		busy   bool      // a call is in progress
		unload bool
	}{
		{"just used", now, false, false},
		{"used a moment ago", now.Add(after / 2), false, false},
		{"unused for the period", now.Add(after + time.Second), false, true},
		{"in a call for long", now.Add(10 * after), true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rt := testRuntime(t, `console.log("loaded")`, RuntimeOptions{})
			rt.lastUsed.Store(now.UnixNano())
			if c.busy {
				defer rt.use()()
				rt.lastUsed.Store(now.UnixNano())
			}
			l := &Loaded{Manifest: rt.manifest, rt: rt}
			if got := l.unloadIfIdle(c.at, after); got != c.unload {
				t.Fatalf("unloaded = %v, want %v", got, c.unload)
			}
			if c.unload {
				if l.rt != nil || !rt.closed.Load() {
					t.Error("the runtime is still there")
				}
				// What it printed stays for the Logs view.
				if len(l.lastLogs) != 1 || l.lastLogs[0].Message != "loaded" {
					t.Errorf("logs kept = %+v", l.lastLogs)
				}
			} else if l.rt != rt || rt.closed.Load() {
				t.Error("the runtime was closed")
			}
		})
	}
}

func TestJoinLogs(t *testing.T) {
	line := func(s string) LogLine { return LogLine{Message: s} }
	var many []LogLine
	for i := 0; i < maxLogLines; i++ {
		many = append(many, line("old"))
	}
	got := joinLogs(many, []LogLine{line("new")})
	if len(got) != maxLogLines || got[len(got)-1].Message != "new" {
		t.Errorf("len = %d, last = %+v", len(got), got[len(got)-1])
	}
	if got := joinLogs(nil, nil); len(got) != 0 {
		t.Errorf("got %+v", got)
	}
}

// An unused provider's runtime is closed after a while, starts again on
// its next use, and keeps its $store and its logs across the restart.
func TestIdleProviderUnloadedAndRestarted(t *testing.T) {
	m := newTestManager(t)
	m.idleAfter = 80 * time.Millisecond
	installTestExtension(t, m, providerManifest("idle"), `
var runs = ($store.get('runs') || 0) + 1;
$store.set('runs', runs);
class Provider {
  search(opts) {
    console.log('search ' + opts.query + ' in run ' + runs);
    return [{ id: String(runs), title: opts.query, url: '', subOrDub: 'sub' }];
  }
}`, false)
	p, err := m.OnlineStreamProvider("idle")
	if err != nil {
		t.Fatal(err)
	}
	search := func(q string) string {
		t.Helper()
		res, err := p.Search(context.Background(), nil, q, false)
		if err != nil || len(res) != 1 {
			t.Fatalf("search: %v %+v", err, res)
		}
		return res[0].ID
	}
	running := func() bool {
		inf, _ := m.Get("idle")
		return inf.Running
	}

	if run := search("first"); run != "1" {
		t.Fatalf("run = %s", run)
	}
	if !running() {
		t.Fatal("not running after a call")
	}
	waitFor(t, 3*time.Second, "the idle runtime to be unloaded", func() bool { return !running() })

	// Started again on demand, with the state it saved.
	if run := search("second"); run != "2" {
		t.Fatalf("run = %s, want the second run", run)
	}
	var msgs []string
	for _, l := range m.Logs("idle") {
		msgs = append(msgs, l.Message)
	}
	if got := strings.Join(msgs, "|"); got != "search first in run 1|search second in run 2" {
		t.Errorf("logs = %s", got)
	}
}

// A runtime busy with a long call isn't unloaded under it.
func TestBusyProviderNotUnloaded(t *testing.T) {
	m := newTestManager(t)
	m.idleAfter = 40 * time.Millisecond
	installTestExtension(t, m, providerManifest("busy"), `
class Provider {
  search(opts) {
    $sleep(400);
    return [{ id: 'done', title: opts.query, url: '', subOrDub: 'sub' }];
  }
}`, false)
	p, err := m.OnlineStreamProvider("busy")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	var callErr error
	go func() {
		defer wg.Done()
		_, callErr = p.Search(context.Background(), nil, "slow", false)
	}()
	wg.Wait()
	if callErr != nil {
		t.Fatalf("the call was cut short: %v", callErr)
	}
	waitFor(t, 3*time.Second, "the runtime to be unloaded once idle", func() bool {
		inf, _ := m.Get("busy")
		return !inf.Running
	})
}

// Plugins are never unloaded, nor providers when unloading is off.
func TestUnloadIdleSkipsPluginsAndWhenOff(t *testing.T) {
	m := newTestManager(t)
	m.idleAfter = 0
	installTestExtension(t, m, providerManifest("kept"), `class Provider { search() { return []; } }`, false)
	p, err := m.OnlineStreamProvider("kept")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Search(context.Background(), nil, "x", false); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if inf, _ := m.Get("kept"); !inf.Running {
		t.Error("unloaded with unloading off")
	}

	l := installTestExtension(t, m, pluginManifest("plugin"), `function init() { $ui.register(function(ctx) {}); }`, true)
	m.startPlugin(l)
	m.idleAfter = time.Nanosecond
	if n := m.unloadIdle(time.Now().Add(time.Hour)); n != 1 {
		t.Errorf("unloaded %d runtimes, want only the provider's", n)
	}
	if inf, _ := m.Get("plugin"); !inf.Running {
		t.Error("the plugin was stopped")
	}
}
