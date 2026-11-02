package stremio

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/db"
	"github.com/v0-0x/mediaserver/movies/internal/providers"
)

// The table is created here rather than in db.go so this package owns its
// storage (and the table only exists once Stremio addons are in use).
const storeSchema = `CREATE TABLE IF NOT EXISTS stremio_addons(
	transport_url TEXT PRIMARY KEY,
	manifest TEXT NOT NULL,
	enabled INTEGER NOT NULL DEFAULT 1,
	position INTEGER NOT NULL DEFAULT 0,
	installed_at INTEGER NOT NULL
)`

// Errors of Store.
var (
	ErrUnsupportedAddon = errors.New("this addon has no streams or subtitles for movies or series (catalog and metadata addons are of no use to Moviko)")
	ErrAdultAddon       = errors.New("this addon is flagged as adult content: confirm to install it anyway")
	ErrNotInstalled     = errors.New("this addon is not installed")
)

// ConfigurationError is returned for addons that must be configured on
// their own page before they are installed.
type ConfigurationError struct {
	// ConfigureURL is the addon's configuration page.
	ConfigureURL string
}

func (e *ConfigurationError) Error() string {
	return "this addon must be configured first: open " + e.ConfigureURL + ", then install the URL it gives you"
}

// InstallOptions are the user's confirmations for addons that need one.
type InstallOptions struct {
	// AllowLocal installs an addon running on this computer or the LAN.
	AllowLocal bool `json:"allowLocal"`
	// AllowAdult installs an addon flagged as adult content.
	AllowAdult bool `json:"allowAdult"`
}

// Store keeps the installed addons in the database (table stremio_addons).
// Addons are identified by their transport URL; there's one addon per
// manifest id, as in Stremio (installing another configuration of an
// addon replaces it).
type Store struct {
	db     *db.DB
	client *Client
	// OnChange is called after each change (install, removal, reorder...).
	OnChange func()

	mu     sync.RWMutex
	addons []Addon // by position
}

