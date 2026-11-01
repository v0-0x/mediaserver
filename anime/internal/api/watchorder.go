package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/v0-0x/mediaserver/anime/internal/anilist"
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

// Back to Aniko's order.
func (s *Server) deleteWatchOrder(r *http.Request) (any, error) {
	key, err := strconv.Atoi(r.PathValue("key"))
	if err != nil || key <= 0 {
		return nil, badRequest("bad franchise key")
	}
	return nil, s.app.DB.DeleteKV(watchorder.EditKey(key))
}
