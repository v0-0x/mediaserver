package extensions

import (
	"context"
	"errors"
	"fmt"
	"math"
	"runtime/debug"
	"runtime/metrics"
	"strings"
	"sync"
	"time"
)

// Keeping extensions' memory in check
//
// Extensions are other people's code running in KumoBox's own process, and
// Go has no memory limit per JavaScript VM: one that builds a huge string or
// array, or reads a huge answer, would take KumoBox's memory (and, past the
// soft limit, its CPU, the garbage collector working flat out) with it.
// Three guards:
//
//   - fetch() reads answers up to a size (fetchLimit);
//   - at most maxRunningVMs extensions run at once, a call into a provider or
//     a runtime loading its code: a stream search asks every provider at
//     the same moment, and dozens of them each holding their VM and the
//     pages they read add up. The others wait their turn, or give up when
//     their caller does;
//   - while a call runs, a watchdog samples the Go heap every
//     memorySampleEvery: when it has grown by more than memoryBudget()
//     since the call started, the JavaScript is interrupted and the runtime
//     closed, so everything it holds is freed. The call fails with
//     ErrTooMuchMemory; a provider starts afresh on its next call, a plugin
//     stays stopped until it's turned on again.
//
// The heap is the whole process's, so what else KumoBox does during a call
// counts too: the budget (half the soft limit) is far above what a call
// normally makes the heap grow by, a few megabytes. Code an extension runs
// outside a call (a timer it left behind) isn't watched; one native
// operation (String.prototype.repeat with a huge count...) can't be
// interrupted halfway either, so the obvious ones are refused up front
// (allocationGuards).

// ErrTooMuchMemory is the error of a call the memory watchdog stopped.
var ErrTooMuchMemory = errors.New("the provider used too much memory and was stopped")

// errPluginMemory is what a plugin the watchdog stopped shows.
var errPluginMemory = errors.New("the plugin used too much memory and was stopped")

// errBusy is returned when a runtime couldn't get a turn to load its code.
var errBusy = errors.New("too many extensions are running at once: try again in a moment")

// ---------------------------------------------------------------------------
// Answers to fetch()

const (
	// maxFetchText bounds an HTML, JSON, JavaScript, XML or text answer,
	// which becomes a JavaScript string (twice its size when it isn't
	// ASCII) and often a parsed document on top: a few megabytes is far
	// more than the pages and APIs providers read.
	maxFetchText = 8 << 20
	// maxFetchBinary bounds any other answer (images, archives...).
	maxFetchBinary = 16 << 20
)

// fetchLimit is the most fetch() reads of an answer of type contentType.
func fetchLimit(contentType string) int64 {
	mt, _, _ := strings.Cut(strings.ToLower(contentType), ";")
	mt = strings.TrimSpace(mt)
	switch {
	case mt == "", strings.HasPrefix(mt, "text/"):
		return maxFetchText
	}
	for _, kind := range []string{"json", "javascript", "ecmascript", "xml", "html", "x-www-form-urlencoded", "mpegurl"} {
		if strings.Contains(mt, kind) {
			return maxFetchText
		}
	}
	return maxFetchBinary
}

// tooLarge is the error of an answer over limit bytes.
func tooLarge(u string, limit int64) error {
	return fmt.Errorf("%s is too large (over %d MB)", u, limit>>20)
}

// ---------------------------------------------------------------------------
// How many run at once

// maxRunningVMs is how many extensions may run at once (calls into
// providers, runtimes loading their code). A stream search asks the enabled
// providers together; most answer within seconds, so the others' wait is
// short, and a search over dozens of them no longer has them all in memory
// at the same moment.
const maxRunningVMs = 8

// vmSlots holds a token per extension running (tests make it smaller).
var vmSlots = make(chan struct{}, maxRunningVMs)

