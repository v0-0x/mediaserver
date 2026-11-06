package api

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/v0-0x/mediaserver/anime/internal/anikohub"
	"github.com/v0-0x/mediaserver/anime/internal/anilist"
	"github.com/v0-0x/mediaserver/anime/internal/config"
	"github.com/v0-0x/mediaserver/anime/internal/library"
	"github.com/v0-0x/mediaserver/anime/internal/share"
)

// An Aniko Hub's dashboard (aniko --hub): what the hub's owner sees from a
// browser on the network, signed in with its password (set the first time
// with the setup code, see middleware).

func (s *Server) hubRoutes() {
	m := s.mux
	owner := func(fn func(r *http.Request) (any, error)) http.HandlerFunc {
		return h(func(r *http.Request) (any, error) {
			if s.app.HubStore == nil {
				return nil, notFound("this Aniko isn't a hub")
			}
			if !isTrusted(r) {
				return nil, forbidden("sign in to the hub first")
			}
			return fn(r)
		})
	}
	m.HandleFunc("POST /api/hub/setup", s.hubSetup)
	m.HandleFunc("GET /api/hub/overview", owner(s.hubOverview))
	s.auditAPI(owner)
	// A device: share the library with it (or stop), the AniList login it's
	// given (and which), downloading onto the hub.
	m.HandleFunc("POST /api/hub/devices/{id}", owner(func(r *http.Request) (any, error) {
		var body struct {
			share.Grants
			AccountUser *int `json:"accountUser"`
		}
		if err := decode(r, &body); err != nil {
			return nil, err
		}
		id := r.PathValue("id")
		if err := s.app.Share.SetGrants(id, body.Grants); err != nil {
			return nil, notFound(err.Error())
		}
		if body.AccountUser != nil {
			if err := s.app.Share.SetDeviceAccount(id, *body.AccountUser); err != nil {
				return nil, badRequest(err.Error())
			}
		}
		s.app.Hub.Publish("hub-updated", nil)
		return s.app.Share.HubDevices(), nil
	}))
	// Forget an AniList login given to the hub: the devices using it are
	// logged out of it.
	m.HandleFunc("DELETE /api/hub/accounts/{user}", owner(func(r *http.Request) (any, error) {
		user, _ := strconv.Atoi(r.PathValue("user"))
		if err := s.app.HubStore.RemoveAccount(user); err != nil {
			return nil, err
		}
		by, ip := auditBy(r)
		s.app.HubStore.Log(anikohub.Event{Kind: "account", By: by, IP: ip, User: user, Text: "removed an AniList login from the hub"})
		s.app.Hub.Publish("hub-updated", nil)
		return nil, nil
	}))
	// A new password: the other browsers signed in with the old one are
	// signed out, this one stays in.
	m.HandleFunc("POST /api/hub/password", func(w http.ResponseWriter, r *http.Request) {
		if s.app.HubStore == nil || !isTrusted(r) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "sign in to the hub first"})
			return
		}
		var body struct {
			Password string `json:"password"`
		}
		if err := decode(r, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if len(body.Password) < 6 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "use at least 6 characters"})
			return
		}
		cfg := s.app.Settings.Get()
		cfg.Server.Password = body.Password
		if _, err := s.app.Settings.Save(cfg); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		s.signIn(w)
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	m.HandleFunc("POST /api/hub/signout", func(w http.ResponseWriter, r *http.Request) {
		if s.app.HubStore != nil && isTrusted(r) {
			by, ip := auditBy(r)
			s.app.HubStore.Log(anikohub.Event{Kind: "signout", By: by, IP: ip, Text: "signed out"})
		}
		http.SetCookie(w, &http.Cookie{Name: "kumo_session", Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
}

// hubSetup sets the hub's password the first time, with its setup code,
// and signs this browser in.
func (s *Server) hubSetup(w http.ResponseWriter, r *http.Request) {
	if s.app.HubStore == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "this Aniko isn't a hub"})
		return
	}
	var body struct {
		Code     string `json:"code"`
		Password string `json:"password"`
	}
	if err := decode(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	cfg := s.app.Settings.Get()
	if cfg.Server.Password != "" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "the hub's password is already set: sign in with it"})
		return
	}
	ip := clientIP(r).String()
	if wait := s.logins.wait(ip, time.Now()); wait > 0 {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many wrong codes: try again in a few minutes"})
		return
	}
	if !constEq(normalCode(body.Code), normalCode(s.app.HubStore.SetupCode())) {
		s.logins.failed(ip, time.Now())
		s.signedIn(r, false, "tried a wrong setup code")
		time.Sleep(time.Second)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "that isn't the setup code"})
		return
	}
	if len(body.Password) < 6 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "use at least 6 characters"})
		return
	}
	s.logins.passed(ip)
	cfg.Server.Password = body.Password
	if _, err := s.app.Settings.Save(cfg); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.signedIn(r, true, "set the dashboard's password (the first time, with the setup code)")
	s.signIn(w)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// signIn gives this browser the session cookie (the password's).
