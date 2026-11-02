package extensions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/events"
)

// Nuvio providers
//
// A Nuvio repository is a manifest.json listing providers: either a JSON
// array of entries or an object with a "scrapers" array. Each entry points
// (filename, relative to the manifest) at a CommonJS file exporting
// getStreams(tmdbId, mediaType, season, episode). Moviko installs each
// provider as an extension of type TypeNuvioProvider (same table, same
// enable/disable/uninstall/logs/update flow as the others); the extension
// id is "nuvio." + the provider id, its ManifestURI the repository's
// manifest URL and PayloadURI the code's URL.

// TypeNuvioProvider is the extension type of Nuvio providers.
const TypeNuvioProvider = "nuvio-provider"

const (
	nuvioExtPrefix      = "nuvio."
	maxNuvioRepoSize    = 4 << 20
	maxNuvioCodeSize    = 8 << 20
	nuvioFetchTimeout   = 30 * time.Second
	nuvioDefaultVersion = "0"
)

// NuvioMeta is what an installed Nuvio provider's entry said beyond the
// common manifest fields (Manifest.Nuvio).
type NuvioMeta struct {
	// ID is the provider's id in its repository.
	ID string `json:"id"`
	// Repo is the repository's manifest URL, Filename the code's path in
	// it.
	Repo     string `json:"repo"`
	Filename string `json:"filename"`
	// SupportedTypes are "movie" and/or "tv".
	SupportedTypes  []string `json:"supportedTypes"`
	ContentLanguage []string `json:"contentLanguage,omitempty"`
	Formats         []string `json:"formats,omitempty"`
	Limited         bool     `json:"limited,omitempty"`
}

// NuvioEntry is a provider listed by a repository.
type NuvioEntry struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	Version         string   `json:"version"`
	Author          string   `json:"author"`
	SupportedTypes  []string `json:"supportedTypes"`
	Filename        string   `json:"filename"`
	Enabled         bool     `json:"enabled"`
	Logo            string   `json:"logo"`
	ContentLanguage []string `json:"contentLanguage"`
	Formats         []string `json:"formats"`
	Limited         bool     `json:"limited"`

	// Filled in by Moviko: the code's URL, the extension id it installs
	// as, and its installed state.
	URL              string `json:"url"`
	ExtensionID      string `json:"extensionId"`
	Installed        bool   `json:"installed"`
	InstalledVersion string `json:"installedVersion,omitempty"`
	HasUpdate        bool   `json:"hasUpdate"`
}

// NuvioRepo is a Nuvio provider repository.
type NuvioRepo struct {
	URL       string       `json:"url"`
	Name      string       `json:"name,omitempty"`
	Version   string       `json:"version,omitempty"`
	Providers []NuvioEntry `json:"providers"`
}

// Entry finds a provider of the repository by id.
func (r *NuvioRepo) Entry(id string) (NuvioEntry, bool) {
	for _, e := range r.Providers {
		if e.ID == id || e.ExtensionID == id {
			return e, true
		}
	}
	return NuvioEntry{}, false
}

var defaultNuvioFetcher = sync.OnceValue(NewFetcher)

// FetchNuvioRepo downloads and parses a Nuvio repository manifest (through
// the sandbox's fetcher: no LAN addresses).
func FetchNuvioRepo(ctx context.Context, repoURL string) (*NuvioRepo, error) {
	return fetchNuvioRepo(ctx, defaultNuvioFetcher(), repoURL)
}

func fetchNuvioRepo(ctx context.Context, f *Fetcher, repoURL string) (*NuvioRepo, error) {
	repoURL = strings.TrimSpace(repoURL)
	u, err := url.Parse(repoURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("the repository URL must start with https:// (it usually ends with manifest.json)")
	}
	raw, err := nuvioFetch(ctx, f, repoURL, maxNuvioRepoSize)
	if err != nil {
		return nil, fmt.Errorf("could not download the repository: %w", err)
	}
	return ParseNuvioRepo(repoURL, raw)
}

