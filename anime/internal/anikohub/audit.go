package anikohub

import (
	"encoding/json"
	"log"
	"strconv"
	"strings"
	"time"
)

// The audit log: everything that happened on the hub, kept in its database
// (AuditKeep, at most auditMax events): what the devices did through it
// (episodes watched, logins and settings sent, downloads), every change
// made from the dashboard and who made it, sign-ins and wrong passwords,
// downloads finishing or failing, files deleted (with what Download again
// needs), the hub starting and stopping. The server's own log is apart
// (app.LogFile).

// Event is something that happened.
type Event struct {
	ID   int64  `json:"id"`
	At   int64  `json:"at"` // milliseconds
	Kind string `json:"kind"`
	// Who: a device (its name), or By: "dashboard" (its owner, signed in;
	// IP its address) or "hub" (the hub itself, or a program on it). An
	// address alone: someone who isn't signed in (a wrong password).
	Device string `json:"device,omitempty"`
	By     string `json:"by,omitempty"`
	IP     string `json:"ip,omitempty"`
	User   int    `json:"user,omitempty"`
	Text   string `json:"text"`
	// It didn't work (a wrong password, a change refused, a download failed).
	Failed bool `json:"failed,omitempty"`
	// More about it, on a line of its own (why it didn't work).
	Detail string `json:"detail,omitempty"`
	// An anime it's about (its title is filled in when it's known).
	MediaID int    `json:"mediaId,omitempty"`
	Episode int    `json:"episode,omitempty"`
	Title   string `json:"title,omitempty"`
	// Files it's about (deleted, downloaded), and their size together.
	Files []AuditFile `json:"files,omitempty"`
	Size  int64       `json:"size,omitempty"`
	// Deleted episodes: how to download them again.
	Again *Again `json:"again,omitempty"`
}

// AuditFile is a file an event is about.
type AuditFile struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	Episode int    `json:"episode,omitempty"`
}

// Again is what Download again queues for deleted episodes.
type Again struct {
	MediaID  int    `json:"mediaId"`
	Episodes []int  `json:"episodes"`
	Dub      bool   `json:"dub,omitempty"`
	Provider string `json:"provider,omitempty"`
	Quality  string `json:"quality,omitempty"`
	// When it was last queued again (milliseconds; 0: not yet).
	Queued int64 `json:"queued,omitempty"`
}

const (
	AuditKeep = 90 * 24 * time.Hour
	auditMax  = 50000
)

// AuditGroups: the kinds of event each filter of the dashboard shows.
var AuditGroups = map[string][]string{
	"watching":  {"playing", "finished"},
	"library":   {"delete", "again", "scan", "library", "folders"},
	"downloads": {"download", "downloaded", "download-failed", "again"},
	"devices":   {"account", "prefs", "import", "device"},
	"dashboard": {"signin", "signin-failed", "signout", "password", "settings", "change", "update"},
	"hub":       {"start", "stop", "update"},
}

// Log keeps an event in the audit log, and gives its ID.
func (s *Store) Log(e Event) int64 {
	if e.At == 0 {
		e.At = time.Now().UnixMilli()
	}
	e.Text = strings.TrimSpace(e.Text)
	e.ID = 0
	data, err := json.Marshal(e)
	if err != nil {
		return 0
	}
	res, err := s.db.Write(`INSERT INTO hub_audit (at, kind, search, data) VALUES (?, ?, ?, ?)`, e.At, e.Kind, searchText(e), string(data))
	if err != nil {
		return 0
	}
	id, _ := res.LastInsertId()
	// The server's log has it too.
	log.Printf("audit: %s", e.Line())
	// Now and then, what's past keeping.
	if id%500 == 0 {
		s.PruneAudit(time.Now())
	}
	return id
}

