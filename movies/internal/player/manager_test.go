package player

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/config"
	"github.com/v0-0x/mediaserver/movies/internal/db"
	"github.com/v0-0x/mediaserver/movies/internal/events"
	"github.com/v0-0x/mediaserver/movies/internal/history"
)

// newTestManager builds a Manager on a fresh database, with the network
// features (list progress) off and mpv replaced by fakes.
func newTestManager(t *testing.T, edit func(*config.Settings)) (*Manager, *fakeLauncher) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "moviko.db"))
	if err != nil {
		t.Fatal(err)
	}
	settings, err := config.NewStore(d)
	if err != nil {
		t.Fatal(err)
	}
	cfg := settings.Get()
	cfg.Playback.AutoUpdateProgress = false
	cfg.Playback.SkipIntro, cfg.Playback.SkipCredits = false, false
	cfg.Playback.RememberTracks = true
	cfg.Playback.AutoPlayNext = false
	if edit != nil {
		edit(&cfg)
	}
	if _, err := settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	hub := events.NewHub()
	m := NewManager(settings, history.NewStore(d), d, catalog.NewPlatform(d, hub, settings), hub)
	m.trackGuard = 50 * time.Millisecond
	m.trackSettle = 200 * time.Millisecond
	l := newFakeLauncher(t)
	m.launch = l.launch
	t.Cleanup(func() {
		m.Stop()
		_ = d.Close()
	})
	return m, l
}

// liveSession returns the manager's current session itself (not a copy).
func liveSession(t *testing.T, m *Manager) *Session {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cur == nil {
		t.Fatal("nothing is playing")
	}
	return m.cur
}

func waitLoopDone(t *testing.T, s *Session) {
	t.Helper()
	select {
	case <-s.loopDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the session's event loop did not finish")
	}
}

func TestMpvKeepsTrackChoiceThroughUnload(t *testing.T) {
	saved := TrackPrefs{MediaID: 7, AudioLang: "jpn", AudioTitle: "Japanese", AudioIndex: 1,
		SubLang: "eng", SubTitle: "Full Subtitles", SubIndex: 1, StreamMode: "sub"}
	want := saved
	want.SubTitle, want.SubIndex = "Signs & Songs", 2

	// How mpv may report the unload: aid/sid fall back to their option
	// values ("auto", false for "no") and the track list empties, in any
	// order around end-file. linger() is mpv taking its time (slow shutdown,
	// --idle=yes); each case relies on a different safeguard.
	unloads := map[string]func(f *fakeMpv, linger func()){
		"fallbacks before end-file": func(f *fakeMpv, linger func()) {
			f.prop("sid", false)
			f.prop("aid", "auto")
			f.prop("time-pos", nil)
			f.event("end-file", "reason", "eof")
			linger()
			f.prop("track-list", []Track{})
		},
		"fallbacks after end-file": func(f *fakeMpv, linger func()) {
			f.event("end-file", "reason", "eof")
			f.prop("sid", false)
			f.prop("aid", false)
			linger()
			f.prop("track-list", []Track{})
			f.event("shutdown")
		},
		"track list emptied first": func(f *fakeMpv, linger func()) {
			f.prop("track-list", []Track{})
			f.prop("sid", false)
			f.prop("aid", "auto")
			linger()
			f.event("end-file", "reason", "eof")
		},
		"user quits": func(f *fakeMpv, linger func()) {
			f.prop("sid", "auto")
			f.prop("aid", "auto")
			linger()
			f.event("end-file", "reason", "quit")
			f.prop("track-list", nil)
		},
	}
	for name, unload := range unloads {
		t.Run(name, func(t *testing.T) {
			m, l := newTestManager(t, nil)
			if err := m.Tracks.Save(saved); err != nil {
				t.Fatal(err)
			}
			if _, err := m.PlayMpv(PlayRequest{MediaID: 7, Episode: 1003, Source: "local", Target: "/shows/S01E03.mkv"}); err != nil {
				t.Fatal(err)
			}
			s, f := liveSession(t, m), l.last()

			// Nothing plays yet right after mpv starts.
			f.prop("aid", "auto")
			f.prop("sid", "auto")
			f.prop("track-list", []Track{})
			// mpv's own pick while loading; on file-loaded the saved choice
			// is restored (the fake echoes our set_property calls).
			f.prop("aid", 1)
			f.prop("sid", 3)
			f.prop("track-list", testTracks)
			f.event("file-loaded")
			waitFor(t, "the saved choice to be restored", func() bool { return f.received("show-text") })
			if !f.received("set_property sid") {
				t.Fatal("the saved subtitle choice was not applied")
			}
			f.prop("duration", 1420.0)
			f.prop("time-pos", 1300.0)
			time.Sleep(3 * m.trackGuard)

			f.prop("sid", 2) // the user picks the signs track
			waitFor(t, "the user's pick to be saved", func() bool {
				p := m.Tracks.Get(7)
				return p != nil && p.SubTitle == "Signs & Songs"
			})

			unload(f, func() { time.Sleep(3 * m.trackSettle) })
			f.exit()
			waitLoopDone(t, s)

			got := m.Tracks.Get(7)
			if got == nil {
				t.Fatal("track prefs were deleted")
			}
			got.UpdatedAt = 0
			if *got != want {
				t.Errorf("after the episode ended the saved choice is %+v; want the user's %+v", *got, want)
			}
		})
	}
}

