package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/v0-0x/mediaserver/anime/internal/anikohub"
	"github.com/v0-0x/mediaserver/anime/internal/config"
	"github.com/v0-0x/mediaserver/anime/internal/share"
)

// An Aniko Hub's audit log (anikohub): the dashboard's API for it and for the
// server's log, and what the API puts in it itself: every change made from
// the dashboard (or on the hub itself) and who made it, sign-ins and wrong
// passwords.

// auditBy: who a request is, for the audit log: the dashboard's owner
// (signed in), or the hub itself (a browser or program on it).
func auditBy(r *http.Request) (by, ip string) {
	ip = clientIP(r).String()
	if kindOf(r) == clientOwner {
		return "dashboard", ip
	}
	return "hub", ip
}

// auditRoute: what a change is (its kind and what to say), by the API's
// pattern.
type auditRoute struct{ kind, text string }

var auditRoutes = map[string]auditRoute{
	"PUT /api/settings":                     {"settings", "changed the hub's settings"},
	"POST /api/library/scan":                {"scan", "scanned the library"},
	"POST /api/library/match":               {"library", "matched files"},
	"POST /api/library/unmatch":             {"library", "unmatched files"},
	"POST /api/library/ignore":              {"library", "ignored files"},
	"PATCH /api/library/file":               {"library", "changed a file's anime or episode"},
	"POST /api/library/index":               {"library", "indexed folders"},
	"POST /api/hub/devices/{id}":            {"device", "changed what a device may do"},
	"DELETE /api/sharing/peers/{id}":        {"device", "forgot a device"},
	"POST /api/sharing/peers/{id}":          {"device", "changed what a device may do"},
	"POST /api/sharing/peers/{id}/parental": {"device", "changed a device's parental controls"},
	"POST /api/hub/password":                {"password", "changed the dashboard's password"},
	"POST /api/update/check":                {"update", "checked for an update"},
	"POST /api/update/apply":                {"update", "installed an update"},
	"POST /api/update/restart":              {"update", "restarted the hub"},
	"POST /api/onlinestream/download":       {"download", "queued downloads"},
	"POST /api/downloads/{id}/cancel":       {"download", "cancelled a download"},
	"POST /api/downloads/{id}/retry":        {"download", "tried a download again"},
	"DELETE /api/downloads/{id}":            {"download", "removed a download from the list"},
	"POST /api/downloads/clear":             {"download", "cleared the finished downloads"},
	"POST /api/torrent-client/add":          {"download", "added torrents"},
	"POST /api/torrents/download":           {"download", "added torrents"},
	"POST /api/torrent-client/action":       {"download", "changed torrents"},
	"POST /api/autodownloader/rules":        {"download", "saved an auto download rule"},
	"DELETE /api/autodownloader/rules/{id}": {"download", "deleted an auto download rule"},
	"POST /api/autodownloader/run":          {"download", "ran the auto downloader"},
	"POST /api/backup/restore":              {"settings", "restored a backup"},
	"POST /api/cache/clear":                 {"settings", "cleared the cache"},
	"POST /api/images/clear":                {"settings", "cleared the saved images"},
	"DELETE /api/history":                   {"settings", "cleared the watch history"},
	"POST /api/extensions/install":          {"settings", "installed an extension"},
	"DELETE /api/extensions/{id}":           {"settings", "removed an extension"},
}

// auditSkip: not changes (searches, playing), or noted where they happen
// with more said.
var auditSkip = map[string]bool{
	// A file's pieces: the file is noted once it's all there (upload.go).
	"POST /api/upload":                true,
	"POST /api/anilist/search":        true,
	"POST /api/torrents/search":       true,
	"POST /api/local/hls":             true,
	"DELETE /api/local/hls/{id}":      true,
	"POST /api/playback/progress":     true,
	"POST /api/playback/note":         true,
	"POST /api/playback/command":      true,
	"POST /api/plugins/navigate":      true,
	"POST /api/plugins/{id}/event":    true,
	"POST /api/library/delete":        true,
	"POST /api/hub/audit/{id}/again":  true,
	"POST /api/auth/server-login":     true,
	"POST /api/hub/setup":             true,
	"POST /api/hub/signout":           true,
	"DELETE /api/hub/accounts/{user}": true,
}

// auditBodies: the changes whose request says more (read, then put back).
var auditBodies = map[string]bool{
	"POST /api/library/scan":    true,
	"POST /api/library/match":   true,
	"POST /api/library/unmatch": true,
	"POST /api/library/ignore":  true,
}