// ParseNuvioRepo parses a repository manifest downloaded from repoURL.
// Entries without code are skipped; ids, versions and URLs are
// normalized.
func ParseNuvioRepo(repoURL string, raw []byte) (*NuvioRepo, error) {
	base, err := url.Parse(repoURL)
	if err != nil {
		return nil, err
	}
	repo := &NuvioRepo{URL: repoURL, Providers: []NuvioEntry{}}
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		var obj struct {
			Name      string            `json:"name"`
			Version   json.RawMessage   `json:"version"`
			Scrapers  []json.RawMessage `json:"scrapers"`
			Providers []json.RawMessage `json:"providers"`
		}
		if err := json.Unmarshal(raw, &obj); err != nil || (obj.Scrapers == nil && obj.Providers == nil) {
			return nil, errors.New("this is not a Nuvio repository (expected a list of providers or {\"scrapers\": [...]})")
		}
		repo.Name, repo.Version = obj.Name, nuvioFlexString(obj.Version)
		entries = append(obj.Scrapers, obj.Providers...)
	}
	seen := map[string]bool{}
	for _, e := range entries {
		entry, ok := parseNuvioEntry(base, e)
		if !ok || seen[entry.ExtensionID] {
			continue
		}
		seen[entry.ExtensionID] = true
		repo.Providers = append(repo.Providers, entry)
	}
	return repo, nil
}

func parseNuvioEntry(base *url.URL, raw json.RawMessage) (NuvioEntry, bool) {
	var w struct {
		ID              string          `json:"id"`
		Name            string          `json:"name"`
		Description     string          `json:"description"`
		Version         json.RawMessage `json:"version"`
		Author          string          `json:"author"`
		SupportedTypes  json.RawMessage `json:"supportedTypes"`
		Filename        string          `json:"filename"`
		Enabled         *bool           `json:"enabled"`
		Logo            string          `json:"logo"`
		ContentLanguage json.RawMessage `json:"contentLanguage"`
		Formats         json.RawMessage `json:"formats"`
		Limited         bool            `json:"limited"`
	}
	if json.Unmarshal(raw, &w) != nil {
		return NuvioEntry{}, false
	}
	filename := strings.TrimSpace(w.Filename)
	if filename == "" {
		return NuvioEntry{}, false
	}
	ref, err := url.Parse(filename)
	if err != nil {
		return NuvioEntry{}, false
	}
	code := base.ResolveReference(ref)
	if code.Scheme != "http" && code.Scheme != "https" {
		return NuvioEntry{}, false
	}
	id := strings.TrimSpace(w.ID)
	if id == "" {
		id = strings.TrimSuffix(path.Base(code.Path), path.Ext(code.Path))
	}
	sid := sanitizeNuvioID(id)
	if sid == "" {
		return NuvioEntry{}, false
	}
	e := NuvioEntry{
		ID: id, Name: strings.TrimSpace(w.Name), Description: w.Description, Version: nuvioFlexString(w.Version),
		Author: w.Author, Filename: filename, Enabled: w.Enabled == nil || *w.Enabled, Logo: w.Logo,
		ContentLanguage: nuvioFlexStrings(w.ContentLanguage), Formats: nuvioFlexStrings(w.Formats), Limited: w.Limited,
		URL: code.String(), ExtensionID: nuvioExtPrefix + sid,
	}
	if e.Name == "" {
		e.Name = id
	}
	if e.Version == "" {
		e.Version = nuvioDefaultVersion
	}
	for _, t := range nuvioFlexStrings(w.SupportedTypes) {
		switch t = strings.ToLower(strings.TrimSpace(t)); t {
		case "movie", "movies":
			e.SupportedTypes = nuvioAppendUnique(e.SupportedTypes, "movie")
		case "tv", "series", "show", "shows":
			e.SupportedTypes = nuvioAppendUnique(e.SupportedTypes, "tv")
		}
	}
	if len(e.SupportedTypes) == 0 {
		e.SupportedTypes = []string{"movie", "tv"}
	}
	return e, true
}

