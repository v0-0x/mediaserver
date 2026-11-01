package share

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/config"
	"github.com/v0-0x/mediaserver/movies/internal/db"
	"github.com/v0-0x/mediaserver/movies/internal/events"
	"github.com/v0-0x/mediaserver/movies/internal/history"
	"github.com/v0-0x/mediaserver/movies/internal/library"
)

const (
	beaconEvery = 5 * time.Second
	// An KumoBox not heard from for this long is offline.
	onlineFor = 16 * time.Second
	// How often a guest asks a host whether its library changed.
	askEvery = 30 * time.Second
	// An KumoBox that proved who it is this lately keeps its address: beacons
	// don't move it (see heard).
	verifiedFor = time.Minute
	// How often Settings (open) has the others asked again (they answer
	// from their library's version, read at most every few seconds).
	refreshEvery = 3 * time.Second
	// How many KumoBoxes only heard of (not paired, not added by hand) are kept.
	maxStrangers = 64
	// The largest request body read from an KumoBox that isn't paired.
	maxUnpairedBody = 1 << 20
	// Failed requests in a row after which a host's library is gone.
	maxFails = 3
)

// Library is what a host shares: its library's files.
type Library interface {
	All() ([]*library.LocalFile, error)
	Get(path string) (*library.LocalFile, error)
}

// History is the watch history (where each episode was stopped), which
// KumoBoxes on the same Letterboxd account keep in step (see syncHistory).
type History interface {
	Since(t int64, limit int) []*history.Entry
	Get(mediaID, episode int) *history.Entry
	After(seq int64, limit int) []*history.Entry
	Latest(limit int) []*history.Entry
	MergeAll(entries []history.Entry) ([]history.Entry, error)
}

type Service struct {
	id       *Identity
	db       *db.DB
	settings *config.Store
	library  Library
	hub      *events.Hub
	api      *http.Client // short requests to the others
	media    *http.Client // forwarded video: no time limit
	slow     *http.Client // requests a host takes long to answer (ani-cli)

	// OnFiles runs when the files shared with this KumoBox change (in the
	// background).
	OnFiles func()
	// User is the Letterboxd account this KumoBox is connected to (0: none),
	// and History its watch history: kept in step with the KumoBoxes it
	// shares with on the same account. Both nil: not kept in step.
	User    func() int
	History History
	// OnWatchedElsewhere runs with the episodes another KumoBox on the
	// account says were finished there (each KumoBox keeps its own list:
	// they move it on).
	OnWatchedElsewhere func(finished []history.Entry)
	// Server: this is mediaserver's movies side, which tells the others it
	// runs on PlatformServer (not its system): they don't ask whether to
	// share with it, nor to update it.
	Server bool

	// The library's version for the others (sharedVersion).
	versionMu sync.Mutex
	version   string
	versionAt time.Time

	pushMu    sync.Mutex
	pending   map[[2]int]history.Entry
	pushTimer *time.Timer
	// mergeMu: one entry from the others merged at a time (see merge).
	mergeMu sync.Mutex

	mu      sync.Mutex
	peers   map[string]*peer
	running *run
	// applyMu: one start or stop at a time (settings saved in a row).
	applyMu sync.Mutex
}

// run is sharing while it's on.
type run struct {
	cancel context.CancelFunc
	disc   *discovery // nil when the network can't be listened on
	wake   chan struct{}
	done   chan struct{}
}

type peer struct {
	id, name, version string
	platform          string // what it runs on ("android": a TV), as it says
	pub               []byte
	addr              net.IP
	port              int
	seen              time.Time // last heard from (beacon, request or answer)
	lastSeen          time.Time // before this run: when it was last around
	manual            string    // the address it was added with by hand
	allowed           bool      // this KumoBox shares its library with it
	// And with it (allowed), downloading onto this KumoBox.
	downloads bool

	// As a host: whether it shares its library with this KumoBox, and its files.
	shares   bool
	files    []*library.LocalFile
	filesVer string
	asked    time.Time
	asking   bool
	fails    int
	// historySeq: the last of its history entries (in the order it stored
	// them) this KumoBox has; pushedSeq: the last of this KumoBox's it sent it.
	historySeq, pushedSeq int64
	// user: the Letterboxd account it's connected to, as it told (it shares
	// with this KumoBox).
	user int
	// hostDownloads: this KumoBox may download onto it.
	hostDownloads bool
	// verified: when it last proved who it is (a request or an answer
	// signed); offset: how far its clock is ahead of this one's.
	verified time.Time
	offset   time.Duration
	// callerUser: the Letterboxd account it said it's on, asking this KumoBox.
	callerUser int
	// wakeMACs: its network cards (it shares), what wakes it (wake.go);
	// wakeUntil: it's being woken up until then.
	wakeMACs  []string
	wakeUntil time.Time
	// autoSynced: when the watch history was last synced with it by itself.
	autoSynced time.Time
	// declined: asked whether to share this KumoBox's library with it, the
	// answer was "not now" (Home doesn't ask again).
	declined bool
}

// paired: this KumoBox shares its library with it, or it with this one.
func (p *peer) paired() bool { return p.allowed || p.shares }

// displayName is its name, or what it is when it has none.
func (p *peer) displayName() string {
	if p.name != "" {
		return p.name
	}
	return "Another KumoBox"
}

// online: heard from lately. A host that shares with this KumoBox is asked
// every askEvery, and its answer counts until the next one is due: also when
// its beacons don't come through (the Wi-Fi of some TVs drops them), it
// doesn't look gone half of the time. The same the other way: a device this
// KumoBox shares with asks it as often (a hub's devices, which it never asks).
func (p *peer) online(now time.Time) bool {
	d := now.Sub(p.seen)
	return d < onlineFor || ((p.shares && p.fails == 0) || p.allowed) && d < askEvery+onlineFor
}

func (p *peer) lastAround() time.Time {
	if p.seen.After(p.lastSeen) {
		return p.seen
	}
	return p.lastSeen
}

func (p *peer) baseURL() string {
	return "http://" + net.JoinHostPort(p.addr.String(), strconv.Itoa(p.port))
}

// New loads this KumoBox's identity and the KumoBoxes it knows.
func New(d *db.DB, settings *config.Store, lib Library, hub *events.Hub) (*Service, error) {
	id, err := LoadIdentity(d)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 4 * time.Second}
	// Never through a proxy: the others are on this network.
	transport := func() *http.Transport {
		return &http.Transport{Proxy: nil, DialContext: dialer.DialContext, ResponseHeaderTimeout: time.Minute, MaxIdleConnsPerHost: 4}
	}
	s := &Service{
		id: id, db: d, settings: settings, library: lib, hub: hub,
		api:   &http.Client{Timeout: 15 * time.Second, Transport: transport()},
		media: &http.Client{Transport: transport()},
		slow:  &http.Client{Timeout: 3 * time.Minute, Transport: &http.Transport{Proxy: nil, DialContext: dialer.DialContext, MaxIdleConnsPerHost: 2}},
		peers: map[string]*peer{},
	}
	s.load()
	return s, nil
}

// ID is this KumoBox's ID.
func (s *Service) ID() string { return s.id.ID }

// Name is what the others call this KumoBox.
func (s *Service) Name() string {
	if n := cleanName(s.settings.Get().Sharing.Name); n != "" {
		return n
	}
	// As the others see it (cleanName keeps names short).
	return cleanName(Hostname())
}