// NewStore opens the store (c nil: DefaultClient).
func NewStore(d *db.DB, c *Client) (*Store, error) {
	if c == nil {
		c = DefaultClient
	}
	if _, err := d.Write(storeSchema); err != nil {
		return nil, fmt.Errorf("stremio addons table: %w", err)
	}
	s := &Store{db: d, client: c}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// Client is the store's addon client.
func (s *Store) Client() *Client { return s.client }

func (s *Store) load() error {
	rows, err := s.db.Query(`SELECT transport_url, manifest, enabled, position, installed_at FROM stremio_addons ORDER BY position, installed_at`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var list []Addon
	for rows.Next() {
		var a Addon
		var manifest string
		var enabled int
		if err := rows.Scan(&a.TransportURL, &manifest, &enabled, &a.Position, &a.InstalledAt); err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(manifest), &a.Manifest); err != nil {
			log.Printf("stremio: skipping addon %s: %v", a.TransportURL, err)
			continue
		}
		a.raw = json.RawMessage(manifest)
		a.Installed, a.Enabled = true, enabled == 1
		a.Local, a.Supported = IsLocalURL(a.TransportURL), a.Manifest.Useful()
		list = append(list, a)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	s.addons = list
	s.mu.Unlock()
	return nil
}

func (s *Store) changed() {
	if s.OnChange != nil {
		s.OnChange()
	}
}

// List returns the installed addons, in the user's order.
func (s *Store) List() []Addon {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Addon(nil), s.addons...)
}

// Get finds an installed addon by transport URL (as pasted or
// normalized), manifest id or provider id ("stremio:<id>").
func (s *Store) Get(key string) (Addon, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	i := s.indexLocked(key)
	if i < 0 {
		return Addon{}, false
	}
	return s.addons[i], true
}

func (s *Store) indexLocked(key string) int {
	key = strings.TrimSpace(key)
	norm, _ := NormalizeURL(key)
	id := strings.TrimPrefix(key, "stremio:")
	for i, a := range s.addons {
		if a.TransportURL == key || (norm != "" && a.TransportURL == norm) || a.Manifest.ID == id {
			return i
		}
	}
	return -1
}

// MarkInstalled sets Installed (and the installed state) on addons of a
// collection.
func (s *Store) MarkInstalled(list []Addon) []Addon {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range list {
		for _, a := range s.addons {
			if a.TransportURL == list[i].TransportURL || a.Manifest.ID == list[i].Manifest.ID {
				list[i].Installed, list[i].Enabled, list[i].Position, list[i].InstalledAt = true, a.Enabled, a.Position, a.InstalledAt
				break
			}
		}
	}
	return list
}

// Collection downloads an addon collection and marks the installed addons.
func (s *Store) Collection(ctx context.Context, rawURL string) ([]Addon, error) {
	list, err := s.client.FetchCollection(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	return s.MarkInstalled(list), nil
}

// Install fetches an addon's manifest and installs it (or updates it, when
// it's installed already). It refuses addons Moviko can't use (no streams
// or subtitles for movies or series), addons that must be configured
// first (*ConfigurationError), and, unless opts allows them, adult addons
// (ErrAdultAddon) and addons on this computer or the LAN (ErrLocalAddon).
func (s *Store) Install(ctx context.Context, rawURL string, opts InstallOptions) (Addon, error) {
	a, err := s.client.fetchManifest(ctx, rawURL, opts.AllowLocal)
	if err != nil {
		return Addon{}, err
	}
	if !a.Supported {
		return Addon{}, ErrUnsupportedAddon
	}
	if a.Manifest.BehaviorHints.Adult && !opts.AllowAdult {
		return Addon{}, ErrAdultAddon
	}
	if a.Manifest.BehaviorHints.ConfigurationRequired && !configured(a.TransportURL) {
		return Addon{}, &ConfigurationError{ConfigureURL: a.BaseURL() + "/configure"}
	}

	s.mu.Lock()
	a.Installed, a.Enabled, a.InstalledAt = true, true, time.Now().Unix()
	a.Position = len(s.addons)
	if n := len(s.addons); n > 0 {
		a.Position = s.addons[n-1].Position + 1
	}
	var replaced []string
	for _, old := range s.addons {
		if old.TransportURL == a.TransportURL || old.Manifest.ID == a.Manifest.ID {
			if len(replaced) == 0 {
				a.Position, a.InstalledAt = old.Position, old.InstalledAt
			}
			replaced = append(replaced, old.TransportURL)
		}
	}
	raw := a.raw
	if len(raw) == 0 {
		raw, _ = json.Marshal(a.Manifest)
	}
	err = s.db.Tx(func(tx *sql.Tx) error {
		for _, u := range replaced {
			if _, err := tx.Exec(`DELETE FROM stremio_addons WHERE transport_url = ?`, u); err != nil {
				return err
			}
		}
		_, err := tx.Exec(`INSERT INTO stremio_addons(transport_url, manifest, enabled, position, installed_at) VALUES(?, ?, 1, ?, ?)`,
			a.TransportURL, string(raw), a.Position, a.InstalledAt)
		return err
	})
	s.mu.Unlock()
	if err != nil {
		return Addon{}, err
	}
	if err := s.load(); err != nil {
		return Addon{}, err
	}
	s.changed()
	return a, nil
}

// configured reports whether a transport URL carries a configuration (a
// path before /manifest.json, which is how addons encode it).
func configured(transport string) bool {
	u, err := url.Parse(transport)
	if err != nil {
		return false
	}
	return strings.Trim(strings.TrimSuffix(u.EscapedPath(), "/manifest.json"), "/") != ""
}

// Uninstall removes an installed addon (key: see Get).
func (s *Store) Uninstall(key string) error {
	a, ok := s.Get(key)
	if !ok {
		return ErrNotInstalled
	}
	if _, err := s.db.Write(`DELETE FROM stremio_addons WHERE transport_url = ?`, a.TransportURL); err != nil {
		return err
	}
	if err := s.load(); err != nil {
		return err
	}
	s.changed()
	return nil
}

// SetEnabled turns an installed addon on or off.
func (s *Store) SetEnabled(key string, enabled bool) error {
	a, ok := s.Get(key)
	if !ok {
		return ErrNotInstalled
	}
	v := 0
	if enabled {
		v = 1
	}
	if _, err := s.db.Write(`UPDATE stremio_addons SET enabled = ? WHERE transport_url = ?`, v, a.TransportURL); err != nil {
		return err
	}
	if err := s.load(); err != nil {
		return err
	}
	s.changed()
	return nil
}

// Reorder puts the addons in the given order (keys: see Get); the ones not
// listed keep their relative order after them. Order matters: providers
// are asked, and their streams listed, in this order.
func (s *Store) Reorder(keys []string) error {
	s.mu.RLock()
	current := append([]Addon(nil), s.addons...)
	var order []string
	used := map[string]bool{}
	for _, k := range keys {
		if i := s.indexLocked(k); i >= 0 && !used[current[i].TransportURL] {
			used[current[i].TransportURL] = true
			order = append(order, current[i].TransportURL)
		}
	}
	s.mu.RUnlock()
	for _, a := range current {
		if !used[a.TransportURL] {
			order = append(order, a.TransportURL)
		}
	}
	err := s.db.Tx(func(tx *sql.Tx) error {
		for i, u := range order {
			if _, err := tx.Exec(`UPDATE stremio_addons SET position = ? WHERE transport_url = ?`, i, u); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := s.load(); err != nil {
		return err
	}
	s.changed()
	return nil
}

// Refresh downloads the manifests of the installed addons again (addons
// change their resources, names and logos over time). It returns the
// errors by transport URL; an addon that can't be reached keeps its
// manifest.
func (s *Store) Refresh(ctx context.Context) map[string]error {
	list := s.List()
	errs := map[string]error{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for _, a := range list {
		wg.Add(1)
		go func(a Addon) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			fresh, err := s.client.fetchManifest(ctx, a.TransportURL, a.Local)
			if err == nil {
				raw := fresh.raw
				if len(raw) == 0 {
					raw, _ = json.Marshal(fresh.Manifest)
				}
				_, err = s.db.Write(`UPDATE stremio_addons SET manifest = ? WHERE transport_url = ?`, string(raw), a.TransportURL)
			}
			if err != nil {
				mu.Lock()
				errs[a.TransportURL] = err
				mu.Unlock()
			}
		}(a)
	}
	wg.Wait()
	if err := s.load(); err != nil {
		errs[""] = err
	}
	s.changed()
	return errs
}

// Providers returns the enabled addons that serve streams, in order.
func (s *Store) Providers() []providers.Provider {
	var out []providers.Provider
	for _, a := range s.List() {
		if !a.Enabled {
			continue
		}
		p := NewProvider(s.client, a)
		if p.Supports(providers.TypeMovie) || p.Supports(providers.TypeTV) {
			out = append(out, p)
		}
	}
	return out
}

// SubtitleProviders returns the enabled addons that serve subtitles, in
// order (each is an *AddonProvider).
func (s *Store) SubtitleProviders() []providers.SubtitleProvider {
	var out []providers.SubtitleProvider
	for _, a := range s.List() {
		if !a.Enabled {
			continue
		}
		p := NewProvider(s.client, a)
		if p.SupportsSubtitles(providers.TypeMovie) || p.SupportsSubtitles(providers.TypeTV) {
			out = append(out, p)
		}
	}
	return out
}
