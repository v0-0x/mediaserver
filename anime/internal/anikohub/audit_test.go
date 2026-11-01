package anikohub

import (
	"testing"
	"time"
)

// Kept in the database: the newest first, a page at a time, by kind and by
// search; an event changed later (Download again) is saved; what's past
// keeping goes.
func TestAudit(t *testing.T) {
	s := newStore(t)
	old := time.Now().Add(-AuditKeep - time.Hour).UnixMilli()
	s.Log(Event{At: old, Kind: "playing", Device: "TV", Text: "is watching"})
	del := s.Log(Event{Kind: "delete", By: "dashboard", IP: "192.168.1.20", Text: "deleted", Title: "One Piece",
		Files: []AuditFile{{Name: "One Piece - 1051.mkv", Size: 100, Episode: 1051}}, Again: &Again{MediaID: 21, Episodes: []int{1051}}})
	s.Log(Event{Kind: "signin-failed", IP: "192.168.1.40", Text: "tried a wrong password", Failed: true})
	for i := 0; i < 5; i++ {
		s.Log(Event{Kind: "finished", Device: "Living room TV", Text: "finished", Episode: i + 1})
	}

	all, more, err := s.Audit(AuditQuery{Limit: 3})
	if err != nil || len(all) != 3 || !more || all[0].Kind != "finished" || all[0].Episode != 5 || all[0].ID == 0 {
		t.Fatalf("first page: %+v, more %v, %v", all, more, err)
	}
	rest, more, _ := s.Audit(AuditQuery{Before: all[2].ID, Limit: 10})
	if len(rest) != 5 || more || rest[4].Kind != "playing" {
		t.Fatalf("the rest: %d events, more %v", len(rest), more)
	}

	lib, _, _ := s.Audit(AuditQuery{Kinds: AuditGroups["library"]})
	if len(lib) != 1 || lib[0].ID != del || lib[0].Again == nil || lib[0].Files[0].Name != "One Piece - 1051.mkv" {
		t.Fatalf("library: %+v", lib)
	}
	for q, want := range map[string]int{"1051.mkv": 1, "one piece": 1, "192.168.1.40": 1, "living TV": 5, "nothing like it": 0, "100%": 0} {
		if got, _, _ := s.Audit(AuditQuery{Search: q}); len(got) != want {
			t.Errorf("search %q: %d events, want %d", q, len(got), want)
		}
	}

	e := s.AuditEvent(del)
	e.Again.Queued = 1234
	if err := s.UpdateAudit(*e); err != nil {
		t.Fatal(err)
	}
	if got := s.AuditEvent(del); got.Again.Queued != 1234 || got.Title != "One Piece" {
		t.Errorf("after the change: %+v", got)
	}
	if s.AuditEvent(99999) != nil {
		t.Error("an event that isn't there")
	}

	s.PruneAudit(time.Now())
	if got, _, _ := s.Audit(AuditQuery{Kinds: []string{"playing"}}); len(got) != 0 {
		t.Error("the old event wasn't forgotten")
	}
	if got := s.Activity(); len(got) != 7 {
		t.Errorf("lately: %d events, want 7", len(got))
	}
}