// Hostname is the computer's name, without its domain (the device's, as
// the Android app gives it).
func Hostname() string {
	if n := cleanName(config.Getenv("DEVICE_NAME")); n != "" {
		return n
	}
	h, _ := os.Hostname()
	h, _, _ = strings.Cut(h, ".")
	if h == "" {
		return "KumoBox"
	}
	return h
}

// ---------------------------------------------------------------------------
// Saved peers

const peersKey = "share:peers"

type savedPeer struct {
	Name    string `json:"name"`
	Key     string `json:"key"`
	Addr    string `json:"addr"`
	Port    int    `json:"port"`
	Manual  string `json:"manual,omitempty"`
	Allowed bool   `json:"allowed,omitempty"`
	Shares  bool   `json:"shares,omitempty"`
	Seen    int64  `json:"seen"`
	// Granted to it (allowed): downloading here.
	Downloads bool `json:"downloads,omitempty"`
	// It takes this KumoBox's downloads (it shares, see downloads.go).
	TakesDownloads bool `json:"takesDownloads,omitempty"`
	// Its Letterboxd account, as it told; where the watch histories are at.
	User       int   `json:"user,omitempty"`
	CallerUser int   `json:"callerUser,omitempty"`
	HistorySeq int64 `json:"historySeq,omitempty"`
	PushedSeq  int64 `json:"pushedSeq,omitempty"`
	// Its network cards, to wake it (wake.go).
	Wake     []string `json:"wake,omitempty"`
	Platform string   `json:"platform,omitempty"`
	Declined bool     `json:"declined,omitempty"`
}

func (s *Service) load() {
	var saved map[string]savedPeer
	if ok, _ := s.db.GetKV(peersKey, &saved); !ok {
		return
	}
	for id, sp := range saved {
		pub, err := parsePublicKey(id, sp.Key)
		if err != nil {
			continue
		}
		s.peers[id] = &peer{
			id: id, name: sp.Name, pub: pub, addr: net.ParseIP(sp.Addr), port: sp.Port,
			manual: sp.Manual, allowed: sp.Allowed, shares: sp.Shares, lastSeen: time.Unix(sp.Seen, 0),
			downloads: sp.Downloads, hostDownloads: sp.TakesDownloads,
			user: sp.User, callerUser: sp.CallerUser, historySeq: sp.HistorySeq, pushedSeq: sp.PushedSeq,
			wakeMACs: cleanMACs(sp.Wake), platform: cleanPlatform(sp.Platform), declined: sp.Declined,
		}
	}
}

// save keeps the KumoBoxes worth knowing next time: those this one shares with,
// those that share with it, and those added by hand. Must be called with
// s.mu held.
func (s *Service) save() {
	out := map[string]savedPeer{}
	for id, p := range s.peers {
		if !p.allowed && !p.shares && p.manual == "" && !p.declined {
			continue
		}
		sp := savedPeer{
			Name: p.name, Key: base64.StdEncoding.EncodeToString(p.pub), Port: p.port, Manual: p.manual, Allowed: p.allowed, Shares: p.shares, Seen: p.lastAround().Unix(),
			Downloads: p.downloads, TakesDownloads: p.hostDownloads,
			User: p.user, CallerUser: p.callerUser, HistorySeq: p.historySeq, PushedSeq: p.pushedSeq,
			Wake: p.wakeMACs, Platform: p.platform, Declined: p.declined,
		}
		if p.addr != nil {
			sp.Addr = p.addr.String()
		}
		out[id] = sp
	}
	if err := s.db.SetKV(peersKey, out); err != nil {
		log.Printf("sharing: %v", err)
	}
}

// ---------------------------------------------------------------------------
// On and off

// Start follows the settings: sharing runs while it's on.
func (s *Service) Start() {
	s.apply(s.settings.Get())
	s.settings.OnChange(func(old, cur config.Settings) {
		if old.Sharing.Enabled != cur.Sharing.Enabled || old.Sharing.Name != cur.Sharing.Name || old.Server.Port != cur.Server.Port {
			// The settings now, not these: listeners run apart, and two
			// saves close together may arrive in either order.
			s.apply(s.settings.Get())
		}
	})
}

// Stop ends sharing (KumoBox is quitting).
func (s *Service) Stop() {
	s.flushPush()
	s.apply(config.Settings{})
}

