package extensions

import (
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"sync"
	"time"

	"github.com/dop251/goja"
	"github.com/evanw/esbuild/pkg/api"
)

// Nuvio providers run in the same sandbox as the other extensions (same
// fetch, which can't reach this computer or the LAN, same limits and
// timeouts), with the CommonJS/React Native environment they're written
// for on top (see nuvioPrelude).

// nuvioCallTimeout bounds one getStreams() call (callers usually give it
// less through their context).
const nuvioCallTimeout = 90 * time.Second

// compileNuvio compiles a provider's code. Nuvio providers are CommonJS
// scripts, which goja runs as they are; code using ES module syntax (or
// syntax goja lacks) goes through esbuild first.
func compileNuvio(man *Manifest, code string) (*goja.Program, error) {
	prog, err := goja.Compile(man.ID+".js", code, false)
	if err == nil {
		return prog, nil
	}
	res := api.Transform(code, api.TransformOptions{
		Loader:    api.LoaderJS,
		Format:    api.FormatCommonJS,
		Target:    api.ES2017,
		Sourcemap: api.SourceMapNone,
	})
	if len(res.Errors) > 0 {
		return nil, err
	}
	if prog, err2 := goja.Compile(man.ID+".js", string(res.Code), false); err2 == nil {
		return prog, nil
	}
	return nil, err
}

// startNuvio starts a Nuvio provider's runtime (Manager.start calls it for
// TypeNuvioProvider extensions).
func (m *Manager) startNuvio(ctx context.Context, man *Manifest, payload string, onTooMuchMemory func(*Runtime)) (*Runtime, error) {
	prog, err := compileNuvio(man, payload)
	if err != nil {
		return nil, err
	}
	return NewRuntime(man, prog, RuntimeOptions{
		Context:         ctx,
		LoadTimeout:     m.loadTimeout,
		Store:           m.store(man.ID),
		Storage:         NewStorage(m.db, man.ID),
		Fetcher:         m.fetcher.Isolated(), // its own cookies, not shared across extensions
		Extra:           installNuvio,
		OnTooMuchMemory: onTooMuchMemory,
	})
}

// installNuvio sets the Nuvio environment up, before the provider's code
// runs.
func installNuvio(r *Runtime, vm *goja.Runtime) error {
	installNuvioCrypto(vm)
	setup, err := vm.RunString(nuvioPrelude)
	if err != nil {
		return fmt.Errorf("nuvio prelude: %w", err)
	}
	fn, ok := goja.AssertFunction(setup)
	if !ok {
		return errors.New("nuvio prelude is not a function")
	}
	origRequire := vm.Get("require")
	if origRequire == nil {
		origRequire = goja.Undefined()
	}
	_, err = fn(goja.Undefined(), vm.GlobalObject(), newCheerio(vm), origRequire)
	return jsError(err)
}

// installNuvioCrypto adds what Nuvio providers use of crypto-js beyond the
// sandbox's AES and encoders: hashes and HMACs, and the mode/padding
// constants (AES stays CBC with PKCS#7 whatever they say).
func installNuvioCrypto(vm *goja.Runtime) {
	c, ok := vm.Get("CryptoJS").(*goja.Object)
	if !ok {
		return
	}
	encOf := func(v goja.Value) goja.Callable {
		o, ok := v.(*goja.Object)
		if !ok {
			return nil
		}
		fn, _ := goja.AssertFunction(o.Get("stringify"))
		return fn
	}
	// A hash's result, like CryptoJS's WordArray: toString() is hex,
	// toString(CryptoJS.enc.Base64) base64...
	wordArray := func(b []byte) goja.Value {
		o := vm.NewObject()
		u8 := bytesToJS(vm, b)
		_ = o.Set("__bytes", u8)
		_ = o.Set("sigBytes", len(b))
		words := make([]any, 0, (len(b)+3)/4)
		for i := 0; i < len(b); i += 4 {
			var w uint32
			for j := 0; j < 4; j++ {
				w <<= 8
				if i+j < len(b) {
					w |= uint32(b[i+j])
				}
			}
			words = append(words, int32(w))
		}
		_ = o.Set("words", vm.NewArray(words...))
		_ = o.Set("toString", func(call goja.FunctionCall) goja.Value {
			if enc := encOf(call.Argument(0)); enc != nil {
				s, err := enc(goja.Undefined(), u8)
				if err != nil {
					panic(err)
				}
				return s
			}
			return vm.ToValue(hex.EncodeToString(b))
		})
		return o
	}
	input := func(v goja.Value) []byte {
		if o, ok := v.(*goja.Object); ok {
			if b := o.Get("__bytes"); b != nil && !goja.IsUndefined(b) {
				return valueToBytes(vm, b)
			}
		}
		return valueToBytes(vm, v)
	}
	hashes := map[string]func() hash.Hash{"MD5": md5.New, "SHA1": sha1.New, "SHA256": sha256.New, "SHA512": sha512.New}
	for name, h := range hashes {
		h := h
		_ = c.Set(name, func(call goja.FunctionCall) goja.Value {
			d := h()
			d.Write(input(call.Argument(0)))
			return wordArray(d.Sum(nil))
		})
		_ = c.Set("Hmac"+name, func(call goja.FunctionCall) goja.Value {
			mac := hmac.New(h, input(call.Argument(1)))
			mac.Write(input(call.Argument(0)))
			return wordArray(mac.Sum(nil))
		})
	}
	marker := func(names ...string) *goja.Object {
		o := vm.NewObject()
		for _, n := range names {
			_ = o.Set(n, map[string]any{"name": n})
		}
		return o
	}
	_ = c.Set("mode", marker("CBC", "ECB", "CFB", "OFB", "CTR"))
	_ = c.Set("pad", marker("Pkcs7", "NoPadding", "ZeroPadding", "Iso10126", "Iso97971", "AnsiX923"))
	// The sandbox's AES results don't honour the encoder passed to
	// toString(): give them CryptoJS's behaviour (toString(enc); base64 by
	// default for a ciphertext, hex for a plaintext).
	if aes, ok := c.Get("AES").(*goja.Object); ok {
		patch := func(name string, plaintext bool) {
			orig, ok := goja.AssertFunction(aes.Get(name))
			if !ok {
				return
			}
			_ = aes.Set(name, func(call goja.FunctionCall) goja.Value {
				res, err := orig(aes, call.Arguments...)
				if err != nil {
					panic(err)
				}
				o, ok := res.(*goja.Object)
				if !ok {
					return res
				}
				u8 := o.Get("__bytes")
				b := input(o)
				_ = o.Set("toString", func(call goja.FunctionCall) goja.Value {
					if enc := encOf(call.Argument(0)); enc != nil {
						s, err := enc(goja.Undefined(), u8)
						if err != nil {
							panic(err)
						}
						return s
					}
					if plaintext {
						return vm.ToValue(hex.EncodeToString(b))
					}
					return vm.ToValue(base64.StdEncoding.EncodeToString(b))
				})
				if !plaintext {
					_ = o.Set("ciphertext", wordArray(b))
				}
				return o
			})
		}
		patch("encrypt", false)
		patch("decrypt", true)
	}
	// The encoders also take hash results.
	if enc, ok := c.Get("enc").(*goja.Object); ok {
		for _, k := range enc.Keys() {
			e, ok := enc.Get(k).(*goja.Object)
			if !ok {
				continue
			}
			orig, ok := goja.AssertFunction(e.Get("stringify"))
			if !ok {
				continue
			}
			_ = e.Set("stringify", func(call goja.FunctionCall) goja.Value {
				v, err := orig(goja.Undefined(), bytesToJS(vm, input(call.Argument(0))))
				if err != nil {
					panic(err)
				}
				return v
			})
		}
	}
}

