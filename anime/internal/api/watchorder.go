package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/v0-0x/mediaserver/anime/internal/anilist"
	"github.com/v0-0x/mediaserver/anime/internal/config"
	"github.com/v0-0x/mediaserver/anime/internal/watchorder"
)

// The watch order of an anime's franchise (internal/watchorder), with your
// own story order on it; ?refresh=1 asks AniList again.
func (s *Server) watchOrder(r *http.Request) (any, error) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id <= 0 {
		return nil, badRequest("bad anime id")
	}
	refresh := r.URL.Query().Get("refresh") == "1"
	o, err := watchorder.Build(r.Context(), id, func(ctx context.Context, ids []int) (map[int]*anilist.Linked, error) {
		return s.app.Platform.Links(ctx, ids, refresh)
	})
	if errors.Is(err, watchorder.ErrNotFound) {
		return nil, notFound(err.Error())
	}
	if err != nil {
		return nil, err
	}
	var e watchorder.Edit
	if ok, _ := s.app.DB.GetKV(watchorder.EditKey(o.Key), &e); ok {
		watchorder.Apply(o, e)
	}
	return o, nil
}

// maxEdit caps what an edit may list.
const maxEdit = 1000

// Your story order for a franchise (its key), kept on this Aniko.
func (s *Server) putWatchOrder(r *http.Request) (any, error) {
	key, err := strconv.Atoi(r.PathValue("key"))
	if err != nil || key <= 0 {
		return nil, badRequest("bad franchise key")
	}
	var e watchorder.Edit
	if err := decode(r, &e); err != nil {
		return nil, err
	}
	if len(e.Order) > maxEdit || len(e.Hidden) > maxEdit {
		return nil, badRequest("too many anime")
	}
	if e.Order == nil {
		e.Order = []int{}
	}
	if e.Hidden == nil {
		e.Hidden = []int{}
	}
	return nil, s.app.DB.SetKV(watchorder.EditKey(key), e)
}

// reportWatchOrder keeps a note of a wrong order: Aniko's story order for
// the franchise and your edit of it, as a file in the data folder
// (watch-order-reports/<key>-<time>.json) to send along with a bug
// report, so the order can be put right for everyone.
func (s *Server) reportWatchOrder(r *http.Request) (any, error) {
	key, err := strconv.Atoi(r.PathValue("key"))
	if err != nil || key <= 0 {
		return nil, badRequest("bad franchise key")
	}
	var body struct {
		AnimeID int    `json:"animeId"`
		Note    string `json:"note"`
		watchorder.Edit
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	if len(body.Order) > maxEdit || len(body.Hidden) > maxEdit || len(body.Note) > 4000 {
		return nil, badRequest("too long")
	}
	if body.AnimeID <= 0 {
		body.AnimeID = key
	}
	// Aniko's own order, from what it knows (no refresh), without the edit.
	o, err := watchorder.Build(r.Context(), body.AnimeID, func(ctx context.Context, ids []int) (map[int]*anilist.Linked, error) {
		return s.app.Platform.Links(ctx, ids, false)
	})
	if err != nil {
		return nil, err
	}
	type line struct {
		ID       int    `json:"id"`
		Title    string `json:"title"`
		Kind     string `json:"kind,omitempty"`
		ByDate   bool   `json:"byDate,omitempty"`
		Conflict bool   `json:"conflict,omitempty"`
	}
	lines := func(es []watchorder.Entry) []line {
		out := make([]line, 0, len(es))
		for _, e := range es {
			l := line{Kind: e.Kind, ByDate: e.ByDate, Conflict: e.Conflict}
			if e.Media != nil {
				l.ID, l.Title = e.Media.ID, e.Media.PreferredTitle()
			}
			out = append(out, l)
		}
		return out
	}
	report := map[string]any{
		"aniko":   config.AppVersion,
		"at":      time.Now().UTC().Format(time.RFC3339),
		"key":     key,
		"animeId": body.AnimeID,
		"note":    strings.TrimSpace(body.Note),
		"story":   lines(o.Story),
		"release": lines(o.Release),
		"edit":    body.Edit,
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(s.app.DataDir, "watch-order-reports")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, fmt.Sprintf("%d-%s.json", key, time.Now().UTC().Format("20060102-150405")))
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return nil, err
	}
	return map[string]any{"path": path}, nil
}

// Back to Aniko's order.
func (s *Server) deleteWatchOrder(r *http.Request) (any, error) {
	key, err := strconv.Atoi(r.PathValue("key"))
	if err != nil || key <= 0 {
		return nil, badRequest("bad franchise key")
	}
	return nil, s.app.DB.DeleteKV(watchorder.EditKey(key))
}