func (s *Service) apply(cfg config.Settings) {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	s.mu.Lock()
	old := s.running
	s.running = nil
	s.mu.Unlock()
	if old != nil {
		old.cancel()
		if old.disc != nil {
			old.disc.close()
		}
		<-old.done
	}
	if !cfg.Sharing.Enabled {
		if old != nil {
			s.forgetLibraries()
		}
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &run{cancel: cancel, wake: make(chan struct{}, 1), done: make(chan struct{})}
	disc, err := listen()
	if err != nil {
		// KumoBoxes added by address still work.
		log.Printf("sharing: can't listen for other KumoBox apps on the network: %v", err)
	} else {
		r.disc = disc
		go disc.read(s.heard)
	}
	s.mu.Lock()
	s.running = r
	s.mu.Unlock()
	go s.loop(ctx, r)
}

// forgetLibraries drops what the hosts shared: sharing is off.
func (s *Service) forgetLibraries() {
	s.mu.Lock()
	changed := false
	for _, p := range s.peers {
		if p.files != nil {
			p.files, p.filesVer = nil, ""
			changed = true
		}
	}
	s.mu.Unlock()
	s.publish(changed)
}

func (s *Service) loop(ctx context.Context, r *run) {
	defer close(r.done)
	announce := time.NewTicker(beaconEvery)
	defer announce.Stop()
	check := time.NewTicker(2 * time.Second)
	defer check.Stop()
	s.announce(r)
	s.askHosts(ctx)
	for beacons := 1; ; {
		select {
		case <-ctx.Done():
			return
		case <-announce.C:
			if beacons++; r.disc != nil && beacons%6 == 0 {
				r.disc.join() // interfaces that came up (every 30s)
			}
			s.announce(r)
		case <-check.C:
		case <-r.wake:
		}
		s.askHosts(ctx)
	}
}

func (s *Service) beacon() beacon {
	cfg := s.settings.Get()
	return beacon{KumoBox: protocol, ID: s.id.ID, Name: s.Name(), Port: cfg.Server.Port, Key: s.id.PublicKey(), Version: config.AppVersion, Platform: s.platform()}
}

func (s *Service) announce(r *run) {
	if r.disc != nil {
		r.disc.announce(s.beacon())
	}
}

func (s *Service) wake() {
	s.mu.Lock()
	r := s.running
	s.mu.Unlock()
	if r != nil {
		select {
		case r.wake <- struct{}{}:
		default:
		}
	}
}

// heard handles a beacon from another KumoBox.
func (s *Service) heard(b beacon, from net.IP) {
	if b.ID == s.id.ID || !isLocalNetwork(from) || b.Port <= 0 || b.Port > 65535 {
		return
	}
	pub, err := parsePublicKey(b.ID, b.Key)
	if err != nil {
		return
	}
	s.mu.Lock()
	r := s.running
	p, known := s.peers[b.ID]
	if !known {
		s.makeRoom()
		p = &peer{id: b.ID, pub: pub}
		s.peers[b.ID] = p
	}
	now := time.Now()
	wasOnline := p.online(now)
	p.name, p.version, p.seen = cleanName(b.Name), b.Version, now
	if pl := cleanPlatform(b.Platform); pl != "" {
		p.platform = pl
	}
	// One added by hand keeps its address, and one that proved who it is
	// lately too: anyone can repeat its beacons from elsewhere. Unless it
	// doesn't answer there (its address changed, say): then where it says.
	moved := !p.addr.Equal(from) || p.port != b.Port
	failing := p.fails > 0
	if moved && (failing || p.manual == "" && now.Sub(p.verified) > verifiedFor) {
		p.addr, p.port = from, b.Port
		if p.allowed || p.shares {
			s.save()
		}
	}
	// Anyone can repeat a beacon: at most every 2 seconds. One that didn't
	// answer is asked again at once when it's heard from.
	ask := (b.Ask || failing && moved) && now.Sub(p.asked) > 2*time.Second
	if ask {
		p.asked = time.Time{}
	}
	s.mu.Unlock()
	if ask {
		s.wake()
	}
	if !wasOnline {
		// Just came: tell it about this one, and ask what it shares.
		if r != nil && r.disc != nil && !b.Reply {
			r.disc.reply(s.beacon(), from)
		}
		s.mu.Lock()
		p.asked = time.Time{}
		s.mu.Unlock()
		s.wake()
		s.hub.Publish("sharing-updated", nil)
	}
}

// ---------------------------------------------------------------------------
// The host's side: who asks

// Caller is another KumoBox that made a request, checked.
type Caller struct {
	ID, Name string
	// Allowed: this KumoBox shares its library with it.
	Allowed bool
	// SameUser: it's connected to the same Letterboxd account as this one.
	SameUser bool
	// Downloads: it may download onto this KumoBox (only with Allowed).
	Downloads bool
	// Paired: Allowed, or it shares its library with this KumoBox.
	Paired bool
	// User: the Letterboxd account it says it's on (0: none).
	User int
}

// Verify checks a request from another KumoBox, and notes it as seen. The
// answer (w) gets what proves it comes from this KumoBox.
func (s *Service) Verify(w http.ResponseWriter, r *http.Request) (Caller, error) {
	if !s.settings.Get().Sharing.Enabled {
		return Caller{}, errors.New("library sharing is off")
	}
	ip := remoteIP(r)
	if !isLocalNetwork(ip) {
		return Caller{}, errors.New("not from the local network")
	}
	// A large body is read (to check it) only from an KumoBox paired with this
	// one: anyone else could have it read megabytes for nothing.
	if r.ContentLength > maxUnpairedBody || r.ContentLength < 0 && r.Body != nil && r.Body != http.NoBody {
		s.mu.Lock()
		p := s.peers[r.Header.Get(headerID)]
		paired := p != nil && p.paired()
		s.mu.Unlock()
		if !paired {
			return Caller{}, errors.New("too large")
		}
	}
	now := time.Now()
	c, sig, err := s.id.verify(r, now)
	if sig != nil {
		date := now.UTC().Format(http.TimeFormat)
		if reply, err := sig.reply(date); err == nil {
			w.Header().Set("Date", date)
			w.Header().Set(HeaderReply, reply)
		}
	}
	if err != nil {
		return Caller{}, err
	}
	s.mu.Lock()
	p, known := s.peers[c.id]
	if !known {
		s.makeRoom()
		p = &peer{id: c.id, pub: c.pub}
		s.peers[c.id] = p
	}
	wasOnline := p.online(time.Now())
	if c.name != "" {
		p.name = c.name
	}
	// Where it answers, unless it was added by hand, or the request is a
	// ticket (mpv's, repeated for hours, maybe from another address).
	if !c.ticket {
		if c.port > 0 && c.port <= 65535 && p.manual == "" {
			p.addr, p.port = ip, c.port
		}
		p.verified = now
	}
	p.seen = now
	if p.callerUser != c.user {
		p.callerUser = c.user
		if p.paired() {
			s.save()
		}
	}
	caller := Caller{ID: p.id, Name: p.name, Allowed: p.allowed, Downloads: p.allowed && p.downloads, Paired: p.paired()}
	s.mu.Unlock()
	if !wasOnline {
		s.hub.Publish("sharing-updated", nil)
		// Back (or just started): where each left off, in step.
		if caller.Paired && !c.ticket {
			s.autoSync(p)
		}
	}
	me := s.user()
	caller.User = c.user
	caller.SameUser = me > 0 && c.user == me
	return caller, nil
}

func (s *Service) user() int {
	if s.User == nil {
		return 0
	}
	return s.User()
}

// HistorySince is this KumoBox's watch history after t, for another KumoBox on
// the same account (checked by the caller).
func (s *Service) HistorySince(c Caller, t int64) []*history.Entry {
	h := s.History
	if h == nil {
		return []*history.Entry{}
	}
	out := h.Since(t, historyBatch)
	if out == nil {
		out = []*history.Entry{}
	}
	return out
}

// HistoryAfter is this KumoBox's watch history stored after seq (see
// history.Store.After), for another KumoBox on the same account.
func (s *Service) HistoryAfter(c Caller, seq int64) []*history.Entry {
	h := s.History
	if h == nil {
		return []*history.Entry{}
	}
	out := h.After(seq, historyBatch)
	if out == nil {
		out = []*history.Entry{}
	}
	return out
}

// HistoryLatest is the latest of this KumoBox's watch history (Sync).
func (s *Service) HistoryLatest(c Caller) []*history.Entry {
	h := s.History
	if h == nil {
		return []*history.Entry{}
	}
	out := h.Latest(historyBatch)
	if out == nil {
		out = []*history.Entry{}
	}
	return out
}

const historyBatch = 5000

func remoteIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return net.ParseIP(host)
}

// Hello is the host's answer to a guest asking what it shares.
// PlatformServer is the platform mediaserver's movies side says it runs on.
const PlatformServer = "mediaserver"

// platform is what this KumoBox tells the others it runs on.
func (s *Service) platform() string {
	if s.Server {
		return PlatformServer
	}
	return runtime.GOOS
}

type Hello struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Key     string `json:"key"`
	Version string `json:"version"` // KumoBox's
	// Platform: what it runs on (runtime.GOOS: "android" on a TV).
	Platform string `json:"platform,omitempty"`
	Shares   bool   `json:"shares"`
	// Files changes when the shared files do.
	Files string `json:"files,omitempty"`
	// User: the Letterboxd account it's connected to, told to those it shares
	// with (their watch histories are kept in step on the same account).
	User int `json:"user,omitempty"`
	// Downloads: the guest may download onto it (downloads.go).
	Downloads bool `json:"downloads,omitempty"`
	// Wake: its network cards, to wake it when it's asleep (wake.go).
	Wake []string `json:"wake,omitempty"`
}

// Hello answers a guest (verified, see Verify).
func (s *Service) Hello(c Caller) Hello {
	h := Hello{ID: s.id.ID, Name: s.Name(), Key: s.id.PublicKey(), Version: config.AppVersion, Platform: s.platform(), Shares: c.Allowed}
	if c.Allowed {
		h.Files = s.sharedVersion()
		h.User = s.user()
		h.Downloads = c.Downloads
		h.Wake = macs()
	}
	return h
}

// Whoami is what anyone on the network may ask: who this KumoBox is (what its
// beacons say).
func (s *Service) Whoami() Hello {
	return Hello{ID: s.id.ID, Name: s.Name(), Key: s.id.PublicKey(), Version: config.AppVersion, Platform: s.platform()}
}