// acquireVM waits for a turn to run an extension, or for ctx to end.
// release gives the turn back (once, however many times it's called).
func acquireVM(ctx context.Context) (release func(), err error) {
	slots := vmSlots
	select {
	case slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	var once sync.Once
	return func() { once.Do(func() { <-slots }) }, nil
}

// ---------------------------------------------------------------------------
// The watchdog

// memorySampleEvery is how often the watchdog reads the heap's size (a
// cheap read: runtime/metrics doesn't stop the world).
var memorySampleEvery = 100 * time.Millisecond

// memoryBudgetForTests replaces memoryBudget's answer when set.
var memoryBudgetForTests uint64

const (
	// defaultSoftLimit is the server's soft memory limit when none is set
	// (cmd/kumobox's default, KUMOBOX_MEMORY_LIMIT_MB=0 turns it off).
	defaultSoftLimit = 256 << 20
	// minMemoryBudget keeps a tiny GOMEMLIMIT from stopping every call.
	minMemoryBudget = 32 << 20
)

// memoryBudget is how much the Go heap may grow during a call into an
// extension: half the soft memory limit (128 MB with KumoBox's 256), so a
// single call can't take KumoBox past it.
func memoryBudget() uint64 {
	if memoryBudgetForTests > 0 {
		return memoryBudgetForTests
	}
	limit := debug.SetMemoryLimit(-1) // reads it
	if limit <= 0 || limit == math.MaxInt64 {
		limit = defaultSoftLimit
	}
	return max(uint64(limit)/2, minMemoryBudget)
}

// heapMetric is the memory the heap's objects take, live ones and garbage
// not yet collected: what an extension building a huge value makes grow.
const heapMetric = "/memory/classes/heap/objects:bytes"

// heapBytes reads heapMetric.
func heapBytes() uint64 {
	s := []metrics.Sample{{Name: heapMetric}}
	metrics.Read(s)
	if s[0].Value.Kind() != metrics.KindUint64 {
		return 0
	}
	return s[0].Value.Uint64()
}

// memoryWatch is the watchdog of one call.
type memoryWatch struct {
	done chan struct{} // closed by stop
	// tripped is closed when the watchdog stopped the runtime.
	tripped  chan struct{}
	stopOnce sync.Once
}

// stop ends the watch.
func (w *memoryWatch) stop() { w.stopOnce.Do(func() { close(w.done) }) }

// watchMemory starts the watchdog of a call into the extension; the caller
// stops it when the call is over.
func (r *Runtime) watchMemory() *memoryWatch {
	w := &memoryWatch{done: make(chan struct{}), tripped: make(chan struct{})}
	start, budget, every := heapBytes(), memoryBudget(), memorySampleEvery
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-w.done:
				return
			case <-r.ctx.Done():
				return
			case <-t.C:
				if heap := heapBytes(); heap > start+budget {
					r.tooMuchMemory(heap-start, budget, w)
					return
				}
			}
		}
	}()
	return w
}

// tooMuchMemory stops the runtime whose call made the heap grow by grew
// bytes, over budget: the JavaScript running is interrupted at once (it
// stops allocating), the call fails, and the runtime is closed so that
// what it holds can be freed.
func (r *Runtime) tooMuchMemory(grew, budget uint64, w *memoryWatch) {
	if !r.memoryStopped.CompareAndSwap(false, true) {
		close(w.tripped)
		return
	}
	r.log("error", fmt.Sprintf("used too much memory (the heap grew by %d MB during a call, %d MB allowed): stopped", grew>>20, budget>>20))
	if vm := r.vm.Load(); vm != nil {
		vm.Interrupt(ErrTooMuchMemory)
	}
	close(w.tripped)
	r.Close()
	if r.loaded.Load() && r.onTooMuchMemory != nil {
		r.onTooMuchMemory(r)
	}
}

// ---------------------------------------------------------------------------
// Native allocations

// maxStringLength bounds (in characters) the strings repeat, padStart and
// padEnd build.
const maxStringLength = 32 << 20

// allocationGuards (a format: maxStringLength) refuses the strings
// String.prototype.repeat, padStart and padEnd would make over
// maxStringLength characters: each builds its result in one native call,
// which the watchdog can't interrupt halfway, so "x".repeat(1e10) would
// have KumoBox allocate gigabytes at once (or crash for lack of memory).
// V8 refuses strings past about 512 M characters the same way. The
// originals stay out of the extension's reach.
const allocationGuards = `
(function(max){
  'use strict'; // this stays what it was called on (null throws, as before)
  var proto = String.prototype;
  function guard(name, size){
    var orig = proto[name];
    Object.defineProperty(proto, name, { configurable: true, writable: true, value: function(){
      if (size(String(this), arguments[0]) > max) throw new RangeError('Invalid string length');
      return orig.apply(this, arguments);
    } });
  }
  guard('repeat', function(s, n){ return s.length * Math.max(0, Number(n) || 0); });
  guard('padStart', function(s, n){ return Number(n) || 0; });
  guard('padEnd', function(s, n){ return Number(n) || 0; });
})(%d)`

// callError is what a call into the extension returns for err: the
// watchdog's error once it stopped the runtime (whatever the interrupted
// JavaScript or the closing made of the call).
func (r *Runtime) callError(err error) error {
	if err != nil && r.memoryStopped.Load() {
		return ErrTooMuchMemory
	}
	return err
}