// callNuvio calls the provider's getStreams(tmdbId, mediaType, season,
// episode) and returns its result as JSON.
func (r *Runtime) callNuvio(ctx context.Context, tmdbID, mediaType string, season, episode int) (json.RawMessage, error) {
	defer r.use()()
	release, err := acquireVM(ctx) // its turn (memory.go)
	if err != nil {
		return nil, err
	}
	defer release()
	watch := r.watchMemory()
	defer watch.stop()
	type result struct {
		raw json.RawMessage
		err error
	}
	ch := make(chan result, 1)
	var once sync.Once
	send := func(raw json.RawMessage, err error) { once.Do(func() { ch <- result{raw, err} }) }
	ok := r.RunOnLoop(func(vm *goja.Runtime) {
		defer func() {
			if p := recover(); p != nil {
				send(nil, fmt.Errorf("panic: %v", p))
			}
		}()
		fn, this := nuvioEntry(vm)
		if fn == nil {
			send(nil, errors.New("the provider doesn't export getStreams()"))
			return
		}
		args := []goja.Value{vm.ToValue(tmdbID), vm.ToValue(mediaType), goja.Null(), goja.Null()}
		if mediaType == "tv" {
			args[2], args[3] = vm.ToValue(season), vm.ToValue(episode)
		}
		v, err := fn(this, args...)
		if err != nil {
			send(nil, jsError(err))
			return
		}
		r.awaitValue(vm, v, send)
	})
	if !ok {
		return nil, errors.New("the provider is not running")
	}
	timeout := time.NewTimer(nuvioCallTimeout)
	defer timeout.Stop()
	select {
	case res := <-ch:
		if err := r.callError(res.err); err != nil {
			r.log("error", "getStreams: "+err.Error())
			return nil, err
		}
		return res.raw, nil
	case <-ctx.Done():
		r.unstick()
		return nil, ctx.Err()
	case <-r.ctx.Done():
		return nil, r.callError(errStopped)
	case <-watch.tripped:
		return nil, ErrTooMuchMemory
	case <-timeout.C:
		r.unstick()
		return nil, errors.New("getStreams() timed out")
	}
}

// nuvioEntry finds getStreams: module.exports.getStreams (or exports.,
// module.exports.default., module.exports itself when it's the function),
// else a global function.
func nuvioEntry(vm *goja.Runtime) (goja.Callable, goja.Value) {
	try := func(v goja.Value) (goja.Callable, goja.Value) {
		o, ok := v.(*goja.Object)
		if !ok {
			return nil, nil
		}
		if fn, ok := goja.AssertFunction(o.Get("getStreams")); ok {
			return fn, o
		}
		if d, ok := o.Get("default").(*goja.Object); ok {
			if fn, ok := goja.AssertFunction(d.Get("getStreams")); ok {
				return fn, d
			}
		}
		return nil, nil
	}
	if mod, ok := vm.Get("module").(*goja.Object); ok {
		exp := mod.Get("exports")
		if fn, this := try(exp); fn != nil {
			return fn, this
		}
		if fn, ok := goja.AssertFunction(exp); ok {
			return fn, goja.Undefined()
		}
	}
	if fn, this := try(vm.Get("exports")); fn != nil {
		return fn, this
	}
	if fn, ok := goja.AssertFunction(vm.Get("getStreams")); ok {
		return fn, goja.Undefined()
	}
	return nil, nil
}
