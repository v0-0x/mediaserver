// Package anikohub is Aniko running as a hub (aniko --hub): a small always-on
// computer (a Raspberry Pi) that shares its library with the Aniko apps on
// the network and keeps them in step. It plays nothing itself: it keeps the
// watch history of every AniList account its devices use (each device's
// positions reach the others on the same account as they're saved), the
// AniList logins it's given (handed to the devices allowed them), each
// account's settings (theme and the like), and everything that happened,
// for its dashboard's audit log.
package anikohub

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"

	"github.com/v0-0x/mediaserver/anime/internal/db"
	"github.com/v0-0x/mediaserver/anime/internal/history"
)

var schema = []string{
	// Each account's watch history: what history.Store keeps for one, here
	// for every account (user: its AniList ID).
	`CREATE TABLE IF NOT EXISTS hub_history (
		user INTEGER NOT NULL,
		media_id INTEGER NOT NULL,
		episode INTEGER NOT NULL,
		position REAL NOT NULL DEFAULT 0,
		duration REAL NOT NULL DEFAULT 0,
		source TEXT NOT NULL DEFAULT '',
		updated_at INTEGER NOT NULL,
		seq INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (user, media_id, episode)
	)`,
	`CREATE INDEX IF NOT EXISTS idx_hub_history_seq ON hub_history(user, seq)`,
	// The AniList logins given to the hub.
	`CREATE TABLE IF NOT EXISTS hub_accounts (
		user INTEGER PRIMARY KEY,
		name TEXT NOT NULL DEFAULT '',
		avatar TEXT NOT NULL DEFAULT '',
		token TEXT NOT NULL,
		given_by TEXT NOT NULL DEFAULT '',
		added_at INTEGER NOT NULL
	)`,
	// Each account's settings (Prefs), as last changed on one of its devices.
	`CREATE TABLE IF NOT EXISTS hub_prefs (
		user INTEGER PRIMARY KEY,
		data TEXT NOT NULL,
		updated_at INTEGER NOT NULL
	)`,
	// The audit log (audit.go): everything that happened on the hub.
	`CREATE TABLE IF NOT EXISTS hub_audit (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		at INTEGER NOT NULL,
		kind TEXT NOT NULL,
		search TEXT NOT NULL,
		data TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_hub_audit_kind ON hub_audit(kind, id)`,
}

// Store keeps what the hub knows.
type Store struct {
	db *db.DB

	mu sync.Mutex
	// seq: the order entries are stored in (all accounts), see After.
	seq int64
}

