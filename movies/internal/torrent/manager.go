package torrent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/v0-0x/mediaserver/movies/internal/config"
	"github.com/v0-0x/mediaserver/movies/internal/events"
	"github.com/v0-0x/mediaserver/movies/internal/util"
)

// Manager wires the torrent client and the search sources: the public
// sites and Torznab indexers of Settings › Torrent sources, and the torrent
// providers of extensions.
type Manager struct {
	settings *config.Store
	hub      *events.Hub

	// ExtraProviders returns the torrent providers of extensions.
	ExtraProviders func() []Provider

	srcMu    sync.Mutex
	built    []Source // BuildSources of the settings saved in builtKey
	builtKey string
	fixed    []Source // UseSources

	clientMu  sync.Mutex
	client    Client
	clientKey string
	ownClient Client // UseClient

	lastCount int
}

func NewManager(s *config.Store, hub *events.Hub) *Manager {
	return &Manager{settings: s, hub: hub, lastCount: -1}
}

// UseSources makes the manager search sources instead of those of the
// settings (extension providers still come on top); with none, those of
// the settings again. For tests.
func (m *Manager) UseSources(sources ...Source) {
	m.srcMu.Lock()
	m.fixed = nil
	if len(sources) > 0 {
		m.fixed = append([]Source{}, sources...)
	}
	m.srcMu.Unlock()
}

// UseClient makes the manager use c whatever the settings say. For tests.
func (m *Manager) UseClient(c Client) {
	m.clientMu.Lock()
	m.ownClient = c
	m.clientMu.Unlock()
}

// builtinSources are the sources of Settings › Torrent sources. They're
// built again only when those settings change, so a Torznab feed keeps
// what it learnt of its indexer's abilities meanwhile.
func (m *Manager) builtinSources() []Source {
	m.srcMu.Lock()
	defer m.srcMu.Unlock()
	if m.fixed != nil {
		return append([]Source{}, m.fixed...)
	}
	cfg := m.settings.Get().TorrentSources
	key, _ := json.Marshal(cfg)
	if m.built == nil || string(key) != m.builtKey {
		m.built, m.builtKey = BuildSources(cfg, nil), string(key)
	}
	return append([]Source{}, m.built...)
}

// Sources lists every search source: the indexers and public sites, then
// the extensions' providers. An extension whose id is taken gets "-ext".
func (m *Manager) Sources() []Source {
	out := m.builtinSources()
	if m.ExtraProviders == nil {
		return out
	}
	used := map[string]bool{}
	for _, s := range out {
		used[s.ID()] = true
	}
	for _, p := range m.ExtraProviders() {
		if p == nil {
			continue
		}
		id := p.ID()
		if used[id] {
			id += "-ext"
		}
		for n := 2; used[id]; n++ {
			id = fmt.Sprintf("%s-ext%d", p.ID(), n)
		}
		used[id] = true
		out = append(out, &extensionSource{id: id, p: p})
	}
	return out
}

// Source returns the source with that id, or nil.
func (m *Manager) Source(id string) Source { return SourceByID(m.Sources(), id) }

// ProviderInfo describes a search source for the UI. Kind is "torznab" (an
// indexer of the user's), "builtin" (a public site) or "extension".
// Supports lists the media types it finds ("movie", "tv").
type ProviderInfo struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Supports []string `json:"supports"`
	Kind     string   `json:"kind"`
}

// Providers lists the search sources.
func (m *Manager) Providers() []ProviderInfo {
	out := []ProviderInfo{}
	for _, s := range m.Sources() {
		info := ProviderInfo{ID: s.ID(), Name: s.Name(), Supports: []string{}, Kind: "builtin"}
		for _, t := range []string{MediaMovie, MediaTV} {
			if s.Supports(t) {
				info.Supports = append(info.Supports, t)
			}
		}
		switch s.(type) {
		case *Torznab:
			info.Kind = "torznab"
		case *extensionSource:
			info.Kind = "extension"
		}
		out = append(out, info)
	}
	return out
}

// AllSources is the provider id that searches every source.
const AllSources = "all"

// Search asks the source with the id provider (every source for "" or
// AllSources) for q, as SearchAll does: a single source's results are
// filtered and sorted the same way. It fails only for an unknown source or
// one that can't look for what q asks; the errors of the sources that
// failed come with the results.
func (m *Manager) Search(ctx context.Context, provider string, q MediaQuery) ([]*SearchResult, []error, error) {
	sources := m.Sources()
	if provider != "" && provider != AllSources {
		s := SourceByID(sources, provider)
		if s == nil {
			return nil, nil, fmt.Errorf("unknown torrent source %q — it may have been turned off in Settings › Torrent sources", provider)
		}
		if t := q.normalized().Type; !s.Supports(t) {
			what := map[string]string{MediaMovie: "movies", MediaTV: "shows", "": "free text"}[t]
			return nil, nil, fmt.Errorf("%s doesn't search %s — pick another source", s.Name(), what)
		}
		sources = []Source{s}
	}
	res, errs := SearchAll(ctx, sources, q)
	if res == nil {
		res = []*SearchResult{}
	}
	return res, errs, nil
}

