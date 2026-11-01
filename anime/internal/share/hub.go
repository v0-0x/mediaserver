package share

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/v0-0x/mediaserver/anime/internal/anikohub"
	"github.com/v0-0x/mediaserver/anime/internal/history"
)

// An Aniko Hub (aniko --hub, package anikohub) is an Aniko that plays nothing: a
// small always-on computer that shares its library with the Aniko apps on
// the network and keeps them in step. To each device it says it's on the
// device's own AniList account, so the device keeps its watch history in
// step with it as with any host on its account (pushing what it saves,
// asking for what it missed); the hub keeps each account's history apart,
// and passes each position on at once to the other devices on that account.
// It also keeps the AniList logins it's given (each device allowed one gets
// the one chosen for it) and each account's settings (the theme…), sent on
// to the account's devices as they change.

// RoleHub is what a hub's beacons and hellos say it is.
const RoleHub = "hub"

// IsHub: this Aniko is a hub.
func (s *Service) IsHub() bool { return s.Hub != nil }

func (s *Service) role() string {
	if s.Hub != nil {
		return RoleHub
	}
	return ""
}

// historyFor is the watch history a caller on the same account syncs with:
// this Aniko's, or on a hub the caller's account's.
func (s *Service) historyFor(c Caller) History {
	if s.Hub != nil {
		if c.User <= 0 {
			return nil
		}
		return s.Hub.History(c.User)
	}
	if s.History == nil {
		return nil
	}
	return s.History
}

// hubLog notes something for the hub's dashboard.
func (s *Service) hubLog(kind, device string, user int, format string, args ...any) {
	if s.Hub == nil {
		return
	}
	s.Hub.Log(anikohub.Event{Kind: kind, Device: device, User: user, Text: fmt.Sprintf(format, args...)})
	s.hub.Publish("hub-updated", nil)
}

// hubTake keeps positions a device sent in its account's history, and sends
// those that are new on to the account's other devices.
func (s *Service) hubTake(c Caller, entries []history.Entry) {
	if c.User <= 0 {
		return
	}
	merged, err := s.Hub.History(c.User).MergeAll(entries)
	if err != nil {
		log.Printf("hub: history: %v", err)
		return
	}
	if len(merged) == 0 {
		return
	}
	s.hubWatching(c, merged)
	s.relay(c.User, c.ID, "/api/peer/history", merged)
	s.hub.Publish("hub-updated", nil)
}

// relay sends something (JSON) to the devices on an account that are around,
// but the one it came from: signed as that account's (see sign), each takes
// it as from an Aniko on its own.
func (s *Service) relay(user int, from, path string, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		return
	}
	now := time.Now()
	s.mu.Lock()
	var to []*peer
	for _, p := range s.peers {
		if p.allowed && p.id != from && p.callerUser == user && p.addr != nil && p.port > 0 && p.online(now) {
			to = append(to, p)
		}
	}
	s.mu.Unlock()
	var wg sync.WaitGroup
	for _, p := range to {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			req, err := s.request(ctx, p, http.MethodPost, path, bytes.NewReader(body))
			if err != nil {
				return
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := s.do(s.api, req)
			if err != nil {
				log.Printf("hub: to %s: %v", s.nameOf(p), plainError(err))
				return
			}
			resp.Body.Close()
		}()
	}
	wg.Wait()
}

// ------------------------------------------------------------ now playing

// Playing is what a device is watching, as its positions say.
type Playing struct {
	Device   string  `json:"device"`
	Name     string  `json:"name"`
	User     int     `json:"user"`
	MediaID  int     `json:"mediaId"`
	Episode  int     `json:"episode"`
	Position float64 `json:"position"`
	Duration float64 `json:"duration"`
	At       int64   `json:"at"` // when it last said (ms)
}

// How long a device's last position counts as it playing.
const playingFor = 90 * time.Second

func (s *Service) hubWatching(c Caller, merged []history.Entry) {
	latest := merged[0]
	for _, e := range merged[1:] {
		if e.UpdatedAt > latest.UpdatedAt {
			latest = e
		}
	}
	// Only what it's watching now: a sync (a batch of old ones) isn't.
	if time.Since(time.Unix(latest.UpdatedAt, 0)) > playingFor {
		return
	}
	s.playingMu.Lock()
	if s.playing == nil {
		s.playing = map[string]Playing{}
	}
	prev, had := s.playing[c.ID]
	s.playing[c.ID] = Playing{Device: c.ID, Name: c.Name, User: c.User, MediaID: latest.MediaID, Episode: latest.Episode, Position: latest.Position, Duration: latest.Duration, At: time.Now().UnixMilli()}
	s.playingMu.Unlock()
	if !had || prev.MediaID != latest.MediaID || prev.Episode != latest.Episode {
		s.hubEpisode("playing", c, latest, "started watching")
	}
	if latest.Percent() >= 0.85 && (!had || prev.Duration <= 0 || prev.Position/prev.Duration < 0.85 || prev.Episode != latest.Episode) {
		s.hubEpisode("finished", c, latest, "finished")
	}
}

