package awake

import (
	"sync"
	"testing"
	"time"
)

type fake struct {
	mu            sync.Mutex
	starts, stops int
}

func (f *fake) start() error { f.mu.Lock(); f.starts++; f.mu.Unlock(); return nil }
func (f *fake) stop()        { f.mu.Lock(); f.stops++; f.mu.Unlock() }
func (f *fake) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.starts, f.stops
}

// Awake from the first stream to a while after the last; not at all when
// it's turned off.
func TestKeepsAwakeWhileStreaming(t *testing.T) {
	f := &fake{}
	on := true
	k := &Keeper{Enabled: func() bool { return on }, Linger: 150 * time.Millisecond, impl: f}
	a := k.Hold()
	b := k.Hold()
	if s, _ := f.counts(); s != 1 || !k.Active() {
		t.Fatalf("starts %d", s)
	}
	a()
	a() // once only
	b()
	time.Sleep(50 * time.Millisecond)
	if _, st := f.counts(); st != 0 || !k.Active() {
		t.Fatal("let go before the linger")
	}
	// Another stream within the linger: still the same hold.
	c := k.Hold()
	time.Sleep(200 * time.Millisecond)
	if s, st := f.counts(); s != 1 || st != 0 {
		t.Fatalf("starts %d stops %d while streaming", s, st)
	}
	c()
	time.Sleep(300 * time.Millisecond)
	if _, st := f.counts(); st != 1 || k.Active() {
		t.Fatalf("stops %d after the linger", st)
	}
	on = false
	k.Hold()()
	if s, _ := f.counts(); s != 1 {
		t.Error("kept awake while turned off")
	}
	on = true
	k.Hold()
	k.Close()
	if _, st := f.counts(); st != 2 || k.Active() {
		t.Error("not let go at Close")
	}
	var nothing *Keeper
	nothing.Hold()() // no Keeper: nothing
}