func TestPlayMpvSerializesSessions(t *testing.T) {
	m, l := newTestManager(t, nil)
	l.delay = 30 * time.Millisecond // like waiting for mpv's socket

	var wg sync.WaitGroup
	for i := range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := m.PlayMpv(PlayRequest{MediaID: 1, Episode: 1001 + i, Source: "local", Target: fmt.Sprintf("/shows/S01E%02d.mkv", i+1)})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	launched, live, maxLive := l.stats()
	if launched != 3 || live != 1 || maxLive != 1 {
		t.Fatalf("launched=%d live=%d maxLive=%d; want 3 launches and never more than one mpv", launched, live, maxLive)
	}
	l.mu.Lock()
	lastTarget := l.targets[len(l.targets)-1]
	fakes := slices.Clone(l.launched)
	l.mu.Unlock()
	if st := m.Status(); st == nil || st.Target != lastTarget {
		t.Fatalf("current session = %+v; want the last one launched (%s)", st, lastTarget)
	}
	for i, f := range fakes[:len(fakes)-1] {
		if !f.received("quit") {
			t.Errorf("mpv #%d was not asked to quit", i+1)
		}
	}
}

func TestSessionCopiesDontRace(t *testing.T) {
	m, l := newTestManager(t, nil)
	sess, err := m.PlayMpv(PlayRequest{MediaID: 1, Episode: 1001, Source: "local", Target: "/shows/S01E01.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	f := l.last()
	waitFor(t, "the properties to be observed", func() bool { return f.count("observe_property") == len(observed) })
	go func() {
		for i := range 300 {
			f.prop("time-pos", float64(i+1))
			if i%50 == 0 {
				f.prop("track-list", testTracks)
			}
		}
	}()
	// What a handler does with the result (JSON-encode it) while mpv
	// reports. Nothing here synchronizes with the session loop, so the race
	// detector sees any write to the returned session.
	for end := time.Now().Add(300 * time.Millisecond); time.Now().Before(end); {
		if _, err := json.Marshal(sess); err != nil {
			t.Fatal(err)
		}
	}
	if sess.Position != 0 || sess.Tracks != nil {
		t.Errorf("the returned session changed after PlayMpv returned: %+v", sess)
	}
	waitFor(t, "the position to reach 300", func() bool {
		st := m.Status()
		return st != nil && st.Position == 300 && len(st.Tracks) == len(testTracks)
	})
}

func TestAutoNextPlaysNextEpisode(t *testing.T) {
	m, l := newTestManager(t, func(c *config.Settings) { c.Playback.AutoPlayNext = true })
	m.NextResolver = func(_ context.Context, s *Session) (*PlayRequest, error) {
		return &PlayRequest{MediaID: s.MediaID, Episode: s.Episode + 1, Source: "local", Target: "/shows/S01E02.mkv"}, nil
	}
	if _, err := m.PlayMpv(PlayRequest{MediaID: 4, Episode: 1001, Source: "local", Target: "/shows/S01E01.mkv"}); err != nil {
		t.Fatal(err)
	}
	f := l.last()
	f.prop("duration", 1420.0)
	f.prop("time-pos", 1419.0)
	// mpv sends end-file and exits right away.
	f.event("end-file", "reason", "eof")
	f.exit()

	waitFor(t, "S01E02 to play", func() bool {
		st := m.Status()
		return st != nil && st.Episode == 1002
	})
	if e := m.history.Get(4, 1001); e == nil || e.Position != e.Duration || e.Duration != 1420 {
		t.Errorf("S01E01 history = %+v; want it marked finished", e)
	}
	if _, live, maxLive := l.stats(); live != 1 || maxLive != 1 {
		t.Errorf("live=%d maxLive=%d; want a single mpv", live, maxLive)
	}
}

func TestAutoNextYields(t *testing.T) {
	cases := map[string]func(t *testing.T, m *Manager) (wantID string){
		"to a newer session": func(t *testing.T, m *Manager) string {
			other, err := m.PlayMpv(PlayRequest{MediaID: 9, Episode: 1005, Source: "local", Target: "/other/S01E05.mkv"})
			if err != nil {
				t.Fatal(err)
			}
			return other.ID
		},
		"to Stop": func(t *testing.T, m *Manager) string {
			m.Stop()
			return ""
		},
		"to the stop command": func(t *testing.T, m *Manager) string {
			_ = m.Command("stop", 0) // nothing plays anymore, but the stop still counts
			return ""
		},
	}
	for name, intervene := range cases {
		t.Run(name, func(t *testing.T) {
			m, l := newTestManager(t, func(c *config.Settings) { c.Playback.AutoPlayNext = true })
			resolving, release := make(chan struct{}), make(chan struct{})
			m.NextResolver = func(_ context.Context, s *Session) (*PlayRequest, error) {
				close(resolving)
				<-release
				return &PlayRequest{MediaID: s.MediaID, Episode: s.Episode + 1, Source: "local", Target: "/shows/S01E02.mkv"}, nil
			}
			if _, err := m.PlayMpv(PlayRequest{MediaID: 4, Episode: 1001, Source: "local", Target: "/shows/S01E01.mkv"}); err != nil {
				t.Fatal(err)
			}
			f := l.last()
			f.event("end-file", "reason", "eof")
			f.exit()
			select {
			case <-resolving:
			case <-time.After(5 * time.Second):
				t.Fatal("auto play next did not start")
			}

			wantID := intervene(t, m)
			close(release)
			time.Sleep(200 * time.Millisecond) // let a wrong auto play next happen

			st := m.Status()
			if (wantID == "" && st != nil) || (wantID != "" && (st == nil || st.ID != wantID)) {
				t.Fatalf("playing %+v; auto play next must not replace what the user did", st)
			}
			l.mu.Lock()
			launchedNext := slices.Contains(l.targets, "/shows/S01E02.mkv")
			l.mu.Unlock()
			if launchedNext {
				t.Fatal("auto play next launched S01E02")
			}
		})
	}
}

func TestBuiltinRewatchUpdatesProgressAgain(t *testing.T) {
	m, _ := newTestManager(t, nil)
	var updates atomic.Int32
	m.viewed = func(mediaID, episode int) {
		if mediaID == 3 && episode == 1002 {
			updates.Add(1)
		}
	}
	watch := func() {
		m.ResetBuiltinProgress(3, 1002, 0) // the in-app player opens the episode
		for _, pos := range []float64{600, 1250, 1300, 1390} {
			m.ReportProgress(ProgressReport{MediaID: 3, Episode: 1002, Position: pos, Duration: 1400, Source: "local"})
		}
		m.ReportProgress(ProgressReport{MediaID: 3, Episode: 1002, Position: 1400, Duration: 1400, Source: "local", Ended: true})
	}

	watch()
	waitFor(t, "the progress update", func() bool { return updates.Load() == 1 })
	watch() // rewatch
	waitFor(t, "the rewatch's progress update", func() bool { return updates.Load() == 2 })
	time.Sleep(50 * time.Millisecond)
	if n := updates.Load(); n != 2 {
		t.Fatalf("%d progress updates; want one per viewing", n)
	}
	if e := m.history.Get(3, 1002); e == nil || e.Position != 1400 {
		t.Errorf("history = %+v; want the episode marked finished", e)
	}
}

// A file holding several episodes (S01E01E02) moves the list on to its last
// one, in mpv (at the completion threshold or when it ends) as in the in-app
// player.
func TestMultiEpisodeFileProgress(t *testing.T) {
	m, l := newTestManager(t, nil)
	var (
		mu     sync.Mutex
		viewed []string
	)
	m.viewed = func(mediaID, episode int) {
		mu.Lock()
		viewed = append(viewed, fmt.Sprintf("%d/%d", mediaID, episode))
		mu.Unlock()
	}
	got := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(viewed)
	}

	// mpv, past the completion threshold.
	if _, err := m.PlayMpv(PlayRequest{MediaID: 4, Episode: 1001, EpisodeEnd: 1002, Source: "local", Target: "/shows/S01E01E02.mkv"}); err != nil {
		t.Fatal(err)
	}
	f := l.last()
	f.prop("duration", 2800.0)
	f.prop("time-pos", 2790.0)
	waitFor(t, "the progress update", func() bool { return len(got()) == 1 })
	// mpv, ended before the threshold was reported.
	if _, err := m.PlayMpv(PlayRequest{MediaID: 4, Episode: 2003, EpisodeEnd: 2005, Source: "local", Target: "/shows/S02E03-E05.mkv"}); err != nil {
		t.Fatal(err)
	}
	f = l.last()
	f.event("end-file", "reason", "eof")
	f.exit()
	waitFor(t, "the progress update", func() bool { return len(got()) == 2 })
	// The in-app player.
	m.ResetBuiltinProgress(3, 1001, 1002)
	m.ReportProgress(ProgressReport{MediaID: 3, Episode: 1001, Position: 2790, Duration: 2800, Source: "local"})
	// An episode alone.
	m.ResetBuiltinProgress(3, 1003, 0)
	m.ReportProgress(ProgressReport{MediaID: 3, Episode: 1003, Position: 1400, Duration: 1400, Source: "local", Ended: true})

	if v := got(); !slices.Equal(v, []string{"4/1002", "4/2005", "3/1002", "3/1003"}) {
		t.Errorf("progress went to %v; want the last episode of each file", v)
	}
}

// The in-app player reports every few seconds from 3 s in: the show goes
// on the watching list once per viewing, from 10 s in.
func TestBuiltinStartsWatchingOncePerViewing(t *testing.T) {
	m, _ := newTestManager(t, nil)
	var (
		mu     sync.Mutex
		starts = map[episodeKey]int{}
	)
	m.started = func(mediaID, episode int) {
		mu.Lock()
		starts[episodeKey{mediaID, episode}]++
		mu.Unlock()
	}
	count := func(mediaID, episode int) int {
		mu.Lock()
		defer mu.Unlock()
		return starts[episodeKey{mediaID, episode}]
	}
	report := func(mediaID, episode int, positions ...float64) {
		for _, pos := range positions {
			m.ReportProgress(ProgressReport{MediaID: mediaID, Episode: episode, Position: pos, Duration: 1400, Source: "stream"})
		}
	}

	m.ResetBuiltinProgress(3, 1001, 0) // the in-app player opens the episode
	report(3, 1001, 3, 8)
	if n := count(3, 1001); n != 0 {
		t.Fatalf("%d starts 8 s in; want none before 10 s", n)
	}
	report(3, 1001, 13, 18, 600, 1250, 1300)
	m.ReportProgress(ProgressReport{MediaID: 3, Episode: 1001, Position: 1400, Duration: 1400, Source: "stream", Ended: true})
	if n := count(3, 1001); n != 1 {
		t.Fatalf("%d starts; want one per viewing", n)
	}

	m.ResetBuiltinProgress(3, 1001, 0) // watched again
	report(3, 1001, 4, 9, 14, 19)
	if n := count(3, 1001); n != 2 {
		t.Fatalf("%d starts after watching it again; want 2", n)
	}

	m.ResetBuiltinProgress(3, 1002, 0) // resumed halfway
	report(3, 1002, 700, 705)
	if n := count(3, 1002); n != 1 {
		t.Fatalf("%d starts of a resumed episode; want 1", n)
	}

	report(3, 0, 30, 60) // no episode (an extra)
	report(0, 1, 30, 60) // not matched to a movie or show
	if n := count(3, 0) + count(0, 1); n != 0 {
		t.Fatalf("%d starts without a movie or show episode", n)
	}
}

// What the in-app player's viewings did is forgotten a day after their
// last report, so it doesn't pile up with every episode watched; viewings
// still going are kept.
func TestBuiltinViewingsForgotten(t *testing.T) {
	m, _ := newTestManager(t, nil)
	m.started = func(int, int) {}
	view := func(mediaID, episode, end int) {
		m.ResetBuiltinProgress(mediaID, episode, end)
		m.ReportProgress(ProgressReport{MediaID: mediaID, Episode: episode, Position: 600, Duration: 1400, Source: "stream"})
	}
	view(3, 1001, 1002) // a two-episode file
	view(3, 1003, 0)
	view(4, 1, 0)
	// 1001's viewing was a day and more ago, 1003's an hour ago.
	m.builtinMu.Lock()
	m.builtinSeen[episodeKey{3, 1001}] = time.Now().Add(-builtinForgetAfter - time.Minute)
	m.builtinSeen[episodeKey{3, 1003}] = time.Now().Add(-time.Hour)
	m.builtinMu.Unlock()

	m.ResetBuiltinProgress(5, 1, 0) // a new viewing
	m.builtinMu.Lock()
	defer m.builtinMu.Unlock()
	old := episodeKey{3, 1001}
	if m.builtinStarted[old] || m.builtinEnd[old] != 0 || !m.builtinSeen[old].IsZero() {
		t.Error("the day-old viewing is remembered")
	}
	for _, k := range []episodeKey{{3, 1003}, {4, 1}} {
		if !m.builtinStarted[k] {
			t.Errorf("viewing %v forgotten", k)
		}
	}
	if len(m.builtinSeen) != 3 {
		t.Errorf("%d viewings remembered, want 3", len(m.builtinSeen))
	}
}

// mpv reports the position all the time: the title goes on the watching
// list once per viewing (mpv session), from 10 s in. A resumed session
// starts out at its resume position, which counts once mpv has loaded the
// file (it knows the duration), not before.
func TestMpvStartsWatchingOncePerViewing(t *testing.T) {
	m, l := newTestManager(t, nil)
	var (
		mu     sync.Mutex
		starts []string
	)
	m.started = func(mediaID, episode int) {
		st := m.Status()
		mu.Lock()
		defer mu.Unlock()
		starts = append(starts, fmt.Sprintf("%d/%d at %v of %v", mediaID, episode, st.Position, st.Duration))
	}
	got := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(starts)
	}
	at := func(pos float64) func() bool {
		return func() bool {
			st := m.Status()
			return st != nil && st.Position == pos
		}
	}

	if _, err := m.PlayMpv(PlayRequest{MediaID: 5, Episode: 1001, Source: "stream", Target: "https://cdn.example/1.m3u8"}); err != nil {
		t.Fatal(err)
	}
	f := l.last()
	f.prop("duration", 1420.0)
	for _, pos := range []float64{0.5, 4, 9.5} {
		f.prop("time-pos", pos)
	}
	waitFor(t, "the position to reach 9.5", at(9.5))
	if s := got(); len(s) != 0 {
		t.Fatalf("started before 10 s: %v", s)
	}
	for _, pos := range []float64{10, 15, 600, 1300, 1419} {
		f.prop("time-pos", pos)
	}
	waitFor(t, "the position to reach 1419", at(1419))
	if s := got(); !slices.Equal(s, []string{"5/1001 at 10 of 1420"}) {
		t.Fatalf("starts %v; want one, at 10 s", s)
	}

	resume := 142.0
	if _, err := m.PlayMpv(PlayRequest{MediaID: 5, Episode: 1001, Source: "stream", Target: "https://cdn.example/1.m3u8", Start: &resume}); err != nil {
		t.Fatal(err)
	}
	f = l.last()
	f.prop("pause", false) // mpv reports observed properties before loading the file
	f.prop("time-pos", nil)
	f.prop("duration", 1420.0)
	f.prop("time-pos", 142.0)
	f.prop("time-pos", 150.0)
	waitFor(t, "the position to reach 150", at(150))
	if s := got(); !slices.Equal(s, []string{"5/1001 at 10 of 1420", "5/1001 at 142 of 1420"}) {
		t.Fatalf("starts %v; want the resumed viewing to start once, after the file loaded", s)
	}
}