func (s *Service) hubEpisode(kind string, c Caller, e history.Entry, text string) {
	s.Hub.Log(anikohub.Event{Kind: kind, Device: c.Name, User: c.User, Text: text, MediaID: e.MediaID, Episode: e.Episode})
	s.hub.Publish("hub-updated", nil)
}

// NowPlaying is what the devices are watching now.
func (s *Service) NowPlaying() []Playing {
	s.playingMu.Lock()
	defer s.playingMu.Unlock()
	out := []Playing{}
	cut := time.Now().Add(-playingFor).UnixMilli()
	for id, p := range s.playing {
		if p.At < cut {
			delete(s.playing, id)
			continue
		}
		out = append(out, p)
	}
	return out
}

// ---------------------------------------------------------------- accounts

// hubAccountTag is the tag (see accountTag) of the login a device is given.
func (s *Service) hubAccountTag(device string) string {
	return accountTag(s.Hub.Token(s.Hub.DeviceAccount(device)))
}

// GivenAccount is a device giving the hub its AniList login, sealed for it.
type GivenAccount struct {
	Token string `json:"token"`
}

// CheckAccount asks AniList whose a login is (a hub checks each it's given).
// Set by the app.
type CheckAccount func(ctx context.Context, token string) (user int, name, avatar string, err error)

// TakeAccount keeps an AniList login a device allowed by the hub gives it.
func (s *Service) TakeAccount(ctx context.Context, c Caller, g GivenAccount) (anikohub.Account, error) {
	if s.Hub == nil || s.CheckAccount == nil {
		return anikohub.Account{}, errors.New("not a hub")
	}
	if !c.Allowed {
		return anikohub.Account{}, errors.New("the hub doesn't share its library with this device")
	}
	s.mu.Lock()
	p := s.peers[c.ID]
	var pub []byte
	if p != nil {
		pub = p.pub
	}
	s.mu.Unlock()
	if pub == nil {
		return anikohub.Account{}, errors.New("unknown device")
	}
	// Sealed by the device for the hub: the hub is the one it's for.
	plain, err := s.id.openFrom(pub, s.id.ID, c.ID, g.Token)
	if err != nil {
		return anikohub.Account{}, errors.New("the login couldn't be read")
	}
	token := strings.TrimSpace(string(plain))
	user, name, avatar, err := s.CheckAccount(ctx, token)
	if err != nil {
		return anikohub.Account{}, fmt.Errorf("AniList didn't take that login: %w", err)
	}
	acc := anikohub.Account{User: user, Name: name, Avatar: avatar, GivenBy: c.Name}
	if err := s.Hub.SaveAccount(acc, token); err != nil {
		return anikohub.Account{}, err
	}
	s.hubLog("account", c.Name, user, "gave the hub the AniList login of %s", name)
	return acc, nil
}

// GiveAccount gives a hub this Aniko's AniList login (sealed for it): the
// hub can then hand it to the devices it lets use it.
func (s *Service) GiveAccount(ctx context.Context, hubID string) error {
	p, err := s.hubHost(hubID)
	if err != nil {
		return err
	}
	token := s.accountToken()
	if token == "" {
		return errNoAccount
	}
	s.mu.Lock()
	pub := p.pub
	s.mu.Unlock()
	sealed, err := s.id.sealFor(pub, p.id, s.id.ID, []byte(token))
	if err != nil {
		return err
	}
	return s.postJSON(ctx, p, "/api/peer/hub/account", GivenAccount{Token: sealed}, nil)
}

// hubHost is a hub that shares its library with this Aniko.
func (s *Service) hubHost(id string) (*peer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.peers[id]
	if p == nil || p.role != RoleHub {
		return nil, errors.New("that isn't an Aniko Hub this Aniko knows")
	}
	if !p.shares || p.addr == nil {
		return nil, errors.New("the hub doesn't share its library with this Aniko yet: allow it on the hub's dashboard")
	}
	return p, nil
}

