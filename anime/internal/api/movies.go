package api

import (
	"net/http"
	"strings"
)

// mediaserver's one dashboard: its movies and TV pages ask the movies side
// (KumoBox's server, on its own port) through this one, at /api/movies/...,
// so they're on the dashboard's address with its sign-in. The movies side
// gets them as from its own computer: only the dashboard's owner (or this
// computer) is let through here.
// SetMovies gives the dashboard the movies side's API (it starts after this
// one).
func (s *Server) SetMovies(h http.Handler) { s.movies.Store(&h) }

func (s *Server) moviesRoute() {
	s.mux.HandleFunc("/api/movies/", func(w http.ResponseWriter, r *http.Request) {
		movies := s.movies.Load()
		if movies == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no movies side here"})
			return
		}
		if !isTrusted(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "sign in to the dashboard first", "passwordRequired": true})
			return
		}
		in := r.Clone(r.Context())
		in.URL.Path = "/api/" + strings.TrimPrefix(r.URL.Path, "/api/movies/")
		in.URL.RawPath = ""
		in.RequestURI = in.URL.RequestURI()
		in.RemoteAddr = "127.0.0.1:0"
		in.Host = "localhost"
		in.Header = r.Header.Clone()
		// Its own checks see a request of its own pages.
		in.Header.Del("Cookie")
		in.Header.Del("Origin")
		in.Header.Set("Sec-Fetch-Site", "same-origin")
		(*movies).ServeHTTP(w, in)
	})
}