// chapterList is how mpv reports chapters (name and start).
func chapterList(chapters ...Chapter) []map[string]any {
	out := []map[string]any{}
	for _, c := range chapters {
		out = append(out, map[string]any{"title": c.Title, "time": c.Start})
	}
	return out
}

// told is what mpv was told to do: "<new position> <osd text>".
func told(f *fakeMpv) []string {
	var out []string
	seeks, texts := f.sent("set_property time-pos"), f.sent("show-text")
	for i := range min(len(seeks), len(texts)) {
		out = append(out, fmt.Sprint(seeks[i][0], " ", texts[i][0]))
	}
	return out
}

func TestMpvSkipsChaptersTheSettingsSay(t *testing.T) {
	episode := []Chapter{{"Previously On", 0, 0}, {"Opening", 50, 0}, {"Episode", 110, 0}, {"End Credits", 1300, 0}, {"Preview", 1390, 0}}
	movie := []Chapter{{"Chapter 1", 0, 0}, {"Chapter 2", 2500, 0}, {"End Credits", 5000, 0}, {"Post-Credits Scene", 5600, 0}}
	cases := []struct {
		name            string
		chapters        []Chapter
		duration        float64
		intro, credits  bool
		autoNext, other bool  // auto play next on; another episode to play
		visit           []int // the chapters played, in order
		want            []string
	}{
		{"recap and intro in one go", episode, 1420, true, false, false, false,
			[]int{0, 2, 3, 4}, []string{"110 Skipped recap and intro"}},
		{"resumed in the intro", episode, 1420, true, false, false, false,
			[]int{1, 2}, []string{"110 Skipped intro"}},
		{"going back plays it", episode, 1420, true, true, false, false,
			[]int{1, 2, 1, 2}, []string{"110 Skipped intro"}},
		// Credits and preview go on to the end: skipped only when the next
		// episode plays then.
		{"credits at the end, no auto play next", episode, 1420, false, true, false, true,
			[]int{2, 3, 4}, nil},
		{"credits at the end, auto play next", episode, 1420, false, true, true, true,
			[]int{2, 3}, []string{"1420 Skipped credits and preview"}},
		{"credits of the last episode", episode, 1420, false, true, true, false,
			[]int{2, 3, 4}, nil},
		{"settings off", episode, 1420, false, false, true, true,
			[]int{0, 1, 2, 3, 4}, nil},
		// The scene after the credits plays.
		{"credits before a post-credits scene", movie, 5700, true, true, false, false,
			[]int{0, 2, 3}, []string{"5600 Skipped credits"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, l := newTestManager(t, func(c *config.Settings) {
				c.Playback.SkipIntro, c.Playback.SkipCredits, c.Playback.AutoPlayNext = tc.intro, tc.credits, tc.autoNext
			})
			m.NextResolver = func(context.Context, *Session) (*PlayRequest, error) { return nil, nil }
			m.HasNext = func(s *Session) bool {
				if s.MediaID != 6 || s.Episode != 1002 {
					t.Errorf("asked about the episode after %d/%d", s.MediaID, s.Episode)
				}
				return tc.other
			}
			if _, err := m.PlayMpv(PlayRequest{MediaID: 6, Episode: 1002, Source: "local", Target: "/shows/S01E02.mkv"}); err != nil {
				t.Fatal(err)
			}
			f := l.last()
			f.prop("chapter-list", chapterList(tc.chapters...))
			f.prop("chapter", tc.visit[0])
			// Nothing is skipped before mpv knows the length (where the last
			// chapter ends).
			time.Sleep(50 * time.Millisecond)
			if got := told(f); len(got) != 0 {
				t.Fatalf("skipped %q before the length was known", got)
			}
			f.prop("duration", tc.duration)
			waitFor(t, "the skips", func() bool {
				st := m.Status()
				return st != nil && st.Duration == tc.duration && len(st.Skips) > 0
			})
			// A seek doesn't move to the next chapter here: the test does.
			for _, n := range tc.visit {
				f.prop("chapter", n)
				time.Sleep(20 * time.Millisecond)
			}
			waitFor(t, "the skips", func() bool { return len(told(f)) >= len(tc.want) })
			time.Sleep(100 * time.Millisecond) // a wrong skip would arrive by now
			if got := told(f); !slices.Equal(got, tc.want) {
				t.Errorf("mpv was told %q; want %q", got, tc.want)
			}
		})
	}
}

