package api

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/v0-0x/mediaserver/anime/internal/share"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/v0-0x/mediaserver/anime/internal/extensions"
	"github.com/v0-0x/mediaserver/anime/internal/manga"
)

// LocalProvider reads a downloaded chapter (its file in the manga folder,
// as the chapter's id) from the manga folder.
const LocalProvider = "local"

// ServerProvider starts the provider of chapters downloaded onto another
// Aniko or mediaserver this one uses: server:<its ID>. They're read from
// there (its /api/peer/manga-library), as downloaded ones are from here.
const ServerProvider = "server:"

// serverOf is the host of a server: provider ("" for another provider).
func serverOf(provider string) string {
	host, ok := strings.CutPrefix(provider, ServerProvider)
	if !ok {
		return ""
	}
	return host
}

// Downloaded manga (manga.Library) and their downloads (manga.Downloader).
func (s *Server) mangaDownloadRoutes() {
	m := s.mux
	m.HandleFunc("GET /api/manga-downloads", h(func(r *http.Request) (any, error) { return s.app.MangaDL.List(), nil }))
	m.HandleFunc("POST /api/manga-downloads", h(func(r *http.Request) (any, error) {
		var body struct {
			MediaID  int                `json:"mediaId"`
			Title    string             `json:"title"`
			Provider string             `json:"provider"`
			Chapters []manga.ChapterRef `json:"chapters"`
			// Target: "" this device, else the media server's id.
			Target string `json:"target"`
		}
		if err := decode(r, &body); err != nil {
			return nil, err
		}
		if body.Provider == LocalProvider {
			return nil, badRequest("these chapters are downloaded already")
		}
		name := ""
		if body.Target != "" {
			for _, h := range s.app.Share.UploadHosts() {
				if h.ID == body.Target {
					name = h.Name
				}
			}
			if name == "" {
				return nil, badRequest("that server doesn't take files from this device")
			}
		}
		n, err := s.app.MangaDL.Add(body.MediaID, body.Title, body.Provider, body.Chapters, body.Target, name)
		if err != nil {
			return nil, badRequest(err.Error())
		}
		return map[string]int{"added": n}, nil
	}))
	m.HandleFunc("DELETE /api/manga-downloads/{id}", h(func(r *http.Request) (any, error) {
		if !s.app.MangaDL.Remove(r.PathValue("id")) {
			return nil, notFound("no such download")
		}
		return nil, nil
	}))
	m.HandleFunc("POST /api/manga-downloads/{id}/retry", h(func(r *http.Request) (any, error) {
		if !s.app.MangaDL.Retry(r.PathValue("id")) {
			return nil, notFound("no such failed download")
		}
		return nil, nil
	}))
	m.HandleFunc("POST /api/manga-downloads/clear", h(func(r *http.Request) (any, error) {
		s.app.MangaDL.Clear()
		return nil, nil
	}))

	// The manga downloaded onto the Anikos and servers this one uses, by
	// each (those that answer; an older one doesn't know manga).
	m.HandleFunc("GET /api/manga-shared", h(func(r *http.Request) (any, error) {
		return s.sharedManga(r, 0), nil
	}))
	m.HandleFunc("GET /api/manga-shared/series/{id}", h(func(r *http.Request) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		return s.sharedManga(r, id), nil
	}))
	m.HandleFunc("GET /api/manga-shared/cover", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.URL.Query().Get("id"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		s.app.Share.ForwardPath(w, r, r.URL.Query().Get("host"), "/api/peer/manga-library/"+strconv.Itoa(id)+"/cover")
	})
	m.HandleFunc("GET /api/manga-shared/page", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		s.app.Share.ForwardPath(w, r, q.Get("host"), "/api/peer/manga-library/page?"+url.Values{"file": {q.Get("file")}, "n": {q.Get("n")}}.Encode())
	})

	m.HandleFunc("GET /api/manga-library", h(func(r *http.Request) (any, error) {
		list, err := s.app.MangaLib.List()
		if err != nil {
			return nil, err
		}
		return map[string]any{"dir": s.app.MangaDir(), "series": list}, nil
	}))
	m.HandleFunc("GET /api/manga-library/{id}", h(func(r *http.Request) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		series, chapters, err := s.app.MangaLib.Series(id)
		if errors.Is(err, manga.ErrNotFound) {
			// Nothing downloaded of it: an empty list, not an error.
			return map[string]any{"series": nil, "chapters": []manga.LocalChapter{}}, nil
		}
		if err != nil {
			return nil, err
		}
		return map[string]any{"series": series, "chapters": chapters}, nil
	}))
	m.HandleFunc("DELETE /api/manga-library/{id}", h(trusted(func(r *http.Request) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		if err := s.app.MangaLib.Delete(id, r.URL.Query().Get("file")); err != nil {
			if errors.Is(err, manga.ErrNotFound) {
				return nil, notFound(err.Error())
			}
			return nil, err
		}
		s.app.Hub.Publish("manga-downloads", map[string]any{})
		return nil, nil
	})))
	m.HandleFunc("GET /api/manga-library/{id}/cover", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		p, err := s.app.MangaLib.Cover(id)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "private, max-age=600")
		http.ServeFile(w, r, p)
	})
	m.HandleFunc("GET /api/manga-library/page", func(w http.ResponseWriter, r *http.Request) {
		n, err := strconv.Atoi(r.URL.Query().Get("n"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		rc, ct, err := s.app.MangaLib.Page(r.URL.Query().Get("file"), n)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer rc.Close()
		w.Header().Set("Content-Type", ct)
		w.Header().Set("Cache-Control", "private, max-age=86400")
		_, _ = io.Copy(w, rc)
	})
}

// mangaPeerRoutes: the manga downloaded here, for the Anikos this one
// shares its library with (a server's, read on the PC and the TV).
func (s *Server) mangaPeerRoutes() {
	m := s.mux
	m.HandleFunc("GET /api/peer/manga-library", shared(h(func(r *http.Request) (any, error) {
		list, err := s.app.MangaLib.List()
		if err != nil {
			return nil, err
		}
		return map[string]any{"series": list}, nil
	})))
	m.HandleFunc("GET /api/peer/manga-library/{id}", shared(h(func(r *http.Request) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		series, chapters, err := s.app.MangaLib.Series(id)
		if errors.Is(err, manga.ErrNotFound) {
			return map[string]any{"series": nil, "chapters": []manga.LocalChapter{}}, nil
		}
		if err != nil {
			return nil, err
		}
		return map[string]any{"series": series, "chapters": chapters}, nil
	})))
	m.HandleFunc("GET /api/peer/manga-library/{id}/cover", shared(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		p, err := s.app.MangaLib.Cover(id)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "private, max-age=600")
		http.ServeFile(w, r, p)
	}))
	m.HandleFunc("GET /api/peer/manga-library/pages", shared(h(func(r *http.Request) (any, error) {
		n, err := s.app.MangaLib.Pages(r.URL.Query().Get("file"))
		if err != nil {
			return nil, notFound("this chapter isn't in the manga folder any more")
		}
		return map[string]int{"pages": n}, nil
	})))
	m.HandleFunc("GET /api/peer/manga-library/page", shared(func(w http.ResponseWriter, r *http.Request) {
		n, err := strconv.Atoi(r.URL.Query().Get("n"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		rc, ct, err := s.app.MangaLib.Page(r.URL.Query().Get("file"), n)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer rc.Close()
		w.Header().Set("Content-Type", ct)
		w.Header().Set("Cache-Control", "private, max-age=86400")
		_, _ = io.Copy(w, rc)
	}))
}

// sharedSeries is what one Aniko or server this one uses has downloaded:
// its series (id 0), or one series' chapters.
type sharedSeries struct {
	Host     share.PeerView       `json:"host"`
	Series   []manga.Series       `json:"series,omitempty"`
	Chapters []manga.LocalChapter `json:"chapters,omitempty"`
}

// sharedManga asks the Anikos and servers sharing with this one for their
// manga (id 0: all; else that one's chapters), at once, for a few seconds.
func (s *Server) sharedManga(r *http.Request, id int) []sharedSeries {
	libs := s.app.Share.Libraries()
	out := make([]sharedSeries, len(libs))
	var wg sync.WaitGroup
	for i, l := range libs {
		out[i].Host = l.Host
		if !l.Host.Online {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
			defer cancel()
			path := "/api/peer/manga-library"
			if id > 0 {
				path += "/" + strconv.Itoa(id)
			}
			raw, _, err := s.app.Share.AskHost(ctx, l.Host.ID, path, nil)
			if err != nil {
				return
			}
			var got struct {
				Series   []manga.Series       `json:"series"`
				Chapters []manga.LocalChapter `json:"chapters"`
			}
			if id > 0 {
				var one struct {
					Chapters []manga.LocalChapter `json:"chapters"`
				}
				_ = json.Unmarshal(raw, &one)
				got.Chapters = one.Chapters
			} else {
				_ = json.Unmarshal(raw, &got)
			}
			out[i].Series, out[i].Chapters = got.Series, got.Chapters
		}()
	}
	wg.Wait()
	kept := out[:0]
	for _, o := range out {
		if len(o.Series) > 0 || len(o.Chapters) > 0 {
			kept = append(kept, o)
		}
	}
	return kept
}

// serverPages lists a chapter's pages on another Aniko or server, for the
// reader: read through this one (shared/page).
func (s *Server) serverPages(r *http.Request, host, file string) ([]extensions.MangaPage, error) {
	raw, name, err := s.app.Share.AskHost(r.Context(), host, "/api/peer/manga-library/pages?"+url.Values{"file": {file}}.Encode(), nil)
	if err != nil {
		return nil, notFound(err.Error())
	}
	var got struct {
		Pages int `json:"pages"`
	}
	if json.Unmarshal(raw, &got) != nil || got.Pages <= 0 {
		return nil, notFound(name + " doesn't have this chapter any more")
	}
	out := make([]extensions.MangaPage, got.Pages)
	for i := range out {
		q := url.Values{"host": {host}, "file": {file}, "n": {strconv.Itoa(i)}}
		out[i] = extensions.MangaPage{URL: "/api/manga-shared/page?" + q.Encode(), Index: float64(i), Headers: map[string]string{}}
	}
	return out, nil
}

// localPages lists a downloaded chapter's pages, for the reader.
func (s *Server) localPages(file string) ([]extensions.MangaPage, error) {
	n, err := s.app.MangaLib.Pages(file)
	if err != nil {
		return nil, notFound("this chapter isn't in the manga folder any more")
	}
	out := make([]extensions.MangaPage, n)
	for i := range out {
		q := url.Values{"file": {file}, "n": {strconv.Itoa(i)}}
		out[i] = extensions.MangaPage{URL: "/api/manga-library/page?" + q.Encode(), Index: float64(i), Headers: map[string]string{}}
	}
	return out, nil
}