func (s *Server) signIn(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: "kumo_session", Value: s.sessionToken(), Path: "/", HttpOnly: true,
		SameSite: http.SameSiteStrictMode, Expires: time.Now().Add(365 * 24 * time.Hour)})
}

// normalCode: the code as typed, digits only ("4821 9930" = "4821-9930").
func normalCode(c string) string {
	out := make([]byte, 0, len(c))
	for i := 0; i < len(c); i++ {
		if c[i] >= '0' && c[i] <= '9' {
			out = append(out, c[i])
		}
	}
	return string(out)
}

// hubStatus is what /api/status says of a hub: to a browser on the network
// not signed in, only that it's one (and whether its password is set yet).
func (s *Server) hubStatus(r *http.Request, full map[string]any) map[string]any {
	setup := s.app.Settings.Get().Server.Password == ""
	if kindOf(r) == clientLAN {
		return map[string]any{"version": config.AppVersion, "appName": config.AppName, "client": "lan", "hub": map[string]any{"setup": setup}}
	}
	full["hub"] = map[string]any{"setup": setup}
	return full
}

// ---------------------------------------------------------------- overview

type hubOverview struct {
	Name      string   `json:"name"`
	Version   string   `json:"version"`
	Addresses []string `json:"addresses"`
	Port      int      `json:"port"`

	System  anikohub.System       `json:"system"`
	Streams []anikohub.StreamView `json:"streams"`
	Recent  []anikohub.StreamView `json:"recent"`
	Sent    int64                 `json:"sent"`
	Playing []hubPlaying          `json:"playing"`

	Devices  []share.HubDevice `json:"devices"`
	Accounts []hubAccount      `json:"accounts"`
	Activity []anikohub.Event  `json:"activity"`
	Library  hubLibrary        `json:"library"`
	// Encoder: what converts video when a device can't play a file as it
	// is ("" none: ffmpeg isn't there).
	Encoder   string `json:"encoder"`
	Downloads int    `json:"downloads"` // being downloaded onto the hub
	// Server: mediaserver's version, where its movies side's pages are
	// and where its music side answers (0: none).
	Server     string `json:"server,omitempty"`
	MoviesPort int    `json:"moviesPort,omitempty"`
	MusicPort  int    `json:"musicPort,omitempty"`
}

type hubPlaying struct {
	share.Playing
	Title string `json:"title"`
	Cover string `json:"cover"`
}

type hubAccount struct {
	anikohub.Account
	Devices int `json:"devices"` // using it now
	History int `json:"history"` // episodes with a position
}

type hubLibrary struct {
	Folders   []string `json:"folders"`
	Files     int      `json:"files"`
	Anime     int      `json:"anime"`
	Size      int64    `json:"size"`
	Unmatched int      `json:"unmatched"`
	Scanning  bool     `json:"scanning"`
	// Progress: where the scan running is (with Scanning).
	Progress *library.ScanProgress `json:"progress,omitempty"`
}

// The library's numbers, kept a moment (the dashboard asks every few
// seconds): also while a scan runs, when they're the numbers from before
// it (the scan saves at its end), with where the scan is beside them.
var libStats struct {
	sync.Mutex
	at       time.Time
	v        hubLibrary
	scanning bool
}

// titleCache is what the dashboard calls the anime its activity is about:
// AniList's names, from what's kept here; the ones not kept are asked for
// in the background, never while the dashboard waits (an AniList slow or
// away held every overview for seconds).
type titleCache struct {
	mu        sync.Mutex
	titles    map[int]titleEntry
	resolving map[int]bool
}

type titleEntry struct {
	title, cover string
	at           time.Time // zero: known for good; else when it wasn't (asked again after a while)
}

const titleMissFor = 2 * time.Minute

var titles titleCache

// get is the names of these anime, as known now; the unknown are asked for
// soon.
func (c *titleCache) get(p *anilist.Platform, ids []int) map[int]titleEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.titles == nil {
		c.titles, c.resolving = map[int]titleEntry{}, map[int]bool{}
	}
	out := map[int]titleEntry{}
	var ask []int
	now := time.Now()
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		e, ok := c.titles[id]
		if ok && (e.at.IsZero() || now.Sub(e.at) < titleMissFor) {
			out[id] = e
			continue
		}
		if m := p.CachedLite(id); m != nil {
			e = titleEntry{title: m.PreferredTitle(), cover: m.CoverImage.Large}
			c.titles[id] = e
			out[id] = e
			continue
		}
		if !c.resolving[id] {
			c.resolving[id] = true
			ask = append(ask, id)
		}
	}
	if len(ask) > 0 {
		go c.resolve(p, ask)
	}
	return out
}

