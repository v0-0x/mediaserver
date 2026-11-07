package anikohub

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/anime/internal/db"
)

func item(at int64, deleted bool, extra string) json.RawMessage {
	s := `{"at":` + itoa(at)
	if deleted {
		s += `,"deleted":true`
	}
	if extra != "" {
		s += "," + extra
	}
	return json.RawMessage(s + "}")
}

func itoa(n int64) string { return json.Number(json.RawMessage(itoaRaw(n))).String() }

func itoaRaw(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// Blobs merge item by item: the newer of each wins, removals are kept a
// while (and dropped when old), and the hub keeps the merged result.
func TestBlobsMerge(t *testing.T) {
	now := time.Now().UnixMilli()
	a := Blob{Items: map[string]json.RawMessage{"1": item(now-5000, false, `"status":"PLANNING"`), "2": item(now-4000, false, `"status":"CURRENT"`)}}
	b := Blob{Items: map[string]json.RawMessage{"1": item(now-1000, true, ""), "3": item(now-2000, false, `"status":"COMPLETED"`), "4": item(now-40*24*3600*1000, true, "")}}
	if !a.Merge(b) {
		t.Fatal("nothing changed")
	}
	if it := itemOf(a.Items["1"]); !it.Deleted || it.At != now-1000 {
		t.Fatalf("1: %+v (the newer removal wins)", it)
	}
	if _, ok := a.Items["3"]; !ok || len(a.Items) != 3 {
		t.Fatalf("items %v (3 added, the old removal 4 dropped)", a.Items)
	}
	if a.UpdatedAt != now-1000 {
		t.Fatalf("updatedAt %d, want the newest item's %d", a.UpdatedAt, now-1000)
	}
	// Older items don't come back.
	if a.Merge(Blob{Items: map[string]json.RawMessage{"1": item(now-3000, false, `"status":"CURRENT"`)}}) {
		t.Fatal("an older item changed the blob")
	}

	d, err := db.Open(filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	s, err := New(d)
	if err != nil {
		t.Fatal(err)
	}
	if at := s.BlobsAt(0)["lists"]; at != 0 {
		t.Fatalf("at %d before anything", at)
	}
	merged, changed, err := s.MergeBlob(0, "lists", a)
	if err != nil || !changed || len(merged.Items) != 3 || merged.UpdatedAt != a.UpdatedAt {
		t.Fatalf("first merge: %v %v %+v", err, changed, merged)
	}
	if _, changed, _ := s.MergeBlob(0, "lists", a); changed {
		t.Fatal("the same again changed the hub's")
	}
	if got := s.Blob(0, "lists"); len(got.Items) != 3 || got.UpdatedAt != a.UpdatedAt {
		t.Fatalf("kept: %+v", got)
	}
	if s.BlobsAt(0)["lists"] != a.UpdatedAt || s.BlobsAt(0)["watch-order"] != 0 {
		t.Fatalf("at: %v", s.BlobsAt(0))
	}
	// Another account's are its own; a kind that isn't kept is refused.
	if got := s.Blob(7, "lists"); len(got.Items) != 0 {
		t.Fatalf("account 7 has %v", got.Items)
	}
	if _, _, err := s.MergeBlob(0, "secrets", a); err == nil {
		t.Fatal("an unknown kind was kept")
	}
	// From the future: brought back to now, give or take.
	far := Blob{Items: map[string]json.RawMessage{"9": item(now+3600*1000, false, `"status":"CURRENT"`)}}
	if merged, _, _ := s.MergeBlob(0, "lists", far); itemOf(merged.Items["9"]).At > now+int64(maxAhead/time.Millisecond)+1000 {
		t.Fatalf("a future item kept its time: %v", itemOf(merged.Items["9"]))
	}
}
