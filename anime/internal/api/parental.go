package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/v0-0x/mediaserver/anime/internal/parental"
	"github.com/v0-0x/mediaserver/anime/internal/share"
)

// Parental controls (internal/parental) in the API. On a device that has
// them, what's asked of its Aniko is checked (an anime's page, playing or
// streaming or downloading one) and what it answers has the blocked anime
// taken out; on a host, the same for the requests of a device with parental
// controls set there.

// The answers that list anime, filtered.
var parentalFiltered = []string{
	"/api/anime", "/api/anilist", "/api/manga", "/api/history", "/api/continue", "/api/library/files",
	"/api/sharing/libraries", "/api/onlinestream/search", "/api/stats", "/api/downloads",
	"/api/peer/files", "/api/peer/history",
}

// What a device's own controls never stop: seeing and lifting them.
var parentalOpen = []string{"/api/parental", "/api/status", "/api/settings", "/api/events", "/api/sharing/peers"}

var mediaInPath = regexp.MustCompile(`^/api/(?:anime|manga|playback/tracks)/(\d+)(?:/|$)`)

// parentalServe serves r under rules: refused when it's about a blocked
// anime, its answer filtered.
func (s *Server) parentalServe(rules *parental.Rules, w http.ResponseWriter, r *http.Request, next http.Handler) {
	path := r.URL.Path
	for _, p := range parentalOpen {
		if strings.HasPrefix(path, p) {
			next.ServeHTTP(w, r)
			return
		}
	}
	if why := s.parentalAsked(rules, r); why != "" {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": parental.Message(why), "parental": true})
		return
	}
	filter := false
	for _, p := range parentalFiltered {
		if strings.HasPrefix(path, p) {
			filter = true
		}
	}
	if !filter {
		next.ServeHTTP(w, r)
		return
	}
	bw := &bufferedWriter{header: http.Header{}, status: http.StatusOK}
	next.ServeHTTP(bw, r)
	for k, v := range bw.header {
		w.Header()[k] = v
	}
	if bw.status != http.StatusOK || !strings.HasPrefix(bw.header.Get("Content-Type"), "application/json") {
		w.WriteHeader(bw.status)
		_, _ = w.Write(bw.buf.Bytes())
		return
	}
	var v any
	if err := json.Unmarshal(bw.buf.Bytes(), &v); err != nil {
		w.WriteHeader(bw.status)
		_, _ = w.Write(bw.buf.Bytes())
		return
	}
	out, why := parental.Filter{Rules: rules, Lookup: func(ctx context.Context, ids []int) map[int]parental.Media {
		return s.parentalLookup(ctx, ids, rules.NoAdult)
	}}.Apply(r.Context(), v)
	w.Header().Del("Content-Length")
	if why != "" {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": parental.Message(why), "parental": true})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// parentalAsked says why r is about a blocked anime ("" when it isn't): an
// anime's ID in its path, its query or its JSON body, or a file of one.
func (s *Server) parentalAsked(rules *parental.Rules, r *http.Request) string {
	var ids []int
	if m := mediaInPath.FindStringSubmatch(r.URL.Path); m != nil {
		if id, _ := strconv.Atoi(m[1]); id > 0 {
			ids = append(ids, id)
		}
	}
	q := r.URL.Query()
	if id, _ := strconv.Atoi(q.Get("mediaId")); id > 0 {
		ids = append(ids, id)
	}
	paths := q["path"]
	if r.Body != nil && (r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch) && isJSON(r) {
		raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		_ = r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(raw))
		var body map[string]any
		if err == nil && json.Unmarshal(raw, &body) == nil {
			if id, ok := body["mediaId"].(float64); ok && id > 0 {
				ids = append(ids, int(id))
			}
			if p, ok := body["path"].(string); ok {
				paths = append(paths, p)
			}
		}
	}
	for _, p := range paths {
		if id := s.mediaOfPath(p); id > 0 {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return ""
	}
	known := map[int]parental.Media{}
	if (rules.NoAdult || len(rules.Genres) > 0) && !rules.OnlyAllowed {
		known = s.parentalLookup(r.Context(), ids, rules.NoAdult)
	}
	for _, id := range ids {
		m, ok := known[id]
		if !ok {
			m = parental.Media{ID: id}
		}
		if why := rules.Reason(m); why != "" {
			return why
		}
	}
	return ""
}

// memo keeps what parental controls looked up for a minute: a video is
// asked for in many ranges, each checked.
type memo struct {
	mu    sync.Mutex
	paths map[string]memoed[int]
	media map[int]memoed[parental.Media]
}

type memoed[T any] struct {
	v  T
	at time.Time
	// tags: an anime's tags were looked up too (parentalLookup).
	tags bool
}

const memoFor = time.Minute

// mediaOfPath is the anime of a file: one of the library, or one shared
// with this Aniko (kumo://…).
func (s *Server) mediaOfPath(p string) int {
	if p == "" {
		return 0
	}
	m := &s.parentalMemo
	m.mu.Lock()
	if e, ok := m.paths[p]; ok && time.Since(e.at) < memoFor {
		m.mu.Unlock()
		return e.v
	}
	m.mu.Unlock()
	id := s.findMediaOfPath(p)
	m.mu.Lock()
	if m.paths == nil || len(m.paths) > 2000 {
		m.paths = map[string]memoed[int]{}
	}
	m.paths[p] = memoed[int]{v: id, at: time.Now()}
	m.mu.Unlock()
	return id
}

func (s *Server) findMediaOfPath(p string) int {
	if share.IsRemote(p) {
		for _, lib := range s.app.Share.Libraries() {
			for _, f := range lib.Files {
				if f.Path == p {
					return f.MediaID
				}
			}
		}
		return 0
	}
	if f, err := s.app.Files.Get(p); err == nil && f != nil {
		return f.MediaID
	}
	return 0
}

// parentalLookup finds anime's 18+ flag and genres, and with tags whether
// they're tagged with sexual content, from what Aniko keeps of AniList
// (asking it for the rest, briefly).
func (s *Server) parentalLookup(ctx context.Context, ids []int, tags bool) map[int]parental.Media {
	out := map[int]parental.Media{}
	mm := &s.parentalMemo
	var missing []int
	mm.mu.Lock()
	for _, id := range ids {
		if e, ok := mm.media[id]; ok && time.Since(e.at) < memoFor && (e.tags || !tags) {
			out[id] = e.v
		} else {
			missing = append(missing, id)
		}
	}
	mm.mu.Unlock()
	if len(missing) == 0 {
		return out
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	found, _ := s.app.Platform.MediaBatch(ctx, missing)
	var mature map[int][]string
	if tags {
		mature, _ = s.app.Platform.MatureTags(ctx, missing)
	}
	mm.mu.Lock()
	if mm.media == nil || len(mm.media) > 5000 {
		mm.media = map[int]memoed[parental.Media]{}
	}
	for id, m := range found {
		if m != nil {
			t, tagged := mature[id]
			v := parental.Media{ID: id, Adult: m.IsAdult, Genres: m.Genres, Mature: len(t) > 0, Known: true}
			out[id] = v
			mm.media[id] = memoed[parental.Media]{v: v, at: time.Now(), tags: tagged}
		}
	}
	mm.mu.Unlock()
	return out
}

// bufferedWriter keeps an answer, to filter it before it goes.
type bufferedWriter struct {
	header http.Header
	status int
	buf    bytes.Buffer
}

func (b *bufferedWriter) Header() http.Header         { return b.header }
func (b *bufferedWriter) WriteHeader(code int)        { b.status = code }
func (b *bufferedWriter) Write(p []byte) (int, error) { return b.buf.Write(p) }

// ------------------------------------------------------------------ routes

func (s *Server) parentalRoutes() {
	m := s.mux
	// This device's own: what's on it, from whom; lifting with a PIN.
	m.HandleFunc("GET /api/parental", h(func(r *http.Request) (any, error) {
		rules := s.app.Share.Parental()
		return map[string]any{"active": rules.Active(), "rules": publicRules(rules), "from": s.app.Share.ParentalFrom()}, nil
	}))
	m.HandleFunc("POST /api/parental/unlock", h(func(r *http.Request) (any, error) {
		var body struct {
			PIN string `json:"pin"`
		}
		if err := decode(r, &body); err != nil {
			return nil, err
		}
		ip := clientIP(r).String()
		if wait := s.logins.wait(ip, time.Now()); wait > 0 {
			return nil, &apiError{status: http.StatusTooManyRequests, msg: "too many wrong PINs: try again in a few minutes"}
		}
		if s.app.Share.UnlockParental(strings.TrimSpace(body.PIN)) == 0 {
			s.logins.failed(ip, time.Now())
			time.Sleep(time.Second)
			return nil, forbidden("that isn't the PIN")
		}
		s.logins.passed(ip)
		return nil, nil
	}))
	// A device's, set here (this computer, or a hub's owner).
	m.HandleFunc("GET /api/sharing/peers/{id}/parental", h(trusted(func(r *http.Request) (any, error) {
		id := r.PathValue("id")
		rules := s.app.Share.RulesOf(id)
		watched := s.app.Share.WatchedBy(id, 300)
		// The anime the rules name, by name.
		anime := []animeName{}
		if rules != nil {
			ids := append(append([]int{}, rules.Allowed...), rules.Blocked...)
			ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
			found, _ := s.app.Platform.MediaBatch(ctx, ids)
			cancel()
			for _, id := range ids {
				a := animeName{ID: id}
				if m := found[id]; m != nil {
					a.Title, a.Cover = m.PreferredTitle(), m.CoverImage.Large
				}
				anime = append(anime, a)
			}
		}
		return map[string]any{"rules": publicRules(rules), "hasPin": rules != nil && rules.PIN != "", "watched": s.watchedView(r.Context(), watched), "anime": anime}, nil
	})))
	m.HandleFunc("POST /api/sharing/peers/{id}/parental", h(trusted(func(r *http.Request) (any, error) {
		var body struct {
			parental.Rules
			NewPIN string `json:"newPin"`
		}
		if err := decode(r, &body); err != nil {
			return nil, err
		}
		pin := strings.TrimSpace(body.NewPIN)
		if pin != "" && pin != "-" && (len(pin) < 4 || len(pin) > 12) {
			return nil, badRequest("a PIN of 4 to 12 characters")
		}
		body.Rules.PIN = ""
		if err := s.app.Share.SetRules(r.PathValue("id"), body.Rules, pin); err != nil {
			return nil, badRequest(err.Error())
		}
		return nil, nil
	})))
	// A device telling the host that set its controls what it watched.
	m.HandleFunc("POST /api/peer/parental/watched", shared(h(func(r *http.Request) (any, error) {
		var entries []share.Watched
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&entries); err != nil {
			return nil, badRequest(err.Error())
		}
		if len(entries) > 500 {
			entries = entries[:500]
		}
		return nil, s.app.Share.TakeWatched(peerOf(r), entries)
	})))
}

type animeName struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
	Cover string `json:"cover"`
}

// publicRules are the rules without the PIN's hash.
func publicRules(r *parental.Rules) *parental.Rules {
	if r == nil {
		return nil
	}
	c := *r
	c.PIN = ""
	return &c
}

// watchedView is what a device watched, with the anime's names and covers.
type watchedView struct {
	share.Watched
	Title string `json:"title"`
	Cover string `json:"cover"`
	Adult bool   `json:"adult"`
}

func (s *Server) watchedView(ctx context.Context, ws []share.Watched) []watchedView {
	out := make([]watchedView, 0, len(ws))
	ids := map[int]bool{}
	for _, w := range ws {
		ids[w.MediaID] = true
	}
	list := make([]int, 0, len(ids))
	for id := range ids {
		list = append(list, id)
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	found, _ := s.app.Platform.MediaBatch(ctx, list)
	for _, w := range ws {
		v := watchedView{Watched: w}
		if m := found[w.MediaID]; m != nil {
			v.Title, v.Cover, v.Adult = m.PreferredTitle(), m.CoverImage.Large, m.IsAdult
		}
		out = append(out, v)
	}
	return out
}