var reNuvioIDChars = regexp.MustCompile(`[^a-z0-9_.-]+`)

// sanitizeNuvioID makes a provider id usable in an extension id (letters,
// digits, - _ .), "" when nothing is left.
func sanitizeNuvioID(id string) string {
	s := reNuvioIDChars.ReplaceAllString(strings.ToLower(strings.TrimSpace(id)), "-")
	s = strings.Trim(s, "-_.")
	if len(s) > 64-len(nuvioExtPrefix) {
		s = strings.Trim(s[:64-len(nuvioExtPrefix)], "-_.")
	}
	return s
}

func nuvioAppendUnique(list []string, s string) []string {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}

// nuvioFlexString reads a string or a number.
func nuvioFlexString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String()
	}
	return ""
}

// nuvioFlexStrings reads a string or an array of strings.
func nuvioFlexStrings(raw json.RawMessage) []string {
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		return list
	}
	if s := nuvioFlexString(raw); s != "" {
		return []string{s}
	}
	return nil
}

// nuvioFetch GETs a URL through f and returns the body, refusing bodies
// over limit bytes.
func nuvioFetch(ctx context.Context, f *Fetcher, u string, limit int) ([]byte, error) {
	resp, err := f.Do(ctx, fetchRequest{url: u, method: "GET", timeout: nuvioFetchTimeout, noBypass: true, limit: int64(limit)})
	if err != nil {
		return nil, err
	}
	if resp.status >= 400 {
		return nil, fmt.Errorf("%s answered %d %s", u, resp.status, resp.statusText)
	}
	return resp.body, nil
}

// ---------------------------------------------------------------------------
// Manager

// NuvioRepo downloads a repository and marks the providers already
// installed.
func (m *Manager) NuvioRepo(ctx context.Context, repoURL string) (*NuvioRepo, error) {
	repo, err := fetchNuvioRepo(ctx, m.fetcher, repoURL)
	if err != nil {
		return nil, err
	}
	m.mu.RLock()
	for i := range repo.Providers {
		e := &repo.Providers[i]
		if l, ok := m.exts[e.ExtensionID]; ok {
			e.Installed = true
			e.InstalledVersion = l.Manifest.Version
			e.HasUpdate = e.Version != l.Manifest.Version
		}
	}
	m.mu.RUnlock()
	return repo, nil
}

// InstallNuvio installs (or updates) the provider id of a repository.
func (m *Manager) InstallNuvio(ctx context.Context, repoURL, id string) (Info, error) {
	repo, err := fetchNuvioRepo(ctx, m.fetcher, repoURL)
	if err != nil {
		return Info{}, err
	}
	e, ok := repo.Entry(id)
	if !ok {
		return Info{}, fmt.Errorf("the repository has no provider %q", id)
	}
	return m.installNuvioEntry(ctx, repo, e)
}

// InstallNuvioRepo installs every provider a repository enables by default
// (and updates the ones installed already). It returns what it installed
// and the failures.
func (m *Manager) InstallNuvioRepo(ctx context.Context, repoURL string) ([]Info, error) {
	repo, err := fetchNuvioRepo(ctx, m.fetcher, repoURL)
	if err != nil {
		return nil, err
	}
	var out []Info
	var errs []error
	for _, e := range repo.Providers {
		_, installed := m.get(e.ExtensionID)
		if !e.Enabled && !installed {
			continue
		}
		inf, err := m.installNuvioEntry(ctx, repo, e)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", e.Name, err))
			continue
		}
		out = append(out, inf)
	}
	return out, errors.Join(errs...)
}

