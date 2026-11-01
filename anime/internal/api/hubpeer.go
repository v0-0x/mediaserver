package api

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/v0-0x/mediaserver/anime/internal/anikohub"
	"github.com/v0-0x/mediaserver/anime/internal/share"
)

// hubPeerRoutes are what an Aniko Hub answers the devices it shares with
// (share/hub.go): their account's settings, and the logins they give it.
// Elsewhere they say it isn't a hub.
func (s *Server) hubPeerRoutes() {
	m := s.mux
	hub := func(fn http.HandlerFunc) http.HandlerFunc {
		return shared(func(w http.ResponseWriter, r *http.Request) {
			if !s.app.Share.IsHub() {
				writeJSON(w, http.StatusNotFound, map[string]any{"error": "this Aniko isn't a hub"})
				return
			}
			fn(w, r)
		})
	}
	m.HandleFunc("GET /api/peer/hub/prefs", hub(h(func(r *http.Request) (any, error) {
		return s.app.Share.HubPrefs(peerOf(r))
	})))
	m.HandleFunc("POST /api/peer/hub/prefs", hub(h(func(r *http.Request) (any, error) {
		var p anikohub.Prefs
		if err := json.NewDecoder(io.LimitReader(r.Body, 128<<10)).Decode(&p); err != nil {
			return nil, badRequest(err.Error())
		}
		return nil, s.app.Share.TakeHubPrefs(peerOf(r), p)
	})))
	m.HandleFunc("POST /api/peer/hub/account", hub(h(func(r *http.Request) (any, error) {
		var g share.GivenAccount
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&g); err != nil {
			return nil, badRequest(err.Error())
		}
		acc, err := s.app.Share.TakeAccount(r.Context(), peerOf(r), g)
		if err != nil {
			return nil, badRequest(err.Error())
		}
		s.app.Hub.Publish("hub-updated", nil)
		return acc, nil
	})))
	m.HandleFunc("POST /api/peer/hub/imported", hub(h(func(r *http.Request) (any, error) {
		var body struct {
			What []string `json:"what"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil {
			return nil, badRequest(err.Error())
		}
		s.app.Share.Imported(peerOf(r), body.What)
		return nil, nil
	})))
}