// SharedFiles are the library's files this KumoBox shares: those matched to a
// movie or show.
func (s *Service) SharedFiles() ([]*library.LocalFile, error) {
	all, err := s.library.All()
	if err != nil {
		return nil, err
	}
	out := make([]*library.LocalFile, 0, len(all))
	for _, f := range all {
		if f.MediaID != 0 && !f.Ignored && !IsRemote(f.Path) {
			out = append(out, f)
		}
	}
	return out, nil
}

// sharedVersion is filesVersion of SharedFiles, read again at most every
// few seconds: each KumoBox it shares with asks for it.
func (s *Service) sharedVersion() string {
	s.versionMu.Lock()
	defer s.versionMu.Unlock()
	if time.Since(s.versionAt) < 5*time.Second && s.version != "" {
		return s.version
	}
	files, _ := s.SharedFiles()
	s.version, s.versionAt = filesVersion(files), time.Now()
	return s.version
}

// Shares reports a file this KumoBox shares (see SharedFiles).
func (s *Service) Shares(path string) bool {
	if path == "" || IsRemote(path) {
		return false
	}
	f, err := s.library.Get(path)
	return err == nil && f != nil && f.MediaID != 0 && !f.Ignored
}

func filesVersion(files []*library.LocalFile) string {
	h := sha256.New()
	for _, f := range files {
		fmt.Fprintf(h, "%s|%d|%d|%d|%s|%d\n", f.Path, f.MediaID, f.Episode, f.Size, f.Kind, f.ModTime)
	}
	return hex.EncodeToString(h.Sum(nil)[:12])
}

// ---------------------------------------------------------------------------
// The guest's side: asking the hosts

func (s *Service) askHosts(ctx context.Context) {
	now := time.Now()
	s.mu.Lock()
	var due []*peer
	for _, p := range s.peers {
		if p.asking || p.addr == nil || p.port == 0 {
			continue
		}
		// Hosts that are around, or that were: until they stop answering.
		if !p.online(now) && p.manual == "" && p.files == nil && !p.shares {
			continue
		}
		wait := askEvery
		switch {
		case p.waking(now):
			wait = 3 * time.Second
		case p.fails > 0:
			wait = 10 * time.Second
		}
		if now.Sub(p.asked) >= wait {
			p.asking, p.asked = true, now
			due = append(due, p)
		}
	}
	s.mu.Unlock()
	for _, p := range due {
		go s.ask(ctx, p)
	}
}

// ask asks a host whether it shares its library with this KumoBox, and for its
// files when they changed.
func (s *Service) ask(ctx context.Context, p *peer) {
	h, err := s.hello(ctx, p)
	var files []*library.LocalFile
	if err == nil && h.Shares {
		s.mu.Lock()
		same := h.Files == p.filesVer && p.files != nil
		s.mu.Unlock()
		if !same {
			files, err = s.fetchFiles(ctx, p)
		}
	}
	s.mu.Lock()
	p.asking = false
	if ctx.Err() != nil {
		// Sharing stopped or started over meanwhile (its settings
		// changed): the next run asks again at once, not 30s later.
		p.asked = time.Time{}
		s.mu.Unlock()
		return
	}
	changed, visible := false, false
	if err != nil {
		p.fails++
		// Added by name: maybe at another address now (a new DHCP lease).
		if p.manual != "" && p.fails%2 == 0 {
			go s.resolveManual(p)
		}
		if p.fails == maxFails {
			visible = true
			if p.files != nil {
				p.files, p.filesVer, changed = nil, "", true
			}
		}
		first, name, base := p.fails == 1, p.name, p.baseURL()
		s.mu.Unlock()
		if first {
			log.Printf("sharing: %s (%s): %v", name, base, err)
		}
		s.publish(changed)
		if visible {
			s.hub.Publish("sharing-updated", nil)
		}
		return
	}
	// It answered again after it didn't (or for the first time this run).
	back := p.fails > 0 || !p.online(time.Now())
	visible = back || p.shares != h.Shares || (p.name != h.Name && h.Name != "") || p.waking(time.Now())
	p.fails, p.seen, p.wakeUntil = 0, time.Now(), time.Time{}
	// Its cards, to wake it later (an older KumoBox doesn't say).
	if cards := cleanMACs(h.Wake); h.Shares && len(cards) > 0 && strings.Join(cards, ",") != strings.Join(p.wakeMACs, ",") {
		p.wakeMACs = cards
		s.save()
	}
	if h.Name != "" {
		p.name = cleanName(h.Name)
	}
	p.version = h.Version
	if pl := cleanPlatform(h.Platform); pl != "" {
		p.platform = pl
	}
	if p.shares != h.Shares {
		p.shares = h.Shares
		s.save()
	}
	switch {
	case !h.Shares && p.files != nil:
		p.files, p.filesVer, changed = nil, "", true
	case h.Shares && files != nil:
		p.files, p.filesVer, changed = remoteFiles(p, files), h.Files, true
	}
	s.mu.Unlock()
	s.publish(changed)
	if visible || changed {
		s.hub.Publish("sharing-updated", nil)
	}
	s.mu.Lock()
	was := p.user
	p.user = 0
	takes := false
	if h.Shares {
		p.user = h.User
		takes = h.Downloads
	}
	// Another account there now: its history is asked for from the start
	// (where this KumoBox was is the other account's).
	if p.user != was && p.user > 0 && was > 0 {
		p.historySeq, p.pushedSeq = 0, 0
		s.save()
	}
	took := p.hostDownloads
	p.hostDownloads = takes
	if took != takes {
		s.save()
	}
	s.mu.Unlock()
	if took != takes {
		s.downloadsGranted(p, takes)
		s.hub.Publish("sharing-updated", nil)
	}
	if h.Shares && h.User > 0 && h.User == s.user() {
		s.syncHistory(ctx, p)
		s.pushMissed(ctx, p)
	}
	if back {
		s.autoSync(p)
	}
}

// HistoryChanged sends a position this KumoBox saved to the KumoBoxes that share
// with it on the same Letterboxd account (the one whose files are played,
// most of all), as it happens: their Continue watching goes on from it.
// Gathered for 2 seconds; at once when the episode is over.
func (s *Service) HistoryChanged(e history.Entry) {
	s.mu.Lock()
	on := s.running != nil
	s.mu.Unlock()
	if !on || s.user() == 0 {
		return
	}
	s.pushMu.Lock()
	defer s.pushMu.Unlock()
	if s.pending == nil {
		s.pending = map[[2]int]history.Entry{}
	}
	s.pending[[2]int{e.MediaID, e.Episode}] = e
	wait := 2 * time.Second
	if e.Percent() >= 0.85 {
		wait = 0
	}
	if s.pushTimer == nil {
		s.pushTimer = time.AfterFunc(wait, s.pushHistory)
	} else if wait == 0 {
		s.pushTimer.Reset(0)
	}
}

func (s *Service) pushHistory() {
	s.pushMu.Lock()
	entries := make([]history.Entry, 0, len(s.pending))
	for _, e := range s.pending {
		entries = append(entries, e)
	}
	s.pending, s.pushTimer = nil, nil
	s.pushMu.Unlock()
	me := s.user()
	if len(entries) == 0 || me == 0 {
		return
	}
	s.mu.Lock()
	var to []*peer
	for _, p := range s.peers {
		if p.shares && p.user == me && p.addr != nil && p.fails < maxFails {
			to = append(to, p)
		}
	}
	s.mu.Unlock()
	body, _ := json.Marshal(entries)
	var wg sync.WaitGroup
	for _, p := range to {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = s.sendHistory(ctx, p, body)
		}()
	}
	wg.Wait()
}