// The status lists the chapters' skips, for the Now Playing bar.
func TestMpvStatusHasChapterSkips(t *testing.T) {
	m, l := newTestManager(t, nil)
	if _, err := m.PlayMpv(PlayRequest{MediaID: 6, Episode: 1002, Source: "local", Target: "/shows/S01E02.mkv"}); err != nil {
		t.Fatal(err)
	}
	f := l.last()
	f.prop("duration", 1420.0)
	f.prop("chapter-list", chapterList(Chapter{"Intro", 0, 0}, Chapter{"Episode", 60, 0}, Chapter{"Credits", 1380, 0}))
	want := []SkipInterval{{"intro", 0, 60}, {"credits", 1380, 1420}}
	waitFor(t, "the skips", func() bool {
		st := m.Status()
		return st != nil && slices.Equal(st.Skips, want)
	})
	f.prop("chapter-list", nil) // no chapters (any more)
	waitFor(t, "no skips", func() bool {
		st := m.Status()
		return st != nil && len(st.Skips) == 0
	})
}

// Without a choice remembered for the title, the settings choose the tracks
// once the file is loaded.
func TestMpvChoosesTracksBySettings(t *testing.T) {
	cases := []struct {
		name      string
		edit      func(*config.Settings)
		aid, sid  any // set by Moviko; nil: not set
		wantNotes []string
	}{
		// Japanese audio (the file's first) isn't the user's language:
		// English subtitles, the full ones.
		{"foreign audio", nil, nil, 1.0, nil},
		// English audio: only its forced (signs) subtitles.
		{"preferred audio", func(c *config.Settings) { c.Playback.PreferredAudioLang = "eng,en" }, 2.0, 2.0, nil},
		{"the dub version", func(c *config.Settings) { c.OnlineStream.PreferDub = true }, 2.0, 2.0, []string{"Dub: English audio"}},
		// Subtitles whenever there are some in the preferred language.
		{"subtitles always", func(c *config.Settings) {
			c.Playback.SubtitlesForForeign, c.Playback.PreferredSubLang, c.Playback.PreferredAudioLang = false, "spa,es", "eng"
		}, 2.0, 3.0, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, l := newTestManager(t, tc.edit)
			if _, err := m.PlayMpv(PlayRequest{MediaID: 8, Episode: 1001, Source: "local", Target: "/shows/S01E01.mkv"}); err != nil {
				t.Fatal(err)
			}
			f := l.last()
			f.event("file-loaded")
			waitFor(t, "the tracks to be chosen", func() bool { return f.received("set_property sid") })
			time.Sleep(50 * time.Millisecond)
			last := func(cmd string) any {
				sent := f.sent(cmd)
				if len(sent) == 0 {
					return nil
				}
				return sent[len(sent)-1][0]
			}
			if aid, sid := last("set_property aid"), last("set_property sid"); aid != tc.aid || sid != tc.sid {
				t.Errorf("aid %v, sid %v; want %v, %v", aid, sid, tc.aid, tc.sid)
			}
			var notes []string
			for _, s := range f.sent("show-text") {
				notes = append(notes, fmt.Sprint(s[0]))
			}
			if !slices.Equal(notes, tc.wantNotes) {
				t.Errorf("mpv showed %q; want %q", notes, tc.wantNotes)
			}
		})
	}
}