// Magnet returns what to give the torrent client for r: see MagnetFor.
func (m *Manager) Magnet(ctx context.Context, r *SearchResult) (string, error) {
	return MagnetFor(ctx, m.Sources(), r)
}

// Client returns the configured torrent client (cached per settings).
func (m *Manager) Client() (Client, error) {
	m.clientMu.Lock()
	own := m.ownClient
	m.clientMu.Unlock()
	if own != nil {
		return own, nil
	}
	cfg := m.settings.Get()
	var key string
	var c Client
	switch cfg.Torrent.DefaultClient {
	case "qbittorrent":
		key = fmt.Sprintf("qb|%+v", cfg.Qbittorrent)
	case "transmission":
		key = fmt.Sprintf("tr|%+v", cfg.Transmission)
	default:
		return nil, ErrNoClient
	}
	m.clientMu.Lock()
	defer m.clientMu.Unlock()
	if m.client != nil && m.clientKey == key {
		return m.client, nil
	}
	if cfg.Torrent.DefaultClient == "qbittorrent" {
		c = NewQbittorrent(cfg.Qbittorrent)
	} else {
		c = NewTransmission(cfg.Transmission)
	}
	m.client, m.clientKey = c, key
	return c, nil
}

// Status reports whether the client is reachable.
type Status struct {
	Client    string `json:"client"`
	Connected bool   `json:"connected"`
	Version   string `json:"version"`
	Error     string `json:"error,omitempty"`
	// NeedsAuth: the client runs but refuses the configured credentials.
	NeedsAuth bool `json:"needsAuth,omitempty"`
}

func (m *Manager) Status(ctx context.Context) Status {
	c, err := m.Client()
	if err != nil {
		return Status{Client: "none", Error: err.Error()}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	v, err := c.Version(ctx)
	st := Status{Client: c.Name(), Version: v, Connected: err == nil}
	if err != nil {
		st.Error = err.Error()
		st.NeedsAuth = IsAuthError(err)
	}
	return st
}

// StartClient launches the client executable if it isn't reachable, then
// waits for it to come up.
func (m *Manager) StartClient(ctx context.Context) error {
	if st := m.Status(ctx); st.Connected {
		return nil
	} else if st.NeedsAuth {
		// It's running; launching it again would only bring its window up.
		return errors.New(st.Error)
	}
	cfg := m.settings.Get()
	exe := cfg.Qbittorrent.Executable
	if cfg.Torrent.DefaultClient == "transmission" {
		exe = cfg.Transmission.Executable
	}
	prog, args, err := splitCommand(exe)
	if err != nil {
		return err
	}
	if err := util.Detach(prog, args...); err != nil {
		return err
	}
	// Flatpak apps can take a while on their first start.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
		if st := m.Status(ctx); st.Connected {
			return nil
		} else if st.NeedsAuth {
			return errors.New(st.Error)
		}
	}
	return errors.New("the torrent client started but its Web UI/RPC is not reachable — check host/port/credentials")
}

// splitCommand splits the configured client executable into a program and
// its arguments. The program is the longest leading part that names an
// executable, so a path may contain spaces ("/opt/My Apps/qbittorrent"),
// with or without arguments ("flatpak run org.qbittorrent.qBittorrent").
func splitCommand(exe string) (string, []string, error) {
	exe = strings.TrimSpace(exe)
	if exe == "" {
		return "", nil, errors.New("no executable path configured for the torrent client")
	}
	for end := len(exe); end > 0; {
		if _, ok := util.LookPath(exe[:end]); ok {
			return exe[:end], strings.Fields(exe[end:]), nil
		}
		// Drop the last word.
		i := strings.LastIndexFunc(exe[:end], unicode.IsSpace)
		if i < 0 {
			break
		}
		end = len(strings.TrimRightFunc(exe[:i], unicode.IsSpace))
	}
	return "", nil, fmt.Errorf("torrent client executable not found: %s", exe)
}

// Add sends magnets/URLs to the client, starting it if needed.
func (m *Manager) Add(ctx context.Context, uris []string, savePath string) error {
	c, err := m.Client()
	if err != nil {
		return err
	}
	if st := m.Status(ctx); !st.Connected {
		if err := m.StartClient(ctx); err != nil {
			return err
		}
	}
	return c.Add(ctx, uris, savePath)
}

// RunCounter publishes the number of active torrents for the sidebar badge.
func (m *Manager) RunCounter(ctx context.Context) {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		cfg := m.settings.Get()
		if !cfg.Torrent.ShowActiveCount || cfg.Torrent.DefaultClient == "none" || cfg.Torrent.DefaultClient == "" {
			continue
		}
		c, err := m.Client()
		if err != nil {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		list, err := c.List(cctx)
		cancel()
		count := 0
		if err == nil {
			for _, t := range list {
				if t.State == "downloading" || t.State == "stalled" || t.State == "metadata" || t.State == "queued" {
					count++
				}
			}
		}
		if count != m.lastCount {
			m.lastCount = count
			m.hub.Publish(events.TorrentCount, count)
		}
	}
}
