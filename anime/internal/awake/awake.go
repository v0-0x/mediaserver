// Package awake keeps the computer from going to sleep while other devices
// (a TV, a phone) play its files: their stream would stop with it.
//
// Each stream holds it (Hold) while it's sent; when the last one ends, the
// computer may sleep again after a while (linger), so that a paused episode,
// or the next one starting, doesn't let it go at once.
package awake

import (
	"log"
	"sync"
	"time"
)

// What keeps the system awake, the system's own way (inhibit_*.go).
type inhibitor interface {
	// start keeps it awake until stop.
	start() error
	stop()
}

type Keeper struct {
	// Enabled reports whether to keep the computer awake (the setting).
	Enabled func() bool
	// Linger: how long it stays awake after the last stream.
	Linger time.Duration

	mu     sync.Mutex
	holds  int
	on     bool
	timer  *time.Timer
	impl   inhibitor
	failed bool
}

// New is a Keeper using the system's way to stay awake.
func New(enabled func() bool) *Keeper {
	return &Keeper{Enabled: enabled, Linger: 15 * time.Minute, impl: newInhibitor()}
}

// Hold keeps the computer awake until release is called (once).
func (k *Keeper) Hold() (release func()) {
	if k == nil || k.impl == nil || (k.Enabled != nil && !k.Enabled()) {
		return func() {}
	}
	k.mu.Lock()
	k.holds++
	if k.timer != nil {
		k.timer.Stop()
		k.timer = nil
	}
	if !k.on && !k.failed {
		if err := k.impl.start(); err != nil {
			// Said once: the system has no way (or no permission) to.
			k.failed = true
			log.Printf("can't keep this computer awake while other devices play its files: %v", err)
		} else {
			k.on = true
		}
	}
	k.mu.Unlock()
	var once sync.Once
	return func() { once.Do(k.release) }
}

func (k *Keeper) release() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.holds--
	if k.holds > 0 || !k.on {
		return
	}
	if k.timer != nil {
		k.timer.Stop()
	}
	k.timer = time.AfterFunc(k.Linger, func() {
		k.mu.Lock()
		defer k.mu.Unlock()
		k.timer = nil
		if k.holds == 0 && k.on {
			k.impl.stop()
			k.on = false
		}
	})
}

// Active reports whether it's keeping the computer awake now.
func (k *Keeper) Active() bool {
	if k == nil {
		return false
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.on
}

// Close lets the computer sleep again (Aniko is quitting).
func (k *Keeper) Close() {
	if k == nil {
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.timer != nil {
		k.timer.Stop()
		k.timer = nil
	}
	if k.on {
		k.impl.stop()
		k.on = false
	}
}

// funcs keeps the system awake with start and stop.
type funcs struct {
	startFn func() error
	stopFn  func()
}

func (f funcs) start() error { return f.startFn() }
func (f funcs) stop()        { f.stopFn() }

// WithFuncs is a Keeper that keeps the computer awake with start and stop
// (tests, or a system Aniko has no way for).
func WithFuncs(enabled func() bool, start func() error, stop func()) *Keeper {
	return &Keeper{Enabled: enabled, Linger: 15 * time.Minute, impl: funcs{start, stop}}
}
