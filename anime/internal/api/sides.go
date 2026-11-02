package api

import (
	"net/http"
	"strings"
	"sync/atomic"
)

// mediaserver's one dashboard: its Movies and TV pages ask the movies side
// (Moviko's server, on its own port) through this one, at /api/movies/...,
// and its Music pages the music side (Utako's server), at /api/music/..., so
// they're on the dashboard's address with its sign-in. The other side gets
// them as from its own computer: only the dashboard's owner (or this
// computer) is let through here.

// SetMovies gives the dashboard the movies side's API (it starts after this
// one).
func (s *Server) SetMovies(h http.Handler) { s.movies.Store(&h) }

// SetMusic gives the dashboard the music side's API.
func (s *Server) SetMusic(h http.Handler) { s.music.Store(&h) }

func (s *Server) sideRoutes() {
	s.sideRoute("movies", &s.movies)
	s.sideRoute("music", &s.music)
}

func (s *Server) sideRoute(name string, side *atomic.Pointer[http.Handler]) {
	prefix := "/api/" + name + "/"
	s.mux.HandleFunc(prefix, func(w http.ResponseWriter, r *http.Request) {
		h := side.Load()
		if h == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no " + name + " side here"})
			return
		}
		if !isTrusted(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "sign in to the dashboard first", "passwordRequired": true})
			return
		}
		in := r.Clone(r.Context())
		in.URL.Path = "/api/" + strings.TrimPrefix(r.URL.Path, prefix)
		in.URL.RawPath = ""
		in.RequestURI = in.URL.RequestURI()
		in.RemoteAddr = "127.0.0.1:0"
		in.Host = "localhost"
		in.Header = r.Header.Clone()
		// Its own checks see a request of its own pages.
		in.Header.Del("Cookie")
		in.Header.Del("Origin")
		in.Header.Set("Sec-Fetch-Site", "same-origin")
		(*h).ServeHTTP(w, in)
	})
}