// audited serves a request that changes something on a hub, made from its
// dashboard or on the hub itself, and notes it in the audit log: what,
// who, and whether it went through.
func (s *Server) audited(w http.ResponseWriter, r *http.Request, next http.Handler) {
	_, pattern := s.mux.Handler(r)
	// The movies and music sides' file pieces are like this side's: each
	// file is noted once, when it's whole (LogUpload).
	if pattern == "" || auditSkip[pattern] || r.URL.Path == "/api/movies/upload" || r.URL.Path == "/api/music/upload" {
		next.ServeHTTP(w, r)
		return
	}
	route, ok := auditRoutes[pattern]
	if !ok {
		route = auditRoute{"change", "made a change (" + r.Method + " " + r.URL.Path + ")"}
	}
	var body []byte
	if auditBodies[pattern] && r.Body != nil {
		body, _ = io.ReadAll(io.LimitReader(r.Body, 1<<20))
		r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), r.Body))
	}
	var before config.Settings
	var device *share.HubDevice
	switch pattern {
	case "PUT /api/settings":
		before = s.app.Settings.Get()
	case "POST /api/hub/devices/{id}", "DELETE /api/sharing/peers/{id}", "POST /api/sharing/peers/{id}", "POST /api/sharing/peers/{id}/parental":
		device = s.hubDevice(pathValue(pattern, r.URL.Path, "id"))
	}

	aw := &auditWriter{ResponseWriter: w}
	next.ServeHTTP(aw, r)

	by, ip := auditBy(r)
	e := anikohub.Event{Kind: route.kind, By: by, IP: ip, Text: route.text}
	switch pattern {
	case "PUT /api/settings":
		e.Text = settingsChanged(before, s.app.Settings.Get())
		if e.Text == "" {
			return // saved as it was
		}
		if strings.HasPrefix(e.Text, "added the folder") || strings.HasPrefix(e.Text, "stopped sharing the folder") {
			e.Kind = "folders"
		}
	case "POST /api/library/scan":
		var b struct {
			Full bool `json:"full"`
		}
		_ = json.Unmarshal(body, &b)
		if b.Full {
			e.Text = "scanned everything again"
		}
	case "POST /api/library/match", "POST /api/library/unmatch", "POST /api/library/ignore":
		var b pathsBody
		_ = json.Unmarshal(body, &b)
		n := strconv.Itoa(len(b.Paths)) + " file(s)"
		switch pattern {
		case "POST /api/library/match":
			e.Text, e.MediaID = "matched "+n+" to", b.MediaID
		case "POST /api/library/unmatch":
			e.Text = "unmatched " + n
		default:
			e.Text = "ignored " + n
			if !b.Ignored {
				e.Text = "stopped ignoring " + n
			}
		}
		for _, p := range b.Paths {
			e.Files = append(e.Files, anikohub.AuditFile{Name: baseOf(p), Path: p})
		}
	case "POST /api/hub/devices/{id}", "POST /api/sharing/peers/{id}", "DELETE /api/sharing/peers/{id}", "POST /api/sharing/peers/{id}/parental":
		if device != nil {
			e.Device = device.Name
			if after := s.hubDevice(pathValue(pattern, r.URL.Path, "id")); after != nil && pattern != "DELETE /api/sharing/peers/{id}" {
				if t := grantsChanged(*device, *after); t != "" {
					e.Text = t
				}
			}
			e.Text = strings.Replace(e.Text, "a device", device.Name, 1)
			e.Device = ""
		}
	}
	if aw.status >= 400 {
		e.Failed, e.Detail = true, "refused: "+refusal(aw.status, aw.body)
	}
	s.app.HubStore.Log(e)
	s.app.Hub.Publish("hub-updated", nil)
}

// pathValue is a wildcard's value in a path ("{id}" in the pattern): the
// mux sets them only on the request it hands its handler.
func pathValue(pattern, path, name string) string {
	_, pat, _ := strings.Cut(pattern, " ")
	ps, xs := strings.Split(pat, "/"), strings.Split(path, "/")
	for i, p := range ps {
		if p == "{"+name+"}" && i < len(xs) {
			return xs[i]
		}
	}
	return ""
}

// auditWriter notes a response's status, and the start of an error's body.
type auditWriter struct {
	http.ResponseWriter
	status int
	body   []byte
}

func (a *auditWriter) WriteHeader(code int) {
	if a.status == 0 {
		a.status = code
	}
	a.ResponseWriter.WriteHeader(code)
}

func (a *auditWriter) Write(p []byte) (int, error) {
	if a.status == 0 {
		a.status = http.StatusOK
	}
	if a.status >= 400 && len(a.body) < 512 {
		a.body = append(a.body, p[:min(len(p), 512-len(a.body))]...)
	}
	return a.ResponseWriter.Write(p)
}