func (s *Service) postJSON(ctx context.Context, p *peer, path string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := s.request(ctx, p, http.MethodPost, path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.do(s.api, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := answerError(resp); err != nil {
		return err
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// ------------------------------------------------------------------ prefs

// The device's side: its shared settings (config.SharedPrefs), as JSON, and
// when they last changed here (ms); and taking an account's from the hub.
// Set by the app; nil: not kept in step.
type PrefsSource func() (data json.RawMessage, at int64)
type PrefsSink func(data json.RawMessage, at int64)

// HubPrefs answers a device asking for its account's settings.
func (s *Service) HubPrefs(c Caller) (*anikohub.Prefs, error) {
	if s.Hub == nil || c.User <= 0 {
		return nil, errors.New("not on an AniList account")
	}
	if p := s.Hub.Prefs(c.User); p != nil {
		return p, nil
	}
	return &anikohub.Prefs{}, nil
}

// TakeHubPrefs keeps the settings a device changed for its account, and
// sends them on to the account's other devices.
func (s *Service) TakeHubPrefs(c Caller, p anikohub.Prefs) error {
	if s.Hub == nil || c.User <= 0 {
		return errors.New("not on an AniList account")
	}
	newer, err := s.Hub.SetPrefs(c.User, p)
	if err != nil || !newer {
		return err
	}
	s.hubLog("prefs", c.Name, c.User, "changed the theme and settings")
	go s.relay(c.User, c.ID, "/api/peer/prefs", p)
	return nil
}

// TakePrefs (a device) takes its account's settings a hub sent, when
// they're newer than its own and it keeps them in step.
func (s *Service) TakePrefs(from string, p anikohub.Prefs) {
	s.mu.Lock()
	host := s.peers[from]
	isHub := host != nil && host.role == RoleHub && host.shares
	s.mu.Unlock()
	if !isHub || s.OwnPrefs == nil || s.ApplyPrefs == nil || !s.settings.Get().Sharing.SyncPrefs {
		return
	}
	if _, at := s.OwnPrefs(); p.UpdatedAt > at && len(p.Data) > 0 {
		s.ApplyPrefs(p.Data, p.UpdatedAt)
	}
}

// syncPrefs (a device) brings its settings in step with its account's on a
// hub: the newer of the two wins.
func (s *Service) syncPrefs(ctx context.Context, p *peer, hubAt int64) {
	if s.OwnPrefs == nil || s.ApplyPrefs == nil || !s.settings.Get().Sharing.SyncPrefs {
		return
	}
	data, at := s.OwnPrefs()
	switch {
	// Neither changed them yet: this device's are the account's to start
	// with (the next devices take them).
	case at == 0 && hubAt == 0 && len(data) > 0:
		_ = s.postJSON(ctx, p, "/api/peer/hub/prefs", anikohub.Prefs{Data: data, UpdatedAt: time.Now().UnixMilli()}, nil)
	case hubAt > at:
		var hp anikohub.Prefs
		if err := s.getJSON(ctx, p, "/api/peer/hub/prefs", &hp); err == nil && hp.UpdatedAt > at && len(hp.Data) > 0 {
			s.ApplyPrefs(hp.Data, hp.UpdatedAt)
		}
	case at > hubAt && len(data) > 0:
		_ = s.postJSON(ctx, p, "/api/peer/hub/prefs", anikohub.Prefs{Data: data, UpdatedAt: at}, nil)
	}
}

// PrefsChanged (a device) sends its settings, changed here, to the hubs it
// uses on its account.
func (s *Service) PrefsChanged() {
	if s.OwnPrefs == nil || !s.settings.Get().Sharing.SyncPrefs {
		return
	}
	me := s.user()
	if me == 0 {
		return
	}
	data, at := s.OwnPrefs()
	s.mu.Lock()
	var hubs []*peer
	for _, p := range s.peers {
		if p.role == RoleHub && p.shares && p.user == me && p.addr != nil && p.fails < maxFails {
			hubs = append(hubs, p)
		}
	}
	s.mu.Unlock()
	for _, p := range hubs {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := s.postJSON(ctx, p, "/api/peer/hub/prefs", anikohub.Prefs{Data: data, UpdatedAt: at}, nil); err != nil {
				log.Printf("sharing: settings to %s: %v", s.nameOf(p), plainError(err))
			}
		}()
	}
}

// ----------------------------------------------------------------- import

// SendToHub (a device) gives a hub everything it can take from this Aniko:
// its settings (as the account's), its AniList login, and its whole watch
// history: the hub starts where this device is.
func (s *Service) SendToHub(ctx context.Context, hubID string) (string, error) {
	p, err := s.hubHost(hubID)
	if err != nil {
		return "", err
	}
	var done []string
	if s.accountToken() != "" {
		if err := s.GiveAccount(ctx, hubID); err != nil {
			return "", fmt.Errorf("the AniList login: %w", err)
		}
		done = append(done, "the AniList login")
	}
	if s.OwnPrefs != nil && s.user() > 0 {
		data, _ := s.OwnPrefs()
		if len(data) > 0 {
			// Now: the hub takes them over the account's.
			if err := s.postJSON(ctx, p, "/api/peer/hub/prefs", anikohub.Prefs{Data: data, UpdatedAt: time.Now().UnixMilli()}, nil); err != nil {
				return "", fmt.Errorf("the settings: %w", err)
			}
			done = append(done, "the theme and settings")
		}
	}
	if s.History != nil && s.user() > 0 {
		n := 0
		for after := int64(0); ; {
			entries := s.History.After(after, historyBatch)
			if len(entries) == 0 {
				break
			}
			body, err := json.Marshal(entries)
			if err != nil {
				return "", err
			}
			if err := s.sendHistory(ctx, p, body); err != nil {
				return "", fmt.Errorf("the watch history: %w", err)
			}
			n += len(entries)
			after = entries[len(entries)-1].Seq
			if len(entries) < historyBatch {
				break
			}
		}
		if n > 0 {
			done = append(done, fmt.Sprintf("%d watched positions", n))
		}
	}
	if len(done) == 0 {
		return "", errors.New("there's nothing to send: log into AniList first")
	}
	_ = s.postJSON(ctx, p, "/api/peer/hub/imported", map[string]any{"what": done}, nil)
	return "Sent " + joinWords(done) + " to " + s.nameOf(p) + ".", nil
}

// Imported notes on a hub what a device sent it (SendToHub).
func (s *Service) Imported(c Caller, what []string) {
	if len(what) > 6 {
		what = what[:6]
	}
	s.hubLog("import", c.Name, c.User, "imported %s", joinWords(what))
}

func joinWords(w []string) string {
	switch len(w) {
	case 0:
		return ""
	case 1:
		return w[0]
	}
	return strings.Join(w[:len(w)-1], ", ") + " and " + w[len(w)-1]
}

// ---------------------------------------------------------------- devices

// HubDevice is a device as a hub's dashboard shows it.
type HubDevice struct {
	PeerView
	// User: the AniList account it's on, as it last said (0: none);
	// AccountUser: the login the hub gives it (with Account).
	User        int `json:"user"`
	AccountUser int `json:"accountUser"`
}

// HubDevices are the devices a hub knows: those it shares with first, then
// those around it hasn't been told to.
func (s *Service) HubDevices() []HubDevice {
	if s.Hub == nil {
		return []HubDevice{}
	}
	now := time.Now()
	s.mu.Lock()
	type one struct {
		v    PeerView
		user int
	}
	var all []one
	for _, p := range s.peers {
		// Other hubs, and Anikos only heard of long ago, aren't devices.
		if p.role == RoleHub || (!p.allowed && !p.online(now) && p.manual == "") {
			continue
		}
		all = append(all, one{s.view(p, now), p.callerUser})
	}
	s.mu.Unlock()
	out := make([]HubDevice, 0, len(all))
	for _, o := range all {
		d := HubDevice{PeerView: o.v, User: o.user}
		if o.v.Account {
			d.AccountUser = s.Hub.DeviceAccount(o.v.ID)
		}
		out = append(out, d)
	}
	sortDevices(out)
	return out
}

func sortDevices(d []HubDevice) {
	rank := func(x HubDevice) int {
		switch {
		case x.Allowed && x.Online:
			return 0
		case x.Allowed:
			return 1
		case x.Online:
			return 2
		}
		return 3
	}
	for i := 1; i < len(d); i++ {
		for j := i; j > 0; j-- {
			a, b := d[j-1], d[j]
			if rank(a) < rank(b) || rank(a) == rank(b) && strings.ToLower(a.Name) <= strings.ToLower(b.Name) {
				break
			}
			d[j-1], d[j] = b, a
		}
	}
}

// SetDeviceAccount sets the login a hub gives a device (0: the first one
// given), and makes sure it's allowed one.
func (s *Service) SetDeviceAccount(device string, user int) error {
	if s.Hub == nil {
		return errors.New("not a hub")
	}
	if user != 0 && s.Hub.Token(user) == "" {
		return errors.New("the hub has no such AniList login")
	}
	if err := s.Hub.SetDeviceAccount(device, user); err != nil {
		return err
	}
	on := true
	if err := s.SetGrants(device, Grants{Account: &on}); err != nil {
		return err
	}
	s.hub.Publish("hub-updated", nil)
	return nil
}