func (c *titleCache) resolve(p *anilist.Platform, ids []int) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	got, _ := p.MediaBatch(ctx, ids)
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, id := range ids {
		delete(c.resolving, id)
		if m, ok := got[id]; ok && m != nil {
			c.titles[id] = titleEntry{title: m.PreferredTitle(), cover: m.CoverImage.Large}
		} else {
			c.titles[id] = titleEntry{at: time.Now()}
		}
	}
}

// The accounts' history counts, kept half a minute (a count over the whole
// table each time the dashboard asks).
var historyCounts struct {
	sync.Mutex
	at time.Time
	v  map[int]int
}

// libraryEdited wraps a handler that changes the library by hand
// (matching, ignoring…): the numbers are counted again at once.
func libraryEdited(fn func(*http.Request) (any, error)) func(*http.Request) (any, error) {
	return func(r *http.Request) (any, error) {
		v, err := fn(r)
		if err == nil {
			libStats.Lock()
			libStats.at = time.Time{}
			libStats.Unlock()
		}
		return v, err
	}
}

func (s *Server) hubLibrary() hubLibrary {
	libStats.Lock()
	defer libStats.Unlock()
	scanning := s.app.Scanner.Running()
	if libStats.scanning && !scanning {
		// A scan just ended: its numbers now.
		libStats.at = time.Time{}
	}
	libStats.scanning = scanning
	if time.Since(libStats.at) < 15*time.Second || scanning && !libStats.at.IsZero() {
		v := libStats.v
		v.Scanning = scanning
		v.Progress = s.app.Scanner.Progress()
		return v
	}
	v := hubLibrary{Folders: s.app.Settings.Get().LibraryDirs()}
	files, err := s.app.Files.All()
	if err != nil && !libStats.at.IsZero() {
		// Couldn't be read now (the database busy): the last numbers stand.
		v = libStats.v
		v.Scanning, v.Progress = scanning, s.app.Scanner.Progress()
		return v
	}
	anime := map[int]bool{}
	for _, f := range files {
		if f.Ignored {
			continue
		}
		v.Files++
		v.Size += f.Size
		if f.MediaID > 0 {
			anime[f.MediaID] = true
		} else {
			v.Unmatched++
		}
	}
	v.Anime = len(anime)
	libStats.at, libStats.v = time.Now(), v
	v.Scanning, v.Progress = scanning, s.app.Scanner.Progress()
	return v
}

func (s *Server) hubOverview(r *http.Request) (any, error) {
	cfg := s.app.Settings.Get()
	hs := s.app.HubStore
	lib := s.hubLibrary()
	o := hubOverview{
		Name: s.app.Share.Name(), Version: config.AppVersion, Addresses: lanURLs(cfg.Server.Port), Port: cfg.Server.Port,
		System:  s.app.Sampler.Look(append(append([]string{}, lib.Folders...), s.app.DataDir)),
		Streams: s.app.Streams.Live(), Recent: s.app.Streams.Recent(), Sent: s.app.Streams.Sent(),
		Devices: s.app.Share.HubDevices(), Activity: hs.Activity(), Library: lib,
		Encoder: s.app.Local.Encoder(), Playing: []hubPlaying{}, Accounts: []hubAccount{},
		Server: s.app.Update.Version, MoviesPort: s.MoviesPort, MusicPort: s.MusicPort,
	}
	for _, it := range s.app.Downloads.List() {
		if it.Status == "downloading" || it.Status == "queued" {
			o.Downloads++
		}
	}
	// What's playing and what the activity is about, by name: AniList's,
	// from what's kept here (the rest is asked for in the background).
	playing := s.app.Share.NowPlaying()
	var ids []int
	for _, p := range playing {
		ids = append(ids, p.MediaID)
	}
	for _, e := range o.Activity {
		ids = append(ids, e.MediaID)
	}
	names := titles.get(s.app.Platform, ids)
	for _, p := range playing {
		hp := hubPlaying{Playing: p}
		if e, ok := names[p.MediaID]; ok {
			hp.Title, hp.Cover = e.title, e.cover
		}
		o.Playing = append(o.Playing, hp)
	}
	for i, e := range o.Activity {
		if n, ok := names[e.MediaID]; ok && n.title != "" {
			o.Activity[i].Title = n.title
		}
	}
	historyCounts.Lock()
	if time.Since(historyCounts.at) > 30*time.Second {
		historyCounts.v, historyCounts.at = hs.HistoryCounts(), time.Now()
	}
	counts := historyCounts.v
	historyCounts.Unlock()
	for _, a := range hs.Accounts() {
		ha := hubAccount{Account: a, History: counts[a.User]}
		for _, d := range o.Devices {
			if d.Online && d.User == a.User {
				ha.Devices++
			}
		}
		o.Accounts = append(o.Accounts, ha)
	}
	return o, nil
}