func TestStatusHooksRunOneAtATime(t *testing.T) {
	m, _ := newTestManager(t, nil)
	var running, overlaps atomic.Int32
	var last atomic.Value
	m.OnStatus = []func(*Session){func(s *Session) {
		if running.Add(1) > 1 {
			overlaps.Add(1)
		}
		time.Sleep(time.Millisecond) // a slow hook (Discord IPC)
		last.Store(s.Position)
		running.Add(-1)
	}}
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.ReportProgress(ProgressReport{MediaID: 1, Episode: 1, Position: float64(10 + i), Duration: 1400})
		}()
	}
	wg.Wait()
	m.ReportProgress(ProgressReport{MediaID: 1, Episode: 1, Position: 999, Duration: 1400})
	waitFor(t, "the newest status to reach the hook", func() bool { return last.Load() == 999.0 })
	if n := overlaps.Load(); n != 0 {
		t.Fatalf("status hooks overlapped %d times", n)
	}
}

func TestProgressMessage(t *testing.T) {
	if got := progressMessage(catalog.ShowID(1396), 2005); got != "Progress updated — S02E05" {
		t.Errorf("show: %q", got)
	}
	if got := progressMessage(catalog.MovieID(155), catalog.MovieEpisode); got != "Marked as watched" {
		t.Errorf("movie: %q", got)
	}
}
