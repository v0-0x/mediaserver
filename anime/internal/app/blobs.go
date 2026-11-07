package app

import (
	"encoding/json"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/v0-0x/mediaserver/anime/internal/anikohub"
	"github.com/v0-0x/mediaserver/anime/internal/anilist"
	"github.com/v0-0x/mediaserver/anime/internal/share"
	"github.com/v0-0x/mediaserver/anime/internal/watchorder"
)

// The device's side of what a hub keeps in step besides the settings
// (share.BlobHooks, anikohub.Blob): the local list (a device on no AniList
// account) and the watch-order edits. Each item carries when it changed
// here (whole seconds, as ms); what was removed is remembered for a while
// (a tombstone), so the other devices remove it too.

const (
	listsGoneKey      = "sync:lists-gone"       // media ID -> removed at (ms)
	watchOrderAtKey   = "sync:watch-order-at"   // franchise key -> edited at (ms)
	watchOrderGoneKey = "sync:watch-order-gone" // franchise key -> removed at (ms)
)

// nowAt is the time as items carry it: whole seconds, in ms.
func nowAt() int64 { return time.Now().Unix() * 1000 }

func (a *App) kvMap(key string) map[string]int64 {
	m := map[string]int64{}
	_, _ = a.DB.GetKV(key, &m)
	return m
}

func (a *App) wireBlobs() {
	a.Share.Blobs = map[string]share.BlobHooks{
		"lists":       {Own: a.listsBlob, Apply: a.applyListsBlob},
		"watch-order": {Own: a.watchOrderBlob, Apply: a.applyWatchOrderBlob},
	}
	a.Platform.LocalListChanged = func(mediaID int, deleted bool) {
		key := strconv.Itoa(mediaID)
		gone := a.kvMap(listsGoneKey)
		if deleted {
			gone[key] = nowAt()
		} else {
			delete(gone, key)
		}
		_ = a.DB.SetKV(listsGoneKey, gone)
		a.Share.BlobChanged("lists")
	}
}

// listItem is a local list entry as a blob item.
type listItem struct {
	At       int64           `json:"at"`
	Deleted  bool            `json:"deleted,omitempty"`
	Type     string          `json:"type,omitempty"`
	Status   string          `json:"status,omitempty"`
	Progress int             `json:"progress,omitempty"`
	Score    float64         `json:"score,omitempty"`
	Repeat   int             `json:"repeat,omitempty"`
	Media    json.RawMessage `json:"media,omitempty"`
}

func (a *App) listsBlob() anikohub.Blob {
	b := anikohub.Blob{Items: map[string]json.RawMessage{}}
	rows, err := a.Platform.LocalRows()
	if err != nil {
		log.Printf("sharing: the local list: %v", err)
		return b
	}
	for _, r := range rows {
		b.Items[strconv.Itoa(r.MediaID)], _ = json.Marshal(listItem{At: r.UpdatedAt * 1000, Type: r.Type, Status: r.Status, Progress: r.Progress, Score: r.Score, Repeat: r.Repeat, Media: r.Media})
	}
	for key, at := range a.kvMap(listsGoneKey) {
		if _, ok := b.Items[key]; !ok {
			b.Items[key], _ = json.Marshal(listItem{At: at, Deleted: true})
		}
	}
	b.Merge(anikohub.Blob{}) // sets UpdatedAt, drops old tombstones
	return b
}

func (a *App) applyListsBlob(b anikohub.Blob) bool {
	rows, err := a.Platform.LocalRows()
	if err != nil {
		return false
	}
	have := map[string]anilist.LocalRow{}
	for _, r := range rows {
		have[strconv.Itoa(r.MediaID)] = r
	}
	gone := a.kvMap(listsGoneKey)
	changed, goneChanged := false, false
	for key, raw := range b.Items {
		var it listItem
		if json.Unmarshal(raw, &it) != nil || it.At <= 0 {
			continue
		}
		id, err := strconv.Atoi(key)
		if err != nil || id <= 0 {
			continue
		}
		// What's here, and when: a removal at the same time wins (as on
		// the hub, anikohub.Blob).
		localAt, localGone := gone[key], true
		if r, ok := have[key]; ok {
			localAt, localGone = r.UpdatedAt*1000, false
		}
		if it.At < localAt || it.At == localAt && (!it.Deleted || localGone) {
			continue
		}
		if it.Deleted {
			if _, ok := have[key]; ok {
				if _, err := a.Platform.DeleteLocalRow(id); err != nil {
					continue
				}
				changed = true
			}
			gone[key], goneChanged = it.At, true
			continue
		}
		if len(it.Media) == 0 {
			continue
		}
		if err := a.Platform.PutLocalRow(anilist.LocalRow{MediaID: id, Type: it.Type, Status: it.Status, Progress: it.Progress, Score: it.Score, Repeat: it.Repeat, Media: it.Media, UpdatedAt: it.At / 1000}); err != nil {
			continue
		}
		changed = true
		if _, ok := gone[key]; ok {
			delete(gone, key)
			goneChanged = true
		}
	}
	if goneChanged {
		_ = a.DB.SetKV(listsGoneKey, gone)
	}
	return changed
}