// Line is an event in a sentence, as the server's log says it.
func (e Event) Line() string {
	who := e.Device
	switch {
	case who != "":
	case e.By == "dashboard":
		who = "the dashboard (" + e.IP + ")"
	case e.By == "hub":
		who = "the hub"
	case e.IP != "":
		who = "someone at " + e.IP
	}
	line := strings.TrimSpace(who + " " + e.Text)
	if e.Title != "" {
		line += " " + e.Title
	}
	if e.Episode > 0 {
		line += " episode " + strconv.Itoa(e.Episode)
	}
	if n := len(e.Files); n > 1 {
		line += " (" + strconv.Itoa(n) + " files)"
	} else if n == 1 && e.Episode == 0 {
		line += " (" + e.Files[0].Name + ")"
	}
	if e.Detail != "" {
		line += ": " + e.Detail
	}
	return line
}

// searchText: what a search finds an event by.
func searchText(e Event) string {
	parts := []string{e.Device, e.By, e.IP, e.Text, e.Title, e.Kind, e.Detail}
	if e.Episode > 0 {
		parts = append(parts, "episode "+strconv.Itoa(e.Episode))
	}
	for _, f := range e.Files {
		parts = append(parts, f.Name)
	}
	return strings.ToLower(strings.Join(parts, " "))
}

// AuditQuery picks events: the newest first, before an ID (the page
// before), of some kinds, matching a search.
type AuditQuery struct {
	Before int64
	Kinds  []string
	Search string
	Limit  int
}

// Audit is the events a query picks, and whether there are older ones.
func (s *Store) Audit(q AuditQuery) ([]Event, bool, error) {
	if q.Limit <= 0 || q.Limit > 500 {
		q.Limit = 100
	}
	where := []string{"1=1"}
	var args []any
	if q.Before > 0 {
		where = append(where, "id < ?")
		args = append(args, q.Before)
	}
	if len(q.Kinds) > 0 {
		where = append(where, "kind IN ("+strings.TrimSuffix(strings.Repeat("?,", len(q.Kinds)), ",")+")")
		for _, k := range q.Kinds {
			args = append(args, k)
		}
	}
	for _, w := range strings.Fields(strings.ToLower(q.Search)) {
		where = append(where, `search LIKE ? ESCAPE '\'`)
		args = append(args, "%"+likeEscape(w)+"%")
	}
	args = append(args, q.Limit+1)
	rows, err := s.db.Query(`SELECT id, data FROM hub_audit WHERE `+strings.Join(where, " AND ")+` ORDER BY id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var id int64
		var data string
		if rows.Scan(&id, &data) != nil {
			continue
		}
		var e Event
		if json.Unmarshal([]byte(data), &e) != nil {
			continue
		}
		e.ID = id
		out = append(out, e)
	}
	more := len(out) > q.Limit
	if more {
		out = out[:q.Limit]
	}
	return out, more, rows.Err()
}

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// Activity is what happened lately (the overview's), the latest first.
func (s *Store) Activity() []Event {
	out, _, _ := s.Audit(AuditQuery{Limit: 20})
	return out
}

// AuditEvent is one event (nil when there's none).
func (s *Store) AuditEvent(id int64) *Event {
	var data string
	if err := s.db.QueryRow(`SELECT data FROM hub_audit WHERE id = ?`, id).Scan(&data); err != nil {
		return nil
	}
	var e Event
	if json.Unmarshal([]byte(data), &e) != nil {
		return nil
	}
	e.ID = id
	return &e
}

// UpdateAudit saves a change to an event (Download again: when).
func (s *Store) UpdateAudit(e Event) error {
	id := e.ID
	e.ID = 0
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = s.db.Write(`UPDATE hub_audit SET data = ?, search = ? WHERE id = ?`, string(data), searchText(e), id)
	return err
}

// PruneAudit forgets what's older than AuditKeep, and the oldest past
// auditMax events.
func (s *Store) PruneAudit(now time.Time) {
	_, _ = s.db.Write(`DELETE FROM hub_audit WHERE at < ?`, now.Add(-AuditKeep).UnixMilli())
	_, _ = s.db.Write(`DELETE FROM hub_audit WHERE id <= (SELECT id FROM hub_audit ORDER BY id DESC LIMIT 1 OFFSET ?)`, auditMax)
}
