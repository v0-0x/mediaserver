package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/anime/internal/anikohub"
	"github.com/v0-0x/mediaserver/anime/internal/anilist"
	"github.com/v0-0x/mediaserver/anime/internal/watchorder"
)

// The device's side of lists and watch-order edits kept in step through a
// hub: its own as a blob (removals remembered), and a hub's merged blob
// taken item by item (newer wins; removals remove).
func TestListsAndWatchOrderBlobs(t *testing.T) {
	s := newTestServer(t)
	local := "127.0.0.1:5000"
	hooks := s.app.Share.Blobs
	if hooks["lists"].Own == nil || hooks["watch-order"].Own == nil {
		t.Fatal("no blob hooks")
	}
	media := &anilist.Media{ID: 21, Type: "ANIME", Title: anilist.Title{Romaji: "One Piece"}}
	s.app.DB.SetCache("media-lite:21", media, time.Hour)
	st := "CURRENT"
	if err := s.app.Platform.UpdateEntry(t.Context(), anilist.EntryUpdate{MediaID: 21, Status: &st}); err != nil {
		t.Fatal(err)
	}
	own := hooks["lists"].Own()
	var it struct {
		At     int64
		Status string
		Media  struct{ ID int }
	}
	if raw, ok := own.Items["21"]; !ok || json.Unmarshal(raw, &it) != nil || it.Status != "CURRENT" || it.Media.ID != 21 || it.At <= 0 || it.At%1000 != 0 || own.UpdatedAt != it.At {
		t.Fatalf("own lists blob: %s", own.Items["21"])
	}
	// Removed here: remembered as a removal.
	if err := s.app.Platform.DeleteEntry(t.Context(), 21); err != nil {
		t.Fatal(err)
	}
	var gone struct {
		At      int64
		Deleted bool
	}
	if raw := hooks["lists"].Own().Items["21"]; json.Unmarshal(raw, &gone) != nil || !gone.Deleted || gone.At < it.At {
		t.Fatalf("after removal: %s", raw)
	}
	// A hub's blob: a newer entry for 21 comes back, 22 is new, and 23 was
	// never here (a removal of it changes nothing).
	future := time.Now().Unix()*1000 + 2000
	lite, _ := json.Marshal(media.Lite())
	hub := anikohub.Blob{Items: map[string]json.RawMessage{
		"21": json.RawMessage(`{"at":` + strconv.FormatInt(future, 10) + `,"type":"ANIME","status":"COMPLETED","progress":3,"media":` + string(lite) + `}`),
		"22": json.RawMessage(`{"at":` + strconv.FormatInt(future, 10) + `,"type":"ANIME","status":"PLANNING","media":{"id":22,"type":"ANIME","title":{"romaji":"Bleach"}}}`),
		"23": json.RawMessage(`{"at":` + strconv.FormatInt(future, 10) + `,"deleted":true}`),
	}}
	if !hooks["lists"].Apply(hub) {
		t.Fatal("the hub's blob changed nothing")
	}
	rows, _ := s.app.Platform.LocalRows()
	got := map[int]anilist.LocalRow{}
	for _, r := range rows {
		got[r.MediaID] = r
	}
	if r, ok := got[21]; !ok || r.Status != "COMPLETED" || r.Progress != 3 || r.UpdatedAt != future/1000 {
		t.Fatalf("21 after the hub's: %+v", got[21])
	}
	if r, ok := got[22]; !ok || r.Status != "PLANNING" {
		t.Fatalf("22 after the hub's: %+v", got[22])
	}
	if hooks["lists"].Apply(hub) {
		t.Fatal("the same blob again changed something")
	}
	// The list shows them.
	w := do(s, "GET", "/api/anime/collection", local, "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("collection: %d %s", w.Code, w.Body)
	}

	// Watch order: an edit here is in the blob with its time; a removal
	// too; the hub's newer edit replaces it.
	if w := do(s, "PUT", "/api/watch-order/100", local, `{"order":[102,101],"hidden":[]}`, nil); w.Code != http.StatusOK {
		t.Fatalf("edit: %d %s", w.Code, w.Body)
	}
	wo := hooks["watch-order"].Own()
	var oi struct {
		At    int64
		Order []int
	}
	if raw, ok := wo.Items["100"]; !ok || json.Unmarshal(raw, &oi) != nil || len(oi.Order) != 2 || oi.Order[0] != 102 || oi.At <= 0 {
		t.Fatalf("own watch-order blob: %s", wo.Items["100"])
	}
	hubOrder := anikohub.Blob{Items: map[string]json.RawMessage{
		"100": json.RawMessage(`{"at":` + strconv.FormatInt(future, 10) + `,"order":[101,102],"hidden":[102]}`),
		"200": json.RawMessage(`{"at":` + strconv.FormatInt(future, 10) + `,"order":[201],"hidden":[]}`),
	}}
	if !hooks["watch-order"].Apply(hubOrder) {
		t.Fatal("the hub's orders changed nothing")
	}
	var e watchorder.Edit
	if ok, _ := s.app.DB.GetKV(watchorder.EditKey(100), &e); !ok || len(e.Order) != 2 || e.Order[0] != 101 || len(e.Hidden) != 1 {
		t.Fatalf("100 after the hub's: %+v", e)
	}
	if ok, _ := s.app.DB.GetKV(watchorder.EditKey(200), &e); !ok || len(e.Order) != 1 {
		t.Fatalf("200 after the hub's: %+v", e)
	}
	// An older edit from the hub doesn't undo a newer one here.
	if w := do(s, "DELETE", "/api/watch-order/200", local, "", nil); w.Code != http.StatusOK {
		t.Fatalf("remove: %d", w.Code)
	}
	if raw := hooks["watch-order"].Own().Items["200"]; !json.Valid(raw) || !itemDeleted(raw) {
		t.Fatalf("200 removed here: %s", raw)
	}
	older := anikohub.Blob{Items: map[string]json.RawMessage{
		"200": json.RawMessage(`{"at":` + strconv.FormatInt(future-10000, 10) + `,"order":[201],"hidden":[]}`),
	}}
	if hooks["watch-order"].Apply(older) {
		t.Fatal("the hub's older 200 came back")
	}
	if ok, _ := s.app.DB.GetKV(watchorder.EditKey(200), &e); ok {
		t.Fatal("200 is back")
	}
}

func itemDeleted(raw json.RawMessage) bool {
	var it struct{ Deleted bool }
	return json.Unmarshal(raw, &it) == nil && it.Deleted
}