// orderItem is a franchise's edited order as a blob item.
type orderItem struct {
	At      int64 `json:"at"`
	Deleted bool  `json:"deleted,omitempty"`
	Order   []int `json:"order,omitempty"`
	Hidden  []int `json:"hidden,omitempty"`
}

// WatchOrderEdited notes an edit made here (or its removal: deleted), for
// the devices it's kept in step with.
func (a *App) WatchOrderEdited(key int, deleted bool) {
	k := strconv.Itoa(key)
	at, gone := a.kvMap(watchOrderAtKey), a.kvMap(watchOrderGoneKey)
	// Later than the last change of it here, so an edit right after a
	// removal (or the other way) still counts as newer.
	when := max(nowAt(), at[k]+1000, gone[k]+1000)
	if deleted {
		delete(at, k)
		gone[k] = when
	} else {
		at[k] = when
		delete(gone, k)
	}
	_ = a.DB.SetKV(watchOrderAtKey, at)
	_ = a.DB.SetKV(watchOrderGoneKey, gone)
	a.Share.BlobChanged("watch-order")
}

func (a *App) watchOrderBlob() anikohub.Blob {
	b := anikohub.Blob{Items: map[string]json.RawMessage{}}
	edits, err := a.DB.KVPrefix(watchorder.EditKey(0)[:len(watchorder.EditKey(0))-1])
	if err != nil {
		return b
	}
	at := a.kvMap(watchOrderAtKey)
	atChanged := false
	for k, raw := range edits {
		key := strings.TrimPrefix(k, "watch-order:")
		var e watchorder.Edit
		if json.Unmarshal(raw, &e) != nil {
			continue
		}
		when, ok := at[key]
		if !ok {
			// Edited before edits were kept in step: from now.
			when, at[key], atChanged = nowAt(), nowAt(), true
		}
		b.Items[key], _ = json.Marshal(orderItem{At: when, Order: e.Order, Hidden: e.Hidden})
	}
	if atChanged {
		_ = a.DB.SetKV(watchOrderAtKey, at)
	}
	for key, when := range a.kvMap(watchOrderGoneKey) {
		if _, ok := b.Items[key]; !ok {
			b.Items[key], _ = json.Marshal(orderItem{At: when, Deleted: true})
		}
	}
	b.Merge(anikohub.Blob{})
	return b
}

func (a *App) applyWatchOrderBlob(b anikohub.Blob) bool {
	at, gone := a.kvMap(watchOrderAtKey), a.kvMap(watchOrderGoneKey)
	changed := false
	for key, raw := range b.Items {
		var it orderItem
		if json.Unmarshal(raw, &it) != nil || it.At <= 0 {
			continue
		}
		n, err := strconv.Atoi(key)
		if err != nil || n <= 0 {
			continue
		}
		localAt, localGone := at[key], false
		if g := gone[key]; g > localAt {
			localAt, localGone = g, true
		}
		if it.At < localAt || it.At == localAt && (!it.Deleted || localGone) {
			continue
		}
		if it.Deleted {
			_ = a.DB.DeleteKV(watchorder.EditKey(n))
			delete(at, key)
			gone[key] = it.At
		} else {
			if err := a.DB.SetKV(watchorder.EditKey(n), watchorder.Edit{Order: it.Order, Hidden: it.Hidden}); err != nil {
				continue
			}
			at[key] = it.At
			delete(gone, key)
		}
		changed = true
	}
	if changed {
		_ = a.DB.SetKV(watchOrderAtKey, at)
		_ = a.DB.SetKV(watchOrderGoneKey, gone)
	}
	return changed
}
