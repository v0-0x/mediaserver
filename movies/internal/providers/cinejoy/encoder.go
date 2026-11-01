package cinejoy

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
)

// Cinejoy's resolver only takes sealed requests: its encoder, crush.wasm,
// encrypts each one for the resolver's key with an ephemeral key pair, and
// hands back the key the answer is encrypted with. The encoder needs nothing
// from its host (no imports): it's given the request and 44 random bytes,
// and writes the sealed request into its own memory:
//
//	responseKey (32) | keyID (1) | ephemeralPublic (65) | body
//
// body is what is POSTed to the resolver.

const (
	// EncoderSHA256 is the encoder build KumoBox runs (luffy's pin). A
	// changed encoder is refused until it has been looked at.
	EncoderSHA256 = "b706f1f2d21ef20d49758672dbdf3559b0d0916bb19850b0ef04991918eea73c"

	sealTimeout = 5 * time.Second
	// encoderMaxSize bounds the encoder download.
	encoderMaxSize = 4 << 20
	// encoderMemoryPages bounds the encoder's memory (64 KiB pages): 32 MiB.
	encoderMemoryPages = 512
	headerSize         = 98
	seedSize           = 44
)

// sealed is a sealed request.
type sealed struct {
	responseKey     []byte
	keyID           byte
	ephemeralPublic []byte
	body            []byte
}

func splitSealed(b []byte) (*sealed, error) {
	if len(b) <= headerSize {
		return nil, errors.New("encoder: sealed request too short")
	}
	return &sealed{responseKey: b[:32], keyID: b[32], ephemeralPublic: b[33:headerSize], body: b[headerSize:]}, nil
}

// aad is the additional data the resolver's answer is authenticated with.
func (s *sealed) aad() []byte {
	aad := make([]byte, 0, 16+len(s.ephemeralPublic))
	aad = append(aad, "lumen-gate-v2"...)
	aad = append(aad, 0, 2, s.keyID)
	return append(aad, s.ephemeralPublic...)
}

// encoder is a compiled crush.wasm. Each request runs in an instance of its
// own (with fresh memory), closed when the request is sealed.
type encoder struct {
	rt       wazero.Runtime
	compiled wazero.CompiledModule
}

// Compiled encoders by SHA-256: compiling takes a while, the encoder rarely
// changes, and providers are created anew for every search.
var (
	encodersMu sync.Mutex
	encoders   = map[string]*encoder{}
)

// cachedEncoder returns the encoder compiled from the build with the hash
// sum, nil when there's none yet.
func cachedEncoder(sum string) *encoder {
	encodersMu.Lock()
	defer encodersMu.Unlock()
	return encoders[sum]
}

// loadEncoder compiles wasm, after checking it's the build with the hash
// sum (lowercase hex) and needs nothing from its host.
func loadEncoder(ctx context.Context, wasm []byte, sum string) (*encoder, error) {
	got := sha256.Sum256(wasm)
	if hex.EncodeToString(got[:]) != sum {
		return nil, errors.New("encoder changed upstream: refused until KumoBox is updated")
	}
	encodersMu.Lock()
	defer encodersMu.Unlock()
	if e := encoders[sum]; e != nil {
		return e, nil
	}
	cfg := wazero.NewRuntimeConfig().WithCloseOnContextDone(true).WithMemoryLimitPages(encoderMemoryPages)
	rt := wazero.NewRuntimeWithConfig(context.Background(), cfg)
	compiled, err := rt.CompileModule(ctx, wasm)
	if err == nil {
		err = checkEncoder(compiled)
	}
	if err != nil {
		_ = rt.Close(context.Background())
		return nil, err
	}
	e := &encoder{rt: rt, compiled: compiled}
	encoders[sum] = e
	return e, nil
}

// checkEncoder makes sure the module can't reach anything but its own
// memory, and has what sealing needs.
func checkEncoder(m wazero.CompiledModule) error {
	if len(m.ImportedFunctions()) > 0 || len(m.ImportedMemories()) > 0 {
		return errors.New("encoder: wants functions or memory from its host, refused")
	}
	if len(m.ExportedMemories()) == 0 {
		return errors.New("encoder: no memory")
	}
	fns := m.ExportedFunctions()
	for _, name := range []string{"alloc", "seal_request"} {
		if _, ok := fns[name]; !ok {
			return fmt.Errorf("encoder: no %s function", name)
		}
	}
	return nil
}

// seal seals a request for path with payload.
func (e *encoder) seal(ctx context.Context, path string, payload map[string]any) (*sealed, error) {
	body, err := json.Marshal(map[string]any{"path": path, "payload": payload})
	if err != nil {
		return nil, err
	}
	seed := make([]byte, seedSize)
	if _, err := rand.Read(seed); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, sealTimeout)
	defer cancel()
	// No name: instances of the same module run side by side.
	mod, err := e.rt.InstantiateModule(ctx, e.compiled, wazero.NewModuleConfig().WithName("").WithStartFunctions())
	if err != nil {
		return nil, fmt.Errorf("encoder: %w", err)
	}
	defer mod.Close(context.Background())

	alloc, sealFn, mem := mod.ExportedFunction("alloc"), mod.ExportedFunction("seal_request"), mod.Memory()
	if alloc == nil || sealFn == nil || mem == nil {
		return nil, errors.New("encoder: missing exports")
	}
	capacity := uint64(len(body) + 512)
	ptr := func(n uint64) (uint64, error) {
		res, err := alloc.Call(ctx, n)
		if err != nil {
			return 0, fmt.Errorf("encoder: %w", err)
		}
		if len(res) == 0 || res[0] == 0 {
			return 0, errors.New("encoder: out of memory")
		}
		return res[0], nil
	}
	in, err := ptr(uint64(len(body)))
	if err != nil {
		return nil, err
	}
	sd, err := ptr(seedSize)
	if err != nil {
		return nil, err
	}
	out, err := ptr(capacity)
	if err != nil {
		return nil, err
	}
	if !mem.Write(uint32(in), body) || !mem.Write(uint32(sd), seed) {
		return nil, errors.New("encoder: memory write out of range")
	}
	res, err := sealFn.Call(ctx, in, uint64(len(body)), sd, seedSize, out, capacity)
	if err != nil {
		return nil, fmt.Errorf("encoder: %w", err)
	}
	if len(res) == 0 {
		return nil, errors.New("encoder: no result")
	}
	n := uint64(uint32(res[0]))
	if n <= headerSize || n > capacity {
		return nil, errors.New("encoder: sealed request of a wrong size")
	}
	b, ok := mem.Read(uint32(out), uint32(n))
	if !ok {
		return nil, errors.New("encoder: memory read out of range")
	}
	// mem.Read is a view of the instance's memory, gone once it's closed.
	return splitSealed(append([]byte(nil), b...))
}