func (a *auditWriter) Flush() {
	if f, ok := a.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// refusal: what an error response says.
func refusal(status int, body []byte) string {
	var b struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &b) == nil && b.Error != "" {
		return b.Error
	}
	return strings.ToLower(http.StatusText(status))
}

func baseOf(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// settingsChanged says what changed in the settings: the library folders
// by name, the rest by section (never a value: the password is there).
func settingsChanged(before, after config.Settings) string {
	var said []string
	was, now := before.LibraryDirs(), after.LibraryDirs()
	for _, d := range now {
		if !slices.Contains(was, d) {
			said = append(said, "added the folder "+d)
		}
	}
	for _, d := range was {
		if !slices.Contains(now, d) {
			said = append(said, "stopped sharing the folder "+d)
		}
	}
	b1, _ := json.Marshal(before)
	b2, _ := json.Marshal(after)
	var m1, m2 map[string]json.RawMessage
	_ = json.Unmarshal(b1, &m1)
	_ = json.Unmarshal(b2, &m2)
	var sections []string
	for k, v := range m2 {
		if !bytes.Equal(m1[k], v) {
			sections = append(sections, k)
		}
	}
	slices.Sort(sections)
	// The folders are already said; the rest of the library's settings
	// only when they changed too.
	if len(said) > 0 {
		before.Library.Dir, before.Library.ExtraDirs = after.Library.Dir, after.Library.ExtraDirs
		if l1, _ := json.Marshal(before.Library); bytes.Equal(l1, m2["library"]) {
			sections = slices.DeleteFunc(sections, func(s string) bool { return s == "library" })
		}
	}
	if len(sections) > 0 {
		said = append(said, "changed the hub's settings ("+strings.Join(sections, ", ")+")")
	}
	return strings.Join(said, "; ")
}

// hubDevice is a device the hub knows, by ID.
func (s *Server) hubDevice(id string) *share.HubDevice {
	for _, d := range s.app.Share.HubDevices() {
		if d.ID == id {
			return &d
		}
	}
	return nil
}

// grantsChanged says what a device may do now that it couldn't, or no
// longer.
func grantsChanged(was, now share.HubDevice) string {
	var said []string
	if was.Allowed != now.Allowed {
		said = append(said, map[bool]string{true: "shared the library with " + now.Name, false: "stopped sharing the library with " + now.Name}[now.Allowed])
	}
	if was.Account != now.Account || (now.Account && was.AccountUser != now.AccountUser) {
		said = append(said, map[bool]string{true: "gave " + now.Name + " an AniList login", false: "took the AniList login back from " + now.Name}[now.Account])
	}
	if was.Downloads != now.Downloads {
		said = append(said, map[bool]string{true: "let " + now.Name + " download onto the hub", false: "stopped " + now.Name + " downloading onto the hub"}[now.Downloads])
	}
	return strings.Join(said, "; ")
}

// signedIn notes a sign-in to the dashboard, or a wrong password.
func (s *Server) signedIn(r *http.Request, ok bool, text string) {
	if s.app.HubStore == nil {
		return
	}
	e := anikohub.Event{Kind: "signin", By: "dashboard", IP: clientIP(r).String(), Text: text}
	if !ok {
		e.Kind, e.By, e.Failed = "signin-failed", "", true
	}
	s.app.HubStore.Log(e)
	s.app.Hub.Publish("hub-updated", nil)
}

// --------------------------------------------------------------- the API

func (s *Server) auditAPI(owner func(func(*http.Request) (any, error)) http.HandlerFunc) {
	m := s.mux
	// The audit log, the newest first: ?before= the oldest ID shown (the
	// page before), ?group= a filter (anikohub.AuditGroups), ?q= a search.
	m.HandleFunc("GET /api/hub/audit", owner(func(r *http.Request) (any, error) {
		q := r.URL.Query()
		aq := anikohub.AuditQuery{Search: q.Get("q"), Limit: 100}
		aq.Before, _ = strconv.ParseInt(q.Get("before"), 10, 64)
		if g := q.Get("group"); g != "" {
			kinds, ok := anikohub.AuditGroups[g]
			if !ok {
				return nil, badRequest("no such filter: " + g)
			}
			aq.Kinds = kinds
		}
		events, more, err := s.app.HubStore.Audit(aq)
		if err != nil {
			return nil, err
		}
		s.titles(r, events)
		return map[string]any{"events": events, "more": more}, nil
	}))
	// Download again what a deletion took (dub: sub or dub, if not as it
	// was): onto the hub, where the anime's other episodes are.
	m.HandleFunc("POST /api/hub/audit/{id}/again", owner(func(r *http.Request) (any, error) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		e := s.app.HubStore.AuditEvent(id)
		if e == nil {
			return nil, notFound("no such event")
		}
		if e.Again == nil || len(e.Again.Episodes) == 0 {
			return nil, badRequest("there's nothing to download again here: openings, endings and what has no episode number can't be")
		}
		var body struct {
			Dub *bool `json:"dub"`
		}
		_ = decode(r, &body)
		if body.Dub != nil {
			e.Again.Dub = *body.Dub
		}
		eps := make([]float64, len(e.Again.Episodes))
		for i, n := range e.Again.Episodes {
			eps[i] = float64(n)
		}
		items, err := s.queueStreamDownload(r.Context(), share.StreamDownload{Provider: e.Again.Provider, MediaID: e.Again.MediaID, Episodes: eps, Dub: e.Again.Dub, Quality: e.Again.Quality})
		if err != nil {
			return nil, err
		}
		e.Again.Queued = time.Now().UnixMilli()
		if err := s.app.HubStore.UpdateAudit(*e); err != nil {
			return nil, err
		}
		by, ip := auditBy(r)
		mode, noun := "subbed", "episodes"
		if e.Again.Dub {
			mode = "dubbed"
		}
		if len(items) == 1 {
			noun = "episode"
		}
		s.app.HubStore.Log(anikohub.Event{Kind: "again", By: by, IP: ip, MediaID: e.Again.MediaID, Title: e.Title,
			Text:   fmt.Sprintf("started downloading again (%s): %d %s of", mode, len(items), noun),
			Detail: "deleted " + time.UnixMilli(e.At).Format("2 Jan 15:04")})
		s.app.Hub.Publish("hub-updated", nil)
		return map[string]any{"queued": len(items), "event": e}, nil
	}))
	// The server's log, the newest first: ?skip= lines already shown,
	// ?q= a search, ?errors=1 only what went wrong.
	m.HandleFunc("GET /api/hub/log", owner(func(r *http.Request) (any, error) {
		f := s.app.Logs.File
		if f == nil {
			return map[string]any{"lines": s.reversedLines(), "more": false, "onDisk": false}, nil
		}
		q := r.URL.Query()
		skip, _ := strconv.Atoi(q.Get("skip"))
		lines, more, err := f.Lines(q.Get("q"), q.Get("errors") == "1", max(skip, 0), 500)
		if err != nil {
			return nil, err
		}
		return map[string]any{"lines": lines, "more": more, "onDisk": true}, nil
	}))
	// The whole log, to save.
	m.HandleFunc("GET /api/hub/log/download", func(w http.ResponseWriter, r *http.Request) {
		if s.app.HubStore == nil || !isTrusted(r) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "sign in to the hub first"})
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="aniko-hub-`+time.Now().Format("2006-01-02")+`.log"`)
		if f := s.app.Logs.File; f != nil {
			_, _ = f.WriteTo(w)
			return
		}
		for _, l := range s.app.Logs.Lines() {
			_, _ = io.WriteString(w, l+"\n")
		}
	})
}

// reversedLines: the log kept in memory, the newest first.
func (s *Server) reversedLines() []string {
	lines := s.app.Logs.Lines()
	slices.Reverse(lines)
	return lines
}

// titles fills in the anime events are about, by name (AniList's, kept;
// asked a moment at most).
func (s *Server) titles(r *http.Request, events []anikohub.Event) {
	var ids []int
	for _, e := range events {
		if e.MediaID > 0 && e.Title == "" {
			ids = append(ids, e.MediaID)
		}
	}
	names := titles.get(s.app.Platform, ids)
	for i, e := range events {
		if n, ok := names[e.MediaID]; ok && e.Title == "" && n.title != "" {
			events[i].Title = n.title
		}
	}
}

// LogUpload puts a file uploaded to one of mediaserver's other sides (side:
// "Movies", "Music") in the hub's audit log, as this side's own uploads are:
// from a device (its name), or from the dashboard's browser (device "").
func (s *Server) LogUpload(r *http.Request, side, device, rel, full string, size int64) {
	if s.app.HubStore == nil {
		return
	}
	ev := anikohub.Event{Kind: "upload", Device: device, Text: "uploaded " + rel + " to " + side, Size: size,
		Files: []anikohub.AuditFile{{Name: filepath.Base(full), Path: full, Size: size}}}
	if device == "" {
		ev.By, ev.IP = auditBy(r)
	}
	s.app.HubStore.Log(ev)
}
