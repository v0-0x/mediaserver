package extensions

import (
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/events"
)

// Unloading idle providers
//
// A provider's runtime (torrent and online streaming extensions, Nuvio
// providers) holds its compiled code and everything its JavaScript set up:
// a few megabytes each, for as long as Moviko runs, while most are only
// used when streams or torrents are looked for. A runtime nobody used for
// defaultIdleAfter is closed; the next call starts it again (Loaded.Runtime),
// with the same $store and $storage. Plugins keep running: their UI and hooks
// need them.

// defaultIdleAfter is how long an unused provider's runtime is kept.
const defaultIdleAfter = 5 * time.Minute

// use marks the runtime busy with a call into the extension (never
// unloaded meanwhile); the returned function ends it.
func (r *Runtime) use() (done func()) {
	r.calls.Add(1)
	r.touch()
	return func() {
		r.touch()
		r.calls.Add(-1)
	}
}

// touch records that the runtime was just used.
func (r *Runtime) touch() { r.lastUsed.Store(time.Now().UnixNano()) }

// idle reports whether the runtime has been unused for after (no call in
// progress, none started or ended since).
func (r *Runtime) idle(now time.Time, after time.Duration) bool {
	return r.calls.Load() == 0 && now.Sub(time.Unix(0, r.lastUsed.Load())) >= after
}

// unloadIfIdle closes the provider's runtime when it has been unused for
// after. Its console output is kept for the Logs view (Manager.Logs).
func (l *Loaded) unloadIfIdle(now time.Time, after time.Duration) bool {
	l.mu.Lock()
	rt := l.rt
	if rt == nil || !rt.idle(now, after) {
		l.mu.Unlock()
		return false
	}
	l.rt = nil
	l.lastLogs = joinLogs(l.lastLogs, rt.Logs())
	l.mu.Unlock()
	rt.Close()
	return true
}

// joinLogs is the console output of earlier runtimes followed by the
// current one's, the last maxLogLines lines.
func joinLogs(earlier, current []LogLine) []LogLine {
	out := append(append([]LogLine(nil), earlier...), current...)
	if len(out) > maxLogLines {
		out = out[len(out)-maxLogLines:]
	}
	return out
}

// startUnloading starts the loop that unloads idle providers (once, when
// the first provider runtime starts).
func (m *Manager) startUnloading() {
	if m.idleAfter <= 0 {
		return
	}
	m.unloading.Do(func() { go m.unloadLoop() })
}

func (m *Manager) unloadLoop() {
	// Checked a few times per period: a runtime goes at most a quarter
	// period late.
	every := min(max(m.idleAfter/4, 10*time.Millisecond), time.Minute)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-m.quit:
			return
		case now := <-t.C:
			m.unloadIdle(now)
		}
	}
}

// unloadIdle closes the provider runtimes unused for idleAfter and returns
// how many.
func (m *Manager) unloadIdle(now time.Time) int {
	m.mu.RLock()
	list := make([]*Loaded, 0, len(m.exts))
	for _, l := range m.exts {
		list = append(list, l)
	}
	m.mu.RUnlock()
	n := 0
	for _, l := range list {
		if l.unloadIfIdle(now, m.idleAfter) {
			n++
		}
	}
	if n > 0 {
		m.hub.Publish(events.ExtensionsUpdate, nil) // no longer "Running"
	}
	return n
}