func (m *Manager) installNuvioEntry(ctx context.Context, repo *NuvioRepo, e NuvioEntry) (Info, error) {
	code, err := nuvioFetch(ctx, m.fetcher, e.URL, maxNuvioCodeSize)
	if err != nil {
		return Info{}, fmt.Errorf("could not download the provider: %w", err)
	}
	man := &Manifest{
		ID: e.ExtensionID, Name: e.Name, Version: e.Version, ManifestURI: repo.URL, Language: "javascript",
		Type: TypeNuvioProvider, Description: e.Description, Author: e.Author, Icon: e.Logo, PayloadURI: e.URL,
		Lang: nuvioLang(e.ContentLanguage),
		Nuvio: &NuvioMeta{
			ID: e.ID, Repo: repo.URL, Filename: e.Filename, SupportedTypes: e.SupportedTypes,
			ContentLanguage: e.ContentLanguage, Formats: e.Formats, Limited: e.Limited,
		},
	}
	if !reID.MatchString(man.ID) {
		return Info{}, fmt.Errorf("invalid provider id %q", e.ID)
	}
	if _, err := compileNuvio(man, string(code)); err != nil {
		return Info{}, fmt.Errorf("the provider's code doesn't compile: %w", err)
	}
	return m.installNuvioCode(man, string(code))
}

// installNuvioCode saves a provider and swaps it in, keeping the state of
// the version it replaces.
func (m *Manager) installNuvioCode(man *Manifest, code string) (Info, error) {
	old, exists := m.get(man.ID)
	l := &Loaded{mgr: m, Manifest: man, payload: code, Enabled: true}
	if exists {
		old.mu.Lock()
		l.Enabled, l.UserConfig, l.InstalledAt = old.Enabled, old.UserConfig, old.InstalledAt
		old.mu.Unlock()
	}
	if err := m.persist(l); err != nil {
		return Info{}, err
	}
	m.mu.Lock()
	m.exts[man.ID] = l
	m.mu.Unlock()
	if exists {
		old.retire()
	}
	m.hub.Publish(events.ExtensionsUpdate, nil)
	return m.info(l), nil
}

func nuvioLang(langs []string) string {
	switch len(langs) {
	case 0:
		return "en"
	case 1:
		return strings.ToLower(langs[0])
	}
	return "multi"
}

// nuvioUpdates checks the repositories of the installed Nuvio providers
// for new versions (each repository is downloaded once). Manager's
// CheckUpdates calls it.
func (m *Manager) nuvioUpdates(ctx context.Context) []UpdateInfo {
	byRepo := map[string][]*Loaded{}
	m.mu.RLock()
	for _, l := range m.exts {
		if l.Manifest.Type == TypeNuvioProvider && l.Manifest.ManifestURI != "" {
			byRepo[l.Manifest.ManifestURI] = append(byRepo[l.Manifest.ManifestURI], l)
		}
	}
	m.mu.RUnlock()
	var out []UpdateInfo
	var mu sync.Mutex
	var wg sync.WaitGroup
	for repoURL, list := range byRepo {
		wg.Add(1)
		go func(repoURL string, list []*Loaded) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			repo, err := fetchNuvioRepo(cctx, m.fetcher, repoURL)
			if err != nil {
				return
			}
			for _, l := range list {
				e, ok := repo.Entry(l.Manifest.ID)
				if !ok || e.Version == l.Manifest.Version {
					continue
				}
				mu.Lock()
				out = append(out, UpdateInfo{ID: l.Manifest.ID, Name: l.Manifest.Name, CurrentVersion: l.Manifest.Version, LatestVersion: e.Version})
				mu.Unlock()
			}
		}(repoURL, list)
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// updateNuvio reinstalls a Nuvio provider from its repository. Manager's
// Update calls it.
func (m *Manager) updateNuvio(ctx context.Context, l *Loaded) (Info, error) {
	repo, err := fetchNuvioRepo(ctx, m.fetcher, l.Manifest.ManifestURI)
	if err != nil {
		return Info{}, err
	}
	e, ok := repo.Entry(l.Manifest.ID)
	if !ok {
		return Info{}, errors.New("the provider is no longer in its repository")
	}
	return m.installNuvioEntry(ctx, repo, e)
}