// New opens the hub's tables (made the first time).
func New(d *db.DB) (*Store, error) {
	for _, q := range schema {
		if _, err := d.Write(q); err != nil {
			return nil, err
		}
	}
	s := &Store{db: d}
	_ = d.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM hub_history`).Scan(&s.seq)
	return s, nil
}

// ---------------------------------------------------------------- history

const entryCols = `media_id, episode, position, duration, source, updated_at, seq`

// How far ahead of the hub's clock a device's entries may be.
const maxAhead = 5 * time.Minute

// History is one account's watch history, as the sharing service asks a
// Aniko's (share.History).
func (s *Store) History(user int) *UserHistory { return &UserHistory{s: s, user: user} }

// UserHistory is one account's watch history.
type UserHistory struct {
	s    *Store
	user int
}

func (h *UserHistory) Since(t int64, limit int) []*history.Entry {
	return h.s.query(`WHERE user = ? AND updated_at > ? ORDER BY updated_at LIMIT ?`, h.user, t, limit)
}

// After: stored after seq, in the order they were (whatever their time).
func (h *UserHistory) After(seq int64, limit int) []*history.Entry {
	return h.s.query(`WHERE user = ? AND seq > ? ORDER BY seq LIMIT ?`, h.user, seq, limit)
}

func (h *UserHistory) Latest(limit int) []*history.Entry {
	return h.s.query(`WHERE user = ? ORDER BY seq DESC LIMIT ?`, h.user, limit)
}

// MergeAll keeps the entries newer than the hub's, and returns those.
func (h *UserHistory) MergeAll(entries []history.Entry) ([]history.Entry, error) {
	return h.s.merge(h.user, entries)
}

func (s *Store) query(where string, args ...any) []*history.Entry {
	rows, err := s.db.Query(`SELECT `+entryCols+` FROM hub_history `+where, args...)
	if err != nil {
		return []*history.Entry{}
	}
	defer rows.Close()
	out := []*history.Entry{}
	for rows.Next() {
		var e history.Entry
		if rows.Scan(&e.MediaID, &e.Episode, &e.Position, &e.Duration, &e.Source, &e.UpdatedAt, &e.Seq) == nil {
			out = append(out, &e)
		}
	}
	return out
}

func (s *Store) merge(user int, entries []history.Entry) ([]history.Entry, error) {
	if user <= 0 {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var merged []history.Entry
	seq := s.seq
	err := s.db.Tx(func(tx *sql.Tx) error {
		for _, e := range entries {
			if e.MediaID <= 0 || e.Episode < 0 || e.UpdatedAt <= 0 || e.Position < 0 || e.Duration < 0 {
				continue
			}
			e.UpdatedAt = min(e.UpdatedAt, time.Now().Add(maxAhead).Unix())
			res, err := tx.Exec(`INSERT INTO hub_history(user, media_id, episode, position, duration, source, updated_at, seq)
				VALUES(?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(user, media_id, episode) DO UPDATE SET position = excluded.position, duration = excluded.duration,
					source = excluded.source, updated_at = excluded.updated_at, seq = excluded.seq
				WHERE excluded.updated_at > hub_history.updated_at`,
				user, e.MediaID, e.Episode, e.Position, e.Duration, e.Source, e.UpdatedAt, seq+1)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n > 0 {
				seq++
				e.Seq = seq
				merged = append(merged, e)
			}
		}
		return nil
	})
	if err == nil {
		s.seq = seq
	}
	return merged, err
}

// HistoryCounts is how many episodes each account has a position for.
func (s *Store) HistoryCounts() map[int]int {
	out := map[int]int{}
	rows, err := s.db.Query(`SELECT user, COUNT(*) FROM hub_history GROUP BY user`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var u, n int
		if rows.Scan(&u, &n) == nil {
			out[u] = n
		}
	}
	return out
}

// --------------------------------------------------------------- accounts

// Account is an AniList login given to the hub (its token apart).
type Account struct {
	User   int    `json:"user"`
	Name   string `json:"name"`
	Avatar string `json:"avatar"`
	// GivenBy: the device that gave it (its name).
	GivenBy string `json:"givenBy"`
	AddedAt int64  `json:"addedAt"`
}

// SaveAccount keeps (or replaces) an account's login.
func (s *Store) SaveAccount(a Account, token string) error {
	if a.AddedAt == 0 {
		a.AddedAt = time.Now().Unix()
	}
	_, err := s.db.Write(`INSERT INTO hub_accounts(user, name, avatar, token, given_by, added_at) VALUES(?, ?, ?, ?, ?, ?)
		ON CONFLICT(user) DO UPDATE SET name = excluded.name, avatar = excluded.avatar, token = excluded.token,
			given_by = excluded.given_by, added_at = excluded.added_at`,
		a.User, a.Name, a.Avatar, token, a.GivenBy, a.AddedAt)
	return err
}

// Accounts are the logins given to the hub, the first given first.
func (s *Store) Accounts() []Account {
	out := []Account{}
	rows, err := s.db.Query(`SELECT user, name, avatar, given_by, added_at FROM hub_accounts ORDER BY added_at`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var a Account
		if rows.Scan(&a.User, &a.Name, &a.Avatar, &a.GivenBy, &a.AddedAt) == nil {
			out = append(out, a)
		}
	}
	return out
}

// Token is an account's login ("": not given).
func (s *Store) Token(user int) string {
	var t string
	_ = s.db.QueryRow(`SELECT token FROM hub_accounts WHERE user = ?`, user).Scan(&t)
	return t
}

// RemoveAccount forgets a login (the devices using it are logged out).
func (s *Store) RemoveAccount(user int) error {
	_, err := s.db.Write(`DELETE FROM hub_accounts WHERE user = ?`, user)
	if err == nil {
		s.forgetDeviceAccounts(user)
	}
	return err
}

// Which account each device is given: device ID → AniList ID (none: the
// first account given, when there's one).
const deviceAccountsKey = "hub:device-accounts"

func (s *Store) deviceAccounts() map[string]int {
	m := map[string]int{}
	_, _ = s.db.GetKV(deviceAccountsKey, &m)
	return m
}

// DeviceAccount is the account a device is given (0: none).
func (s *Store) DeviceAccount(device string) int {
	if u, ok := s.deviceAccounts()[device]; ok {
		if s.Token(u) != "" {
			return u
		}
	}
	if accs := s.Accounts(); len(accs) > 0 {
		return accs[0].User
	}
	return 0
}

// SetDeviceAccount sets the account a device is given.
func (s *Store) SetDeviceAccount(device string, user int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.deviceAccounts()
	if user == 0 {
		delete(m, device)
	} else {
		m[device] = user
	}
	return s.db.SetKV(deviceAccountsKey, m)
}

func (s *Store) forgetDeviceAccounts(user int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.deviceAccounts()
	for d, u := range m {
		if u == user {
			delete(m, d)
		}
	}
	_ = s.db.SetKV(deviceAccountsKey, m)
}

// ------------------------------------------------------------------ prefs

// Prefs are an account's settings as its devices share them (the theme…),
// as one sent them: the hub keeps them as they are.
type Prefs struct {
	Data      json.RawMessage `json:"data"`
	UpdatedAt int64           `json:"updatedAt"` // milliseconds
}

// Prefs is an account's settings (nil: none yet).
func (s *Store) Prefs(user int) *Prefs {
	var p Prefs
	var data string
	if err := s.db.QueryRow(`SELECT data, updated_at FROM hub_prefs WHERE user = ?`, user).Scan(&data, &p.UpdatedAt); err != nil {
		return nil
	}
	p.Data = json.RawMessage(data)
	return &p
}

// PrefsAt is when an account's settings last changed (0: never).
func (s *Store) PrefsAt(user int) int64 {
	var at int64
	_ = s.db.QueryRow(`SELECT updated_at FROM hub_prefs WHERE user = ?`, user).Scan(&at)
	return at
}

// SetPrefs keeps an account's settings when they're newer than the hub's,
// and reports whether they were.
func (s *Store) SetPrefs(user int, p Prefs) (bool, error) {
	if user <= 0 || len(p.Data) == 0 || !json.Valid(p.Data) || p.UpdatedAt <= 0 {
		return false, nil
	}
	if len(p.Data) > 64<<10 {
		return false, errTooLarge
	}
	// Not from the future: it would stay over every change after it.
	p.UpdatedAt = min(p.UpdatedAt, time.Now().Add(maxAhead).UnixMilli())
	res, err := s.db.Write(`INSERT INTO hub_prefs(user, data, updated_at) VALUES(?, ?, ?)
		ON CONFLICT(user) DO UPDATE SET data = excluded.data, updated_at = excluded.updated_at
		WHERE excluded.updated_at > hub_prefs.updated_at`, user, string(p.Data), p.UpdatedAt)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

type hubError string

func (e hubError) Error() string { return string(e) }

const errTooLarge = hubError("too large")

// ------------------------------------------------------------------ owner

// The dashboard is the owner's: a password, set the first time with the
// setup code the hub prints (its log, the install script), so only who can
// see the hub's screen or log in to it sets it.

const setupCodeKey = "hub:setup-code"
const sessionSecretKey = "hub:session-secret"

// SetupCode is the code the password is first set with.
func (s *Store) SetupCode() string {
	var code string
	if ok, _ := s.db.GetKV(setupCodeKey, &code); ok && code != "" {
		return code
	}
	code = randomDigits(4) + "-" + randomDigits(4)
	_ = s.db.SetKV(setupCodeKey, code)
	return code
}

// SessionSecret signs the dashboard's sessions: kept, so they outlast the
// hub restarting (an update).
func (s *Store) SessionSecret() string {
	var sec string
	if ok, _ := s.db.GetKV(sessionSecretKey, &sec); ok && len(sec) >= 32 {
		return sec
	}
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	sec = hex.EncodeToString(b)
	_ = s.db.SetKV(sessionSecretKey, sec)
	return sec
}

func randomDigits(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = '0' + b[i]%10
	}
	return string(b)
}