// flushPush sends the positions waiting to go (pushHistory) now: KumoBox is
// closing, and the last one saved would be lost otherwise.
func (s *Service) flushPush() {
	s.pushMu.Lock()
	if s.pushTimer != nil {
		s.pushTimer.Stop()
	}
	waiting := len(s.pending) > 0
	s.pushMu.Unlock()
	if waiting {
		s.pushHistory()
	}
}

// pushMissed sends a host on the same account what this KumoBox stored since
// the last it sent it: what didn't reach it as it was saved (it was away),
// or came from another KumoBox.
func (s *Service) pushMissed(ctx context.Context, p *peer) {
	if s.History == nil {
		return
	}
	s.mu.Lock()
	after := p.pushedSeq
	s.mu.Unlock()
	entries := s.History.After(after, historyBatch)
	if len(entries) == 0 {
		return
	}
	body, err := json.Marshal(entries)
	if err != nil || s.sendHistory(ctx, p, body) != nil {
		return
	}
	s.mu.Lock()
	p.pushedSeq = max(p.pushedSeq, entries[len(entries)-1].Seq)
	s.save()
	s.mu.Unlock()
}

// sendHistory sends positions (JSON) to an KumoBox on the same account.
func (s *Service) sendHistory(ctx context.Context, p *peer, body []byte) error {
	req, err := s.request(ctx, p, http.MethodPost, "/api/peer/history", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.do(s.api, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return answerError(resp)
}

// answerError is what an KumoBox's answer (not OK) says went wrong, in words.
func answerError(resp *http.Response) error {
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	var e struct {
		Error string `json:"error"`
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if json.Unmarshal(raw, &e) != nil || e.Error == "" {
		e.Error = resp.Status
	}
	return errors.New(e.Error)
}

// Paired names the KumoBoxes this one is paired with (it shares its library
// with them, or they with it), with sharing on.
func (s *Service) Paired() []string {
	if !s.settings.Get().Sharing.Enabled {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, p := range s.peers {
		if p.paired() {
			out = append(out, p.displayName())
		}
	}
	sort.Strings(out)
	return out
}

// SyncResult is what SyncNow did.
type SyncResult struct {
	// Devices: the KumoBoxes synced with. Unreachable: those that couldn't be
	// (why, after the name). Skipped: paired KumoBoxes not synced with (why).
	Devices     []string `json:"devices"`
	Unreachable []string `json:"unreachable"`
	Skipped     []string `json:"skipped"`
	// Updated: the positions that changed here.
	Updated int `json:"updated"`
}

// SyncNow brings this KumoBox's watch history in step with the KumoBoxes it's
// paired with on the same Letterboxd account, both ways, now: each takes what
// the other has newer (the latest stored on each), and Continue watching
// goes on from where an episode was stopped on any of them. As it goes, a
// guest takes its hosts' positions every askEvery and sends them its own,
// which this does at once.
func (s *Service) SyncNow(ctx context.Context) (SyncResult, error) {
	res := SyncResult{Devices: []string{}, Unreachable: []string{}, Skipped: []string{}}
	me := s.user()
	if s.History == nil || !s.settings.Get().Sharing.Enabled {
		return res, nil
	}
	s.mu.Lock()
	var with []*peer
	for _, p := range s.peers {
		if !p.paired() || me == 0 {
			continue
		}
		switch skip, away := syncable(p, me); {
		case skip != "":
			res.Skipped = append(res.Skipped, p.displayName()+" ("+skip+")")
		case away != "":
			res.Unreachable = append(res.Unreachable, p.displayName()+" ("+away+")")
		default:
			with = append(with, p)
		}
	}
	s.mu.Unlock()
	if me == 0 {
		if len(s.Paired()) > 0 {
			return res, errors.New("connect Letterboxd: watch history is kept in step between devices on the same Letterboxd account")
		}
		return res, nil
	}
	body, err := json.Marshal(s.History.Latest(historyBatch))
	if err != nil {
		return res, err
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, p := range with {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.mu.Lock()
			name := p.displayName()
			s.mu.Unlock()
			merged, err := s.syncWith(ctx, p, body)
			if err != nil {
				log.Printf("sharing: sync with %s: %v", name, err)
				mu.Lock()
				res.Unreachable = append(res.Unreachable, name+" ("+plainError(err)+")")
				mu.Unlock()
				return
			}
			mu.Lock()
			res.Devices = append(res.Devices, name)
			res.Updated += merged
			mu.Unlock()
		}()
	}
	wg.Wait()
	sort.Strings(res.Devices)
	sort.Strings(res.Unreachable)
	sort.Strings(res.Skipped)
	if res.Updated > 0 {
		s.hub.Publish("history-updated", nil)
	}
	return res, nil
}

// syncable: whether this KumoBox's watch history can be synced with a paired
// one (this one on Letterboxd account me): why not (skip), or why it can't be
// reached. Must be called with mu held.
func syncable(p *peer, me int) (skip, away string) {
	user := p.user
	if user == 0 {
		user = p.callerUser
	}
	switch {
	case user == 0:
		return "it hasn't said its Letterboxd account yet: open KumoBox there", ""
	case user != me:
		return "another Letterboxd account", ""
	case p.addr == nil || p.port <= 0:
		return "", "its address isn't known yet"
	}
	return "", ""
}

// syncWith takes a paired KumoBox's latest positions and sends it this one's
// (body), and returns how many changed here.
func (s *Service) syncWith(ctx context.Context, p *peer, body []byte) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var theirs []*history.Entry
	if err := s.getJSON(ctx, p, "/api/peer/history?latest=1", &theirs); err != nil {
		return 0, err
	}
	if err := s.sendHistory(ctx, p, body); err != nil {
		return 0, err
	}
	return s.merge(theirs), nil
}

// How often at most a paired KumoBox is synced with by itself (autoSync).
const autoSyncEvery = 2 * time.Minute

// autoSync syncs the watch history with a paired KumoBox that came back (it
// answered again, or asked), as Sync would, unless that's turned off or it
// was done a moment ago: Continue watching goes on from where it was left on
// it, without pressing Sync.
func (s *Service) autoSync(p *peer) {
	me := s.user()
	if s.History == nil || me == 0 || !s.settings.Get().Sharing.AutoSync {
		return
	}
	s.mu.Lock()
	skip, away := syncable(p, me)
	due := p.paired() && skip == "" && away == "" && time.Since(p.autoSynced) >= autoSyncEvery && s.running != nil
	if due {
		p.autoSynced = time.Now()
	}
	name := p.displayName()
	s.mu.Unlock()
	if !due {
		return
	}
	go func() {
		body, err := json.Marshal(s.History.Latest(historyBatch))
		if err != nil {
			return
		}
		merged, err := s.syncWith(context.Background(), p, body)
		if err != nil {
			log.Printf("sharing: syncing with %s: %v", name, plainError(err))
			return
		}
		if merged > 0 {
			log.Printf("sharing: synced %d position(s) with %s", merged, name)
			s.hub.Publish("history-updated", nil)
		}
	}()
}

// Gone marks another KumoBox as not heard from for a while (tests: as if it
// had been away).
func (s *Service) Gone(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.peers[id]; p != nil {
		p.seen, p.lastSeen = time.Time{}, time.Time{}
	}
}

// AutoSyncAll syncs with every paired KumoBox by itself (autoSync), as when
// the app is opened again.
func (s *Service) AutoSyncAll() {
	s.mu.Lock()
	var all []*peer
	for _, p := range s.peers {
		if p.paired() {
			all = append(all, p)
		}
	}
	s.mu.Unlock()
	for _, p := range all {
		s.autoSync(p)
	}
}

// plainError says what went wrong talking to another KumoBox, in a few words.
func plainError(err error) string {
	var ne net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()):
		return "it didn't answer in time"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "KumoBox isn't running there"
	case errors.Is(err, syscall.EHOSTUNREACH) || errors.Is(err, syscall.ENETUNREACH):
		return "it isn't on the network"
	}
	var oe *net.OpError
	if errors.As(err, &oe) {
		return "it isn't reachable"
	}
	// Not the request's address (a file's path on that computer, say).
	var ue *url.Error
	if errors.As(err, &ue) {
		if errors.Is(ue.Err, io.EOF) || errors.Is(ue.Err, io.ErrUnexpectedEOF) {
			return "the connection closed"
		}
		return ue.Err.Error()
	}
	return err.Error()
}

// TakeHistory merges positions another KumoBox on the same account sent
// (checked by the caller), the newer ones.
func (s *Service) TakeHistory(c Caller, entries []history.Entry) {
	if s.History == nil {
		return
	}
	merged, finished := s.mergeAll(entries)
	if len(merged) > 0 {
		s.hub.Publish("history-updated", nil)
	}
	s.watchedElsewhere(finished)
}

// finishedAt is how far into an episode it counts as finished.
const finishedAt = 0.85

// watchedElsewhere tells the app about episodes finished on another
// KumoBox, if any.
func (s *Service) watchedElsewhere(finished []history.Entry) {
	if len(finished) > 0 && s.OnWatchedElsewhere != nil {
		go s.OnWatchedElsewhere(finished)
	}
}

// merge takes positions from another KumoBox, the newer ones, and says how
// many changed here. The episodes finished there move the list on here
// (watchedElsewhere).
func (s *Service) merge(entries []*history.Entry) int {
	in := make([]history.Entry, 0, len(entries))
	for _, e := range entries {
		if e != nil {
			in = append(in, *e)
		}
	}
	merged, finished := s.mergeAll(in)
	s.watchedElsewhere(finished)
	return len(merged)
}

// mergeAll takes the entries newer than this KumoBox's, and reports the
// ones taken and, of those, the ones that finished a viewing: the episode
// wasn't finished here yet, or was by an earlier viewing (finished longer
// ago than the episode lasts). The positions sent while the end credits play
// are the same viewing: the list moves on once for it.
func (s *Service) mergeAll(entries []history.Entry) (merged, finished []history.Entry) {
	s.mergeMu.Lock()
	defer s.mergeMu.Unlock()
	before := make(map[[2]int]*history.Entry, len(entries))
	for _, e := range entries {
		k := [2]int{e.MediaID, e.Episode}
		if _, ok := before[k]; !ok {
			before[k] = s.History.Get(e.MediaID, e.Episode)
		}
	}
	merged, err := s.History.MergeAll(entries)
	if err != nil {
		log.Printf("sharing: history: %v", err)
	}
	for _, e := range merged {
		if e.Percent() < finishedAt {
			continue
		}
		b := before[[2]int{e.MediaID, e.Episode}]
		if b == nil || b.Percent() < finishedAt || float64(e.UpdatedAt-b.UpdatedAt) > max(e.Duration, b.Duration) {
			finished = append(finished, e)
		}
	}
	return merged, finished
}

// syncHistory takes what's newer in a host's watch history, both being
// connected to the same Letterboxd account: where each episode was stopped,
// and when it was watched. Continue watching then goes on from there, to the
// second, and the episodes finished there move the list on. It asks for
// what the host stored after the last it took, whatever their times (one
// from another KumoBox comes late with an older time).
func (s *Service) syncHistory(ctx context.Context, p *peer) {
	if s.History == nil {
		return
	}
	s.mu.Lock()
	after := p.historySeq
	s.mu.Unlock()
	var entries []*history.Entry
	if err := s.getJSON(ctx, p, "/api/peer/history?after="+strconv.FormatInt(after, 10), &entries); err != nil {
		return
	}
	merged := s.merge(entries)
	last := after
	for _, e := range entries {
		if e != nil {
			last = max(last, e.Seq)
		}
	}
	s.mu.Lock()
	if last > p.historySeq {
		p.historySeq = last
		s.save()
	}
	s.mu.Unlock()
	if merged > 0 {
		s.hub.Publish("history-updated", nil)
	}
}

// publish tells the app the shared files changed.
func (s *Service) publish(changed bool) {
	if changed {
		s.hub.Publish("library-updated", nil)
		if s.OnFiles != nil {
			go s.OnFiles()
		}
	}
}

// request makes a signed request to a host (sent with do).
func (s *Service) request(ctx context.Context, p *peer, method, path string, body io.Reader) (*http.Request, error) {
	s.mu.Lock()
	base := p.baseURL()
	s.mu.Unlock()
	req, err := http.NewRequestWithContext(ctx, method, base+path, body)
	if err != nil {
		return nil, err
	}
	return s.sign(req, p)
}

type signedKey struct{}

// signed is a request signed for a host: what checks its answer.
type signed struct {
	p      *peer
	sig    *signature
	offset time.Duration
}

// sign signs a request for a host, by its clock as far as this KumoBox knows it.
func (s *Service) sign(req *http.Request, p *peer) (*http.Request, error) {
	s.mu.Lock()
	host, pub, off := p.id, p.pub, p.offset
	user := s.user()
	s.mu.Unlock()
	sig, err := s.id.sign(req, host, pub, s.Name(), s.settings.Get().Server.Port, user, time.Now().Add(off), false)
	if err != nil {
		return nil, err
	}
	return req.WithContext(context.WithValue(req.Context(), signedKey{}, &signed{p: p, sig: sig, offset: off})), nil
}

// do sends a request made by request, and checks its answer comes from the
// host: a computer that took its address can't answer for it. A host whose
// clock is far from this one's refuses the request, with its time (proven):
// it's sent once more, by that time.
func (s *Service) do(c *http.Client, req *http.Request) (*http.Response, error) {
	for try := 0; ; try++ {
		sr, _ := req.Context().Value(signedKey{}).(*signed)
		if sr == nil {
			return nil, errors.New("unsigned request")
		}
		sent := time.Now()
		resp, err := c.Do(req)
		if err != nil {
			return nil, err
		}
		date := resp.Header.Get("Date")
		want, err := sr.sig.reply(date)
		if err != nil || resp.Header.Get(HeaderReply) == "" || !hmac.Equal([]byte(want), []byte(resp.Header.Get(HeaderReply))) {
			resp.Body.Close()
			return nil, fmt.Errorf("the answer can't be checked as %s's (is the latest KumoBox running there, with library sharing on?)", s.nameOf(sr.p))
		}
		// The host dates its answer as it gets the request (Verify), however
		// long the answer then takes (a file read from a sleeping disk):
		// against when it was sent, not when the answer came.
		off := sr.offset
		if t, err := http.ParseTime(date); err == nil {
			off = t.Sub(sent).Round(time.Second)
		}
		s.mu.Lock()
		sr.p.verified, sr.p.offset = time.Now(), off
		s.mu.Unlock()
		moved := off-sr.offset > clockSkew/2 || sr.offset-off > clockSkew/2
		if resp.StatusCode != http.StatusUnauthorized || !moved || try > 0 {
			return resp, nil
		}
		resp.Body.Close()
		again := req.Clone(req.Context())
		if req.GetBody != nil {
			if again.Body, err = req.GetBody(); err != nil {
				return nil, err
			}
		}
		if req, err = s.sign(again, sr.p); err != nil {
			return nil, err
		}
	}
}

// Sign signs a request to the KumoBox with that ID and public key (as its
// whoami tells them).
func (s *Service) Sign(req *http.Request, hostID, hostKey string) error {
	pub, err := parsePublicKey(hostID, hostKey)
	if err != nil {
		return err
	}
	_, err = s.id.sign(req, hostID, pub, s.Name(), s.settings.Get().Server.Port, s.user(), time.Now(), false)
	return err
}

func (s *Service) getJSON(ctx context.Context, p *peer, path string, out any) error {
	return s.getJSONWith(ctx, s.api, p, path, out)
}

func (s *Service) getJSONWith(ctx context.Context, c *http.Client, p *peer, path string, out any) error {
	req, err := s.request(ctx, p, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	resp, err := s.do(c, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(out)
}

func (s *Service) hello(ctx context.Context, p *peer) (*Hello, error) {
	var h Hello
	if err := s.getJSON(ctx, p, "/api/peer/hello", &h); err != nil {
		return nil, err
	}
	// Another KumoBox at that address now (a new IP address from the router).
	if h.ID != p.id {
		return nil, fmt.Errorf("another KumoBox (%s) answers there", h.Name)
	}
	return &h, nil
}

func (s *Service) fetchFiles(ctx context.Context, p *peer) ([]*library.LocalFile, error) {
	var files []*library.LocalFile
	if err := s.getJSON(ctx, p, "/api/peer/files", &files); err != nil {
		return nil, err
	}
	if files == nil {
		files = []*library.LocalFile{}
	}
	return files, nil
}

// remoteFiles names a host's files after it (kumobox://<host>/<path>).
func remoteFiles(p *peer, files []*library.LocalFile) []*library.LocalFile {
	out := make([]*library.LocalFile, 0, len(files))
	for _, f := range files {
		if f == nil || f.Path == "" || f.MediaID == 0 {
			continue
		}
		c := *f
		c.Path = RemotePath(p.id, f.Path)
		c.Dir = ""
		c.Host, c.HostName = p.id, p.name
		out = append(out, &c)
	}
	return out
}

// ---------------------------------------------------------------------------
// Shared files' paths

const scheme = "kumobox://"

// RemotePath is the path of a host's file.
func RemotePath(host, path string) string { return scheme + host + "/" + path }

// IsRemote reports a path of another KumoBox's file.
func IsRemote(path string) bool { return strings.HasPrefix(path, scheme) }

// ParsePath splits a shared file's path into its host and its path there.
func ParsePath(path string) (host, onHost string, ok bool) {
	rest, ok := strings.CutPrefix(path, scheme)
	if !ok {
		return "", "", false
	}
	host, onHost, ok = strings.Cut(rest, "/")
	if !ok || host == "" || onHost == "" {
		return "", "", false
	}
	return host, onHost, true
}

// ---------------------------------------------------------------------------
// What the app shows

// PeerView is another KumoBox on the network, for Settings.
type PeerView struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Address  string `json:"address"`
	Online   bool   `json:"online"`
	LastSeen int64  `json:"lastSeen"`
	Version  string `json:"version"`
	// Platform: what it runs on ("android": a TV; "" when it didn't say).
	Platform string `json:"platform,omitempty"`
	// Allowed: this KumoBox shares its library with it.
	Allowed bool `json:"allowed"`
	// Shares: it shares its library with this KumoBox (Files of them).
	Shares bool   `json:"shares"`
	Files  int    `json:"files"`
	Manual bool   `json:"manual"`
	Error  string `json:"error,omitempty"`
	// Granted to it (with Allowed): downloading onto this KumoBox.
	Downloads bool `json:"downloads"`
	// Granted by it (it shares): downloading onto it.
	TakesDownloads bool `json:"takesDownloads"`
	// It shares and can be woken up (wake.go); it's being woken.
	CanWake bool `json:"canWake"`
	Waking  bool `json:"waking"`
	// Declined: asked whether to share with it, the answer was "not now".
	Declined bool `json:"declined"`
}

func (s *Service) view(p *peer, now time.Time) PeerView {
	v := PeerView{
		ID: p.id, Name: p.name, Online: p.online(now), LastSeen: p.lastAround().Unix(), Version: p.version,
		Allowed: p.allowed, Shares: p.shares, Files: len(p.files), Manual: p.manual != "",
		Downloads: p.downloads, TakesDownloads: p.shares && p.hostDownloads,
		CanWake: p.shares && len(p.wakeMACs) > 0, Waking: p.waking(now),
		Platform: p.platform, Declined: p.declined,
	}
	if p.addr != nil {
		v.Address = net.JoinHostPort(p.addr.String(), strconv.Itoa(p.port))
	}
	v.Name = s.shownName(p)
	if p.fails >= maxFails {
		v.Error = "Not answering"
	}
	return v
}

// shownName is a peer's name as KumoBox shows it, in lists and in what it
// says: two named alike (two Fire TVs) are told apart. Must be called with
// mu held.
func (s *Service) shownName(p *peer) string {
	name := p.name
	if name == "" {
		return "KumoBox " + p.id[:6]
	}
	for _, o := range s.peers {
		if o != p && o.name != "" && strings.EqualFold(o.name, p.name) {
			return name + " (" + p.id[:4] + ")"
		}
	}
	return name
}

// Status is sharing as Settings shows it.
type Status struct {
	Enabled bool       `json:"enabled"`
	ID      string     `json:"id"`
	Name    string     `json:"name"`
	Peers   []PeerView `json:"peers"`
	// Listening: other KumoBoxes can find this one (the network port could be
	// opened).
	Listening bool `json:"listening"`
	// Addresses: where the others reach this KumoBox (to add it by hand).
	Addresses []string `json:"addresses"`
}

func (s *Service) Status() Status {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{Enabled: s.running != nil, ID: s.id.ID, Name: s.Name(), Listening: s.running != nil && s.running.disc != nil, Peers: []PeerView{}, Addresses: addresses(s.settings.Get().Server.Port)}
	for _, p := range s.peers {
		// Those never heard from again, nor useful, aren't listed.
		if !p.online(now) && !p.allowed && !p.shares && p.manual == "" {
			continue
		}
		st.Peers = append(st.Peers, s.view(p, now))
	}
	sort.Slice(st.Peers, func(i, j int) bool {
		a, b := st.Peers[i], st.Peers[j]
		if a.Online != b.Online {
			return a.Online
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	return st
}

// addresses are this computer's addresses on the home network.
func addresses(port int) []string {
	out := []string{}
	for _, ifi := range interfaces() {
		addrs, _ := ifi.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil && isLocalNetwork(ipn.IP) && !ipn.IP.IsLinkLocalUnicast() {
				out = append(out, net.JoinHostPort(ipn.IP.String(), strconv.Itoa(port)))
			}
		}
	}
	return out
}

// Grants is what this KumoBox shares with another one: its library, and with
// it downloading onto this KumoBox. Nil: unchanged.
type Grants struct {
	Allowed   *bool `json:"allowed"`
	Downloads *bool `json:"downloads"`
	// Declined: "not now", asked whether to share with it (Home).
	Declined *bool `json:"declined"`
}

// SetAllowed shares this KumoBox's library with another one, or stops.
func (s *Service) SetAllowed(id string, allowed bool) error {
	return s.SetGrants(id, Grants{Allowed: &allowed})
}

// SetGrants changes what this KumoBox shares with another one.
func (s *Service) SetGrants(id string, g Grants) error {
	s.mu.Lock()
	p, ok := s.peers[id]
	if ok {
		if g.Allowed != nil {
			p.allowed = *g.Allowed
			// Not shared with any more: nor what came with it, which
			// sharing again mustn't give back unasked.
			if !p.allowed {
				p.downloads = false
			}
		}
		if g.Downloads != nil {
			p.downloads = *g.Downloads
		}
		if g.Declined != nil {
			p.declined = *g.Declined
		}
		// Shared with after all: the "not now" is over.
		if p.allowed {
			p.declined = false
		}
		s.save()
	}
	s.mu.Unlock()
	if !ok {
		return errors.New("no KumoBox app with that ID")
	}
	s.hub.Publish("sharing-updated", nil)
	// Tell it to ask again now (it does every 30s anyway).
	s.mu.Lock()
	r := s.running
	s.mu.Unlock()
	if r != nil && r.disc != nil && p.addr != nil {
		b := s.beacon()
		b.Ask = true
		r.disc.reply(b, p.addr)
	}
	return nil
}

// Refresh asks the hosts again what they share, now (Settings or the
// library page is open).
func (s *Service) Refresh() {
	now := time.Now()
	s.mu.Lock()
	for _, p := range s.peers {
		if now.Sub(p.asked) > refreshEvery {
			p.asked = time.Time{}
		}
	}
	s.mu.Unlock()
	s.wake()
}

// resolveManual finds an KumoBox added by name (not an address) again.
func (s *Service) resolveManual(p *peer) {
	s.mu.Lock()
	manual := p.manual
	s.mu.Unlock()
	host, portStr, err := net.SplitHostPort(manual)
	if err != nil || net.ParseIP(host) != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)
	if err != nil || len(ips) == 0 || !isLocalNetwork(ips[0]) {
		return
	}
	port, _ := strconv.Atoi(portStr)
	s.mu.Lock()
	if p.manual == manual && !p.addr.Equal(ips[0]) {
		p.addr, p.port, p.asked = ips[0], port, time.Time{}
		s.save()
		log.Printf("sharing: %s is at %s now", manual, ips[0])
	}
	s.mu.Unlock()
	s.wake()
}

// makeRoom keeps the KumoBoxes this one only heard of to a few: anyone on the
// network can make up new ones. Must be called with mu held.
func (s *Service) makeRoom() {
	var strangers []*peer
	for _, p := range s.peers {
		if !p.paired() && p.manual == "" {
			strangers = append(strangers, p)
		}
	}
	if len(strangers) < maxStrangers {
		return
	}
	oldest := strangers[0]
	for _, p := range strangers[1:] {
		if p.lastAround().Before(oldest.lastAround()) {
			oldest = p
		}
	}
	delete(s.peers, oldest.id)
}

// Forget removes an KumoBox from the list (one added by hand, or gone).
func (s *Service) Forget(id string) {
	s.mu.Lock()
	p, ok := s.peers[id]
	if ok {
		delete(s.peers, id)
		s.save()
	}
	s.mu.Unlock()
	if !ok {
		return
	}
	// Nothing of it stays: where downloads went.
	if cfg := s.settings.Get(); cfg.Sharing.DownloadTo == id {
		cfg.Sharing.DownloadTo = ""
		if _, err := s.settings.Save(cfg); err != nil {
			log.Printf("sharing: %v", err)
		}
	}
	s.publish(p.files != nil)
	s.hub.Publish("sharing-updated", nil)
}

// Connect adds an KumoBox by its address, for networks where they can't find
// each other by themselves (it needs sharing on too).
func (s *Service) Connect(ctx context.Context, address string) (PeerView, error) {
	address = strings.TrimSpace(address)
	address = strings.TrimPrefix(strings.TrimPrefix(address, "http://"), "https://")
	address = strings.TrimSuffix(address, "/")
	host, portStr, err := net.SplitHostPort(address)
	if err != nil {
		host, portStr = address, strconv.Itoa(config.DefaultPort)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return PeerView{}, errors.New("that isn't an address like 192.168.1.20 or 192.168.1.20:43311")
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)
	if err != nil || len(ips) == 0 {
		return PeerView{}, fmt.Errorf("can't find %s on the network", host)
	}
	ip := ips[0]
	if !isLocalNetwork(ip) {
		return PeerView{}, errors.New("only computers on your home network can share")
	}
	probe := &peer{addr: ip, port: port}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probe.baseURL()+"/api/peer/whoami", nil)
	if err != nil {
		return PeerView{}, err
	}
	resp, err := s.api.Do(req)
	if err != nil {
		return PeerView{}, fmt.Errorf("no answer from %s: is KumoBox open there, with library sharing on?", address)
	}
	defer resp.Body.Close()
	var h Hello
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&h) != nil {
		return PeerView{}, fmt.Errorf("%s isn't an KumoBox app with library sharing on", address)
	}
	if h.ID == s.id.ID {
		return PeerView{}, errors.New("that's this KumoBox")
	}
	pub, err := parsePublicKey(h.ID, h.Key)
	if err != nil {
		return PeerView{}, err
	}
	s.mu.Lock()
	p, ok := s.peers[h.ID]
	if !ok {
		p = &peer{id: h.ID, pub: pub}
		s.peers[h.ID] = p
	}
	p.name, p.version, p.addr, p.port, p.seen = cleanName(h.Name), h.Version, ip, port, time.Now()
	if pl := cleanPlatform(h.Platform); pl != "" {
		p.platform = pl
	}
	p.manual = net.JoinHostPort(host, strconv.Itoa(port))
	p.asked, p.fails = time.Time{}, 0
	s.save()
	v := s.view(p, time.Now())
	s.mu.Unlock()
	s.wake()
	s.hub.Publish("sharing-updated", nil)
	return v, nil
}

// SharedLibrary is a host's shared library.
type SharedLibrary struct {
	Host  PeerView             `json:"host"`
	Files []*library.LocalFile `json:"files"`
}

// Libraries are the libraries shared with this KumoBox.
func (s *Service) Libraries() []SharedLibrary {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []SharedLibrary{}
	for _, p := range s.peers {
		if p.files != nil {
			out = append(out, SharedLibrary{Host: s.view(p, now), Files: p.files})
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Host.Name) < strings.ToLower(out[j].Host.Name) })
	return out
}

// RemoteFiles are all the files shared with this KumoBox.
func (s *Service) RemoteFiles() []*library.LocalFile {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*library.LocalFile
	for _, p := range s.peers {
		out = append(out, p.files...)
	}
	return out
}

// File is a shared file, by its kumobox:// path.
func (s *Service) File(path string) (*library.LocalFile, bool) {
	host, _, ok := ParsePath(path)
	if !ok {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.peers[host]
	if p == nil {
		return nil, false
	}
	i := slices.IndexFunc(p.files, func(f *library.LocalFile) bool { return f.Path == path })
	if i < 0 {
		return nil, false
	}
	return p.files[i], true
}

// nameOf is a peer's name.
func (s *Service) nameOf(p *peer) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return p.name
}
