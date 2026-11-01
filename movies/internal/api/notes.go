package api

import (
	"context"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/v0-0x/mediaserver/movies/internal/share"
)

// A player's notes: what went wrong while it played (no picture, say),
// for the log here and, for a shared file, its host's, where it can be
// looked into. A few at most, from each.
type noteGuard struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func (g *noteGuard) allow(from string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	if g.last == nil || len(g.last) > 64 {
		g.last = map[string]time.Time{}
	}
	if now.Sub(g.last[from]) < 2*time.Second {
		return false
	}
	g.last[from] = now
	return true
}

// cleanNote is a note as one line of the log, not too long.
func cleanNote(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > 300 {
		s = string(r[:300]) + "…"
	}
	return s
}

func (s *Server) noteRoutes() {
	m := s.mux
	m.HandleFunc("POST /api/playback/note", h(func(r *http.Request) (any, error) {
		var body struct {
			Path string `json:"path"`
			Text string `json:"text"`
		}
		if err := decode(r, &body); err != nil {
			return nil, err
		}
		from, _, _ := net.SplitHostPort(r.RemoteAddr)
		text := cleanNote(body.Text)
		if text == "" || !s.notes.allow(from) {
			return nil, nil
		}
		log.Printf("player: %s", text)
		if share.IsRemote(body.Path) {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = s.app.Share.Note(ctx, body.Path, text)
			}()
		}
		return nil, nil
	}))
	m.HandleFunc("POST /api/peer/note", shared(h(func(r *http.Request) (any, error) {
		var body struct {
			Text string `json:"text"`
		}
		if err := decode(r, &body); err != nil {
			return nil, err
		}
		c := peerOf(r)
		if text := cleanNote(body.Text); text != "" && s.notes.allow("peer "+c.ID) {
			log.Printf("sharing: the player on %s: %s", cleanNote(c.Name), text)
		}
		return nil, nil
	})))
}
